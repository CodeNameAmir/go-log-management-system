package api

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"log-server/internal/database"
)

// Server wires together the HTTP routes and shared dependencies.
type Server struct {
	mux       *http.ServeMux
	broker    *Broker
	webFS     fs.FS
	uploadDir string
	maxUpload int64
	db        *database.DB
	dbTurso   *database.DB
	sessions  *SessionManager
}

// Config holds tunables for the HTTP server.
type Config struct {
	UploadDir string
	MaxUpload int64 // bytes
	WebFS     fs.FS
}

// NewServer constructs a Server with all routes registered.
func NewServer(db *database.DB, dbTurso *database.DB, broker *Broker, cfg Config) *Server {
	if cfg.MaxUpload <= 0 {
		cfg.MaxUpload = 1 << 30 // 1 GiB
	}
	if cfg.UploadDir == "" {
		cfg.UploadDir = "uploads"
	}

	s := &Server{
		mux:       http.NewServeMux(),
		broker:    broker,
		webFS:     cfg.WebFS,
		uploadDir: cfg.UploadDir,
		maxUpload: cfg.MaxUpload,
		db:        db,
		dbTurso:  dbTurso,
		sessions:  NewSessionManager(),
	}

	// When Turso DB is not available, use local DB for everything.
	// The auth handler will still allow selection via session.
	if s.dbTurso == nil {
		s.dbTurso = db
	}

	if err := os.MkdirAll(cfg.UploadDir, 0o755); err != nil {
		slog.Warn("could not create upload dir", "err", err, "path", cfg.UploadDir)
	}

	s.routes()
	return s
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

// routes registers all endpoints.
func (s *Server) routes() {
	// Auth (public)
	s.mux.HandleFunc("POST /api/signup", s.handleSignup)
	s.mux.HandleFunc("POST /api/login", s.handleLogin)
	s.mux.HandleFunc("POST /api/logout", s.handleLogout)
	s.mux.HandleFunc("GET /api/me", s.RequireAuth(s.handleMe))

	// Login page (public)
	s.mux.HandleFunc("GET /login", s.handleLoginPage)

	// API (protected)
	s.mux.HandleFunc("GET /api/events", s.RequireAuth(s.broker.ServeHTTP))
	s.mux.HandleFunc("GET /api/stats", s.RequireAuth(s.handleStats))

	s.mux.HandleFunc("GET /api/logs", s.RequireAuth(s.handleListLogs))
	s.mux.HandleFunc("POST /api/logs", s.RequireAuth(s.handleCreateLog))
	s.mux.HandleFunc("GET /api/logs/{id}", s.RequireAuth(s.handleGetLog))
	s.mux.HandleFunc("PUT /api/logs/{id}", s.RequireAuth(s.handleUpdateLog))
	s.mux.HandleFunc("DELETE /api/logs/{id}", s.RequireAuth(s.handleDeleteLog))
	s.mux.HandleFunc("POST /api/logs/{id}/pin", s.RequireAuth(s.handleTogglePin))
	s.mux.HandleFunc("POST /api/logs/{id}/attachments", s.RequireAuth(s.handleUploadAttachment))
	s.mux.HandleFunc("DELETE /api/logs/{id}/attachments", s.RequireAuth(s.handleDeleteAttachment))

	s.mux.HandleFunc("GET /api/categories", s.RequireAuth(s.handleListCategories))
	s.mux.HandleFunc("POST /api/categories", s.RequireAuth(s.handleCreateCategory))
	s.mux.HandleFunc("PUT /api/categories/{id}", s.RequireAuth(s.handleUpdateCategory))
	s.mux.HandleFunc("DELETE /api/categories/{id}", s.RequireAuth(s.handleDeleteCategory))

	s.mux.HandleFunc("GET /api/export", s.RequireAuth(s.handleExport))
	s.mux.HandleFunc("POST /api/import", s.RequireAuth(s.handleImport))

	// Uploaded files — served only with Content-Disposition to avoid execution.
	// Protected: only authenticated users can download attachments.
	s.mux.Handle("GET /uploads/", s.requireAuthHandler(http.StripPrefix("/uploads/",
		http.HandlerFunc(s.serveUpload))))

	// Static / UI
	s.mux.Handle("GET /static/", http.FileServerFS(s.webFS))
	s.mux.HandleFunc("GET /", s.RequireAuth(s.handleIndex))

	// Health
	s.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"status":      "ok",
			"clients":     s.broker.ClientCount(),
			"server_time": time.Now().UTC().Format(time.RFC3339),
		})
	})
}

