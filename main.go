package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/getlantern/systray"

	"log-server/internal/api"
	"log-server/internal/database"
	"log-server/internal/supabase"
)

//go:embed all:web
var webFS embed.FS

//go:embed icon.ico
var trayIcon []byte

const version = "1.3.1"

// ANSI color codes for terminal output
const (
	colorReset  = "\033[0m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorRed    = "\033[31m"
	colorCyan   = "\033[36m"
	colorBold   = "\033[1m"
)

var serverAddr string

// colorEnabled is detected once at startup based on the terminal capabilities.
var colorEnabled = detectColorSupport()

// noColor forces plain output when --no-color is passed.
var noColor bool

// quitChan is a global channel used to signal shutdown from the tray menu.
var quitChan = make(chan os.Signal, 1)

func main() {
	addr := flag.String("addr", ":8080", "HTTP listen address")
	turso := flag.Bool("turso", false, "Use Turso remote database instead of local SQLite")
	supabaseURL := flag.String("supabase-url", "", "Supabase project URL (https://<project>.supabase.co)")
	supabaseKey := flag.String("supabase-key", "", "Supabase anonymous key for REST API")
	dbPath := flag.String("db", "logs.db", "SQLite database file path (or Turso URL like libsql://...)")
	dbToken := flag.String("db-token", "", "Turso authentication token (for remote databases)")
	uploadDir := flag.String("uploads", "uploads", "Directory for uploaded files")
	maxUpload := flag.Int64("max-upload", 1<<30, "Max upload size in bytes (default 1 GiB)")
	debug := flag.Bool("debug", false, "Enable debug logging")
	noColorFlag := flag.Bool("no-color", false, "Disable colored terminal output")
	flag.Parse()
	serverAddr = *addr
	noColor = *noColorFlag

	// Load optional configuration from config.json (used as defaults for Supabase and Turso)
	var fileCfg struct {
		SupabaseURL string `json:"supabase_url"`
		SupabaseKey string `json:"supabase_key"`
		TursoURL    string `json:"turso_url"`
		TursoToken  string `json:"turso_token"`
	}
	if cfgFile, err := os.ReadFile("config.json"); err == nil {
		_ = json.Unmarshal(cfgFile, &fileCfg)
	}

	// If --turso flag is set, use Turso URL and token from config.json unless overridden by flags
	if *turso {
		if *dbPath == "logs.db" {
			*dbPath = fileCfg.TursoURL
		}
		if *dbToken == "" {
			*dbToken = fileCfg.TursoToken
		}
	}

	// If no Supabase flags provided, use config.json values
	if *supabaseURL == "" {
		*supabaseURL = fileCfg.SupabaseURL
	}
	if *supabaseKey == "" {
		*supabaseKey = fileCfg.SupabaseKey
	}

	// Determine log level based on --debug flag
	level := slog.LevelInfo
	if *debug {
		level = slog.LevelDebug
	}

	// Custom handler with colored output
	handler := &coloredHandler{
		w:     os.Stderr,
		level: level,
		color: isTerminalColorSupported(),
	}
	logger := slog.New(handler)
	slog.SetDefault(logger)

	// Propagate color support to the API package
	api.SetColorEnabled(isTerminalColorSupported())

	// Print startup banner
	printBanner(*addr, *dbPath, *supabaseURL)

	// Build DSN for Turso or local SQLite
	dsn := buildDSN(*dbPath, *dbToken)

	db, err := database.Open(dsn)
	if err != nil {
		printError("Database connection failed: %v", err)
		os.Exit(1)
	}
	defer db.Close()
	printSuccess("Database: Connected (%s)", *dbPath)

	// Open Turso database (if configured in config.json)
	var dbTurso *database.DB
	if fileCfg.TursoURL != "" {
		dbTurso, err = database.Open(buildDSN(fileCfg.TursoURL, fileCfg.TursoToken))
		if err != nil {
			printWarn("Turso database not available (%v). Only local DB will be used.", err)
		} else {
			defer dbTurso.Close()
			printSuccess("Database: Connected (%s)", fileCfg.TursoURL)
		}
	}

	broker := api.NewBroker()
	defer broker.Stop()
	printSuccess("Real-time: Enabled (SSE)")

	// Supabase client (optional)
	var supabaseClient *supabase.Client
	if *supabaseURL != "" {
		sc, err := supabase.New(*supabaseURL, *supabaseKey)
		if err != nil {
			printError("Supabase setup failed: %v", err)
		} else {
			supabaseClient = sc
			printSuccess("Supabase: Connected (%s)", *supabaseURL)
			// printInfo("Supabase: Signup/login via GoTrue, data via PostgREST")
		}
	}

	web, err := fs.Sub(webFS, "web")
	if err != nil {
		printError("Embedded web fs: %v", err)
		os.Exit(1)
	}

	srv := api.NewServer(db, dbTurso, supabaseClient, broker, api.Config{
		UploadDir:   *uploadDir,
		MaxUpload:   *maxUpload,
		WebFS:       web,
		SupabaseURL: *supabaseURL,
		SupabaseKey: *supabaseKey,
	})

	httpSrv := &http.Server{
		Addr:              *addr,
		Handler:           withMiddleware(srv, *debug),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      0,
		IdleTimeout:       120 * time.Second,
	}

	// Register OS signals for graceful shutdown. The same channel is used by the tray menu.
	signal.Notify(quitChan, os.Interrupt, syscall.SIGTERM)

	// Start HTTP server in background
	go func() {
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			printError("HTTP server: %v", err)
			os.Exit(1)
		}
	}()

	// Show ready info before tray starts
	printReady(*addr)

	// Initialise system tray
	go func() {
		systray.Run(onReady, onExit)
	}()

	// Wait for quit signal (OS or tray)
	<-quitChan

	printWarn("Shutdown requested")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(ctx); err != nil {
		printError("Shutdown error: %v", err)
	}
	printInfo("Server stopped")
}

// onReady sets up the tray menu items.
func onReady() {
	// Set the tray icon using the embedded .ico file.
	systray.SetIcon(trayIcon)
	systray.SetTitle("Log Server")
	systray.SetTooltip("Log Management System")

	openItem := systray.AddMenuItem("Open UI", "Open the web UI in the default browser")
	quitItem := systray.AddMenuItem("Quit", "Exit the application")

	go func() {
		for {
			select {
			case <-openItem.ClickedCh:
				// Open the server address in default browser (Windows specific)
				url := "http://localhost" + serverAddr
				exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
			case <-quitItem.ClickedCh:
				// Signal shutdown via SIGTERM to trigger graceful shutdown.
				quitChan <- syscall.SIGTERM
				systray.Quit()
				return
			}
		}
	}()
}

// onExit is called when the tray exits.
func onExit() {
	// No additional cleanup needed; main will handle server shutdown.
}

// buildDSN builds the DSN for libsql (Turso) or local SQLite
func buildDSN(dbPath, dbToken string) string {
	// If it's a Turso URL (libsql:// or libsqls://), use it with auth token
	if strings.HasPrefix(dbPath, "libsql://") || strings.HasPrefix(dbPath, "libsqls://") {
		if dbToken != "" {
			return fmt.Sprintf("%s?authToken=%s", dbPath, dbToken)
		}
		return dbPath
	}
	// Local SQLite file - use WAL mode and foreign keys
	return fmt.Sprintf("file:%s?_journal_mode=WAL&_foreign_keys=on&_busy_timeout=5000", dbPath)
}