// handleLoginPage serves the login/signup page.
func (s *Server) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	// If already logged in, redirect to home.
	if cookie, err := r.Cookie(sessionKey); err == nil {
		if _, ok := s.sessions.Get(cookie.Value); ok {
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
	}
	http.ServeFileFS(w, r, s.webFS, "templates/login.html")
}

// requireAuthHandler is like RequireAuth but accepts an http.Handler.
func (s *Server) requireAuthHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionKey)
		if err != nil {
			if r.Header.Get("Accept") == "application/json" || strings.HasPrefix(r.URL.Path, "/api/") {
				writeError(w, http.StatusUnauthorized, "unauthorized")
			} else {
				http.Redirect(w, r, "/login", http.StatusSeeOther)
			}
			return
		}
		userID, ok := s.sessions.Get(cookie.Value)
		if !ok {
			http.SetCookie(w, &http.Cookie{
				Name:     sessionKey,
				Value:    "",
				Path:     "/",
				MaxAge:   -1,
				HttpOnly: true,
			})
			if r.Header.Get("Accept") == "application/json" || strings.HasPrefix(r.URL.Path, "/api/") {
				writeError(w, http.StatusUnauthorized, "unauthorized")
			} else {
				http.Redirect(w, r, "/login", http.StatusSeeOther)
			}
			return
		}
		r.Header.Set("X-User-ID", strconv.FormatInt(userID, 10))
		next.ServeHTTP(w, r)
	})
}

// handleIndex serves the SPA entry point.
func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	http.ServeFileFS(w, r, s.webFS, "templates/index.html")
}

// serveUpload streams an uploaded file with a safe Content-Disposition so
// browsers download rather than execute/render it inline.
func (s *Server) serveUpload(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/")
	if name == "" || strings.Contains(name, "/") || strings.Contains(name, "\\") ||
		strings.HasPrefix(name, ".") {
		http.NotFound(w, r)
		return
	}
	full := filepath.Join(s.uploadDir, filepath.Base(name))
	abs, err := filepath.Abs(full)
	baseAbs := mustAbs(s.uploadDir)
	if err != nil || !strings.HasPrefix(abs, baseAbs) {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename=%q`, filepath.Base(name)))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeFile(w, r, full)
}

func mustAbs(p string) string {
	a, err := filepath.Abs(p)
	if err != nil {
		return p + string(filepath.Separator)
	}
	return a + string(filepath.Separator)
}

// ---- helpers ----

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	// Drain and close body to allow connection reuse (keep-alive).
	_ = r.Body.Close()
	return nil
}

func paramID(r *http.Request, key string) (int64, error) {
	s := r.PathValue(key)
	if s == "" {
		return 0, fmt.Errorf("missing %s", key)
	}
	id, err := strconv.ParseInt(s, 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("invalid %s", key)
	}
	return id, nil
}

// getDB returns the appropriate database based on the session's DB type.
func (s *Server) getDB(r *http.Request) *database.DB {
	cookie, err := r.Cookie(sessionKey)
	if err != nil {
		return s.db
	}
	session, ok := s.sessions.GetSession(cookie.Value)
	if !ok {
		return s.db
	}
	if session.DBType == "turso" {
		return s.dbTurso
	}
	return s.db
}