// withMiddleware applies request logging (only in debug mode) and recovery.
func withMiddleware(h http.Handler, debug bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		defer func() {
			if rec := recover(); rec != nil {
				slog.Error("panic", "err", rec, "path", r.URL.Path)
				http.Error(w, "internal server error", http.StatusInternalServerError)
			}
		}()
		h.ServeHTTP(sw, r)
		// Only log in debug mode, and skip SSE/static to reduce noise
		if debug && r.URL.Path != "/api/events" && !strings.HasPrefix(r.URL.Path, "/static/") {
			slog.Debug("http",
				"method", r.Method,
				"path", r.URL.Path,
				"status", sw.status,
				"remote", r.RemoteAddr,
			)
		}
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (sw *statusWriter) WriteHeader(code int) {
	if !sw.wrote {
		sw.status = code
		sw.wrote = true
	}
	sw.ResponseWriter.WriteHeader(code)
}

func (sw *statusWriter) Write(b []byte) (int, error) {
	if !sw.wrote {
		sw.wrote = true
	}
	return sw.ResponseWriter.Write(b)
}

// Flush implements http.Flusher for SSE support
func (sw *statusWriter) Flush() {
	if flusher, ok := sw.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// ============ Colored Terminal Output ============

type coloredHandler struct {
	w     io.Writer
	level slog.Level
	color bool
}

func (h *coloredHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *coloredHandler) Handle(_ context.Context, r slog.Record) error {
	if r.Level < h.level {
		return nil
	}

	var buf string
	if h.color {
		var levelColor string
		switch r.Level {
		case slog.LevelDebug:
			levelColor = colorCyan
		case slog.LevelInfo:
			levelColor = colorGreen
		case slog.LevelWarn:
			levelColor = colorYellow
		case slog.LevelError:
			levelColor = colorRed
		default:
			levelColor = colorReset
		}

		buf = fmt.Sprintf("%s[%s]%s %s\n",
			levelColor,
			strings.ToUpper(r.Level.String()),
			colorReset,
			r.Message,
		)
	} else {
		buf = fmt.Sprintf("[%s] %s\n", strings.ToUpper(r.Level.String()), r.Message)
	}

	// Add attributes in debug mode
	if h.level <= slog.LevelDebug && r.NumAttrs() > 0 {
		if h.color {
			buf += colorCyan
		}
		r.Attrs(func(a slog.Attr) bool {
			buf += fmt.Sprintf("  %s=%v", a.Key, a.Value)
			return true
		})
		if h.color {
			buf += colorReset + "\n"
		} else {
			buf += "\n"
		}
	}

	_, err := h.w.Write([]byte(buf))
	return err
}

func (h *coloredHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return h
}

func (h *coloredHandler) WithGroup(name string) slog.Handler {
	return h
}

// ============ Banner Functions ============

func printBanner(addr, dbPath, supabaseURL string) {
	isColor := isTerminalColorSupported()

	sep := "═══════════════════════════════════════════════════════════"
	if !isColor {
		sep = strings.Repeat("=", 63)
	}

	fmt.Println()
	if isColor {
		fmt.Println(colorCyan + sep + colorReset)
		fmt.Println(colorBold + "                    Log Management System" + colorReset)
		fmt.Println(colorCyan + sep + colorReset)
		fmt.Printf("%sVersion:%s %s\n", colorCyan, colorReset, version)
		fmt.Printf("%sPlatform:%s %s / %s\n", colorCyan, colorReset, runtime.GOOS, runtime.GOARCH)
	} else {
		fmt.Println(sep)
		fmt.Println("                    Log Management System")
		fmt.Println(sep)
		fmt.Printf("Version: %s\n", version)
		fmt.Printf("Platform: %s / %s\n", runtime.GOOS, runtime.GOARCH)
	}
	fmt.Println()
	if isColor {
		fmt.Printf("%sAddress:%s %s\n", colorCyan, colorReset, addr)
		fmt.Printf("%sDatabase:%s %s\n", colorCyan, colorReset, dbPath)
	} else {
		fmt.Printf("Address: %s\n", addr)
		fmt.Printf("Database: %s\n", dbPath)
	}
	fmt.Println()
}

func printSuccess(format string, args ...interface{}) {
	if isTerminalColorSupported() {
		fmt.Printf(colorGreen+format+colorReset+"\n", args...)
	} else {
		fmt.Printf("[OK] "+format+"\n", args...)
	}
}

func printError(format string, args ...interface{}) {
	if isTerminalColorSupported() {
		fmt.Printf(colorRed+format+colorReset+"\n", args...)
	} else {
		fmt.Printf("[ERROR] "+format+"\n", args...)
	}
}

func printWarn(format string, args ...interface{}) {
	if isTerminalColorSupported() {
		fmt.Printf(colorYellow+format+colorReset+"\n", args...)
	} else {
		fmt.Printf("[WARN] "+format+"\n", args...)
	}
}

func printInfo(format string, args ...interface{}) {
	if isTerminalColorSupported() {
		fmt.Printf(colorCyan+format+colorReset+"\n", args...)
	} else {
		fmt.Printf("[INFO] "+format+"\n", args...)
	}
}

func printReady(addr string) {
	isColor := isTerminalColorSupported()
	localIPs := getLocalIPs()
	defaultIP := pickDefaultIP(localIPs)
	port := extractPort(addr)
	if port == "" {
		port = "8080"
	}
	fmt.Println()
	if isColor {
		fmt.Println(colorGreen + "═══════════════════════════════════════════════════════════" + colorReset)
		fmt.Println(colorBold + colorGreen + "                      Server Ready" + colorReset)
		fmt.Println(colorGreen + "═══════════════════════════════════════════════════════════" + colorReset)
		fmt.Println()
		fmt.Printf("  %sAccess from this device:%s\n", colorBold, colorReset)
		fmt.Printf("    • %shttp://localhost:%s %s\n", colorCyan, port, colorReset)
		fmt.Printf("    • %shttp://127.0.0.1:%s %s\n", colorCyan, port, colorReset)
		fmt.Println()
		if defaultIP != "" {
			fmt.Printf("  %sAccess from other devices on the same network:%s\n", colorBold, colorReset)
			fmt.Printf("    • %shttp://%s:%s %s\n", colorCyan, defaultIP, port, colorReset)
			fmt.Println()
			fmt.Printf("  %sOther detected IPs:%s\n", colorBold, colorReset)
			for _, ip := range localIPs {
				if ip != defaultIP {
					fmt.Printf("    • %shttp://%s:%s %s\n", colorCyan, ip, port, colorReset)
				}
			}
			fmt.Println()
		} else {
			fmt.Printf("  %sNo valid network IP found.%s\n", colorYellow, colorReset)
			fmt.Println()
		}
		fmt.Printf("  %sTroubleshooting:%s\n", colorBold, colorReset)
		fmt.Println("    1. Make sure your phone is on the same WiFi network")
		fmt.Println("    2. Check Windows Firewall: allow port 8080")
		fmt.Println("    3. Disable VPN or virtual adapters if not needed")
		fmt.Println()
		fmt.Println(colorGreen + "═══════════════════════════════════════════════════════════" + colorReset)
	} else {
		fmt.Println(strings.Repeat("=", 63))
		fmt.Println("                    Server Ready")
		fmt.Println(strings.Repeat("=", 63))
		fmt.Println()
		fmt.Println("  Access from this device:")
		fmt.Printf("    • http://localhost:%s\n", port)
		fmt.Printf("    • http://127.0.0.1:%s\n", port)
		fmt.Println()
		if defaultIP != "" {
			fmt.Println("  Access from other devices on the same network:")
			fmt.Printf("    • http://%s:%s\n", defaultIP, port)
			fmt.Println()
			fmt.Println("  Other detected IPs:")
			for _, ip := range localIPs {
				if ip != defaultIP {
					fmt.Printf("    • http://%s:%s\n", ip, port)
				}
			}
			fmt.Println()
		} else {
			fmt.Println("  No valid network IP found.")
			fmt.Println()
		}
		fmt.Println("  Troubleshooting:")
		fmt.Println("    1. Make sure your phone is on the same WiFi network")
		fmt.Println("    2. Check Windows Firewall: allow port 8080")
		fmt.Println("    3. Disable VPN or virtual adapters if not needed")
		fmt.Println()
	}
}

func getLocalIPs() []string {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil
	}

	seen := make(map[string]struct{})
	var ips []string

	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}

		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}

		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}

			if ip == nil || ip.IsLoopback() {
				continue
			}

			ip = ip.To4()
			if ip == nil {
				continue
			}

			ipStr := ip.String()
			if strings.HasPrefix(ipStr, "169.254.") {
				continue
			}
			if _, ok := seen[ipStr]; ok {
				continue
			}
			seen[ipStr] = struct{}{}
			ips = append(ips, ipStr)
		}
	}

	return ips
}

func pickDefaultIP(localIPs []string) string {
	for _, ip := range localIPs {
		if !strings.HasPrefix(ip, "169.254.") && !strings.HasPrefix(ip, "172.") && !strings.HasPrefix(ip, "192.168.") {
			return ip
		}
	}
	if len(localIPs) > 0 {
		return localIPs[0]
	}
	return ""
}

func extractPort(addr string) string {
	if _, port, err := net.SplitHostPort(addr); err == nil && port != "" {
		return port
	}
	if strings.HasPrefix(addr, ":") {
		return strings.TrimPrefix(addr, ":")
	}
	if n, err := strconv.Atoi(addr); err == nil && n > 0 {
		return addr
	}
	return ""
}

func detectColorSupport() bool {
	// Standard convention: NO_COLOR present (any value) disables colors.
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	// TERM=dumb signals a terminal without ANSI support.
	if strings.EqualFold(os.Getenv("TERM"), "dumb") {
		return false
	}
	return consoleColorSupported()
}

func isTerminalColorSupported() bool {
	if noColor {
		return false
	}
	return colorEnabled
}
