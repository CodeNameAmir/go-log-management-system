package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"log-server/internal/database"
	"log-server/internal/models"
)

// ColorEnabled controls whether terminal log output uses ANSI escape codes.
// It is set by the main package based on the detected terminal capabilities.
var ColorEnabled = true

// SetColorEnabled updates the terminal color output setting.
func SetColorEnabled(enabled bool) {
	ColorEnabled = enabled
}

// ----- Log Handlers -----

type logCreateReq struct {
	Message    string `json:"message"`
	Level      string `json:"level"`
	CategoryID int64  `json:"category_id"`
	Pinned     bool   `json:"pinned"`
}

func (s *Server) handleCreateLog(w http.ResponseWriter, r *http.Request) {
	userID := getUserID(r)
	// For Supabase, userID will be 0, which is acceptable since Supabase manages users differently
	var req logCreateReq
	if err := readJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	req.Message = strings.TrimSpace(req.Message)
	if req.Message == "" {
		writeError(w, http.StatusBadRequest, "message required")
		return
	}
	if len(req.Message) > 10000 {
		writeError(w, http.StatusBadRequest, "message too long")
		return
	}
	if !models.IsValidLevel(req.Level) {
		writeError(w, http.StatusBadRequest, "invalid level")
		return
	}
	if req.CategoryID <= 0 {
		writeError(w, http.StatusBadRequest, "category required")
		return
	}
	if _, err := s.getDB(r).GetCategory(r.Context(), req.CategoryID); err != nil {
		if errors.Is(err, database.ErrNotFound) {
			writeError(w, http.StatusBadRequest, "unknown category")
			return
		}
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}

	l := &models.Log{
		UserID:     userID,
		Message:    req.Message,
		Level:      req.Level,
		CategoryID: req.CategoryID,
		Pinned:     req.Pinned,
	}
	full, err := s.getDB(r).CreateLogAndGet(r.Context(), l)
	if err != nil {
		slog.Error("create log", "err", err)
		writeError(w, http.StatusInternalServerError, "create failed")
		return
	}
	s.broker.Publish("log.created", full)
	if full.Level == models.LevelError || full.Level == models.LevelCritical {
		s.broker.Publish("log.alert", full)
	}
	// Print formatted log entry to terminal
	printLogEntry(full.Level, full.CategoryName, full.Message)
	writeJSON(w, http.StatusCreated, full)
}

func (s *Server) handleUpdateLog(w http.ResponseWriter, r *http.Request) {
	userID := getUserID(r)
	id, err := paramID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	
	// Check if using Supabase
	cookie, _ := r.Cookie(sessionKey)
	isSupabase := false
	if cookie != nil {
		if sess, ok := s.sessions.GetSession(cookie.Value); ok {
			isSupabase = (sess.DBType == "supabase")
		}
	}
	
	var req logCreateReq
	if err := readJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	req.Message = strings.TrimSpace(req.Message)
	if req.Message == "" {
		writeError(w, http.StatusBadRequest, "message required")
		return
	}
	if !models.IsValidLevel(req.Level) {
		writeError(w, http.StatusBadRequest, "invalid level")
		return
	}
	if req.CategoryID <= 0 {
		writeError(w, http.StatusBadRequest, "category required")
		return
	}
	existing, err := s.getDB(r).GetLog(r.Context(), id)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	
	// Only check ownership for local/turso, not for Supabase
	if !isSupabase && existing.UserID != userID {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	
	upd := existing
	upd.Message = req.Message
	upd.Level = req.Level
	upd.CategoryID = req.CategoryID
	// Preserve existing attachment unless explicitly cleared elsewhere.
	full, err := s.getDB(r).UpdateLogAndGet(r.Context(), &upd)
	if err != nil {
		slog.Error("update log", "err", err)
		writeError(w, http.StatusInternalServerError, "update failed")
		return
	}
	s.broker.Publish("log.updated", full)
	writeJSON(w, http.StatusOK, full)
}

func (s *Server) handleDeleteLog(w http.ResponseWriter, r *http.Request) {
	userID := getUserID(r)
	id, err := paramID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	
	// Check if using Supabase
	cookie, _ := r.Cookie(sessionKey)
	isSupabase := false
	if cookie != nil {
		if sess, ok := s.sessions.GetSession(cookie.Value); ok {
			isSupabase = (sess.DBType == "supabase")
		}
	}
	
	existing, err := s.getDB(r).GetLog(r.Context(), id)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	
	// Only check ownership for local/turso, not for Supabase
	if !isSupabase && existing.UserID != userID {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}

	attachPath, err := s.getDB(r).DeleteLog(r.Context(), id)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		slog.Error("delete log", "err", err)
		writeError(w, http.StatusInternalServerError, "delete failed")
		return
	}
	if attachPath != "" {
		_ = tryRemoveFile(attachPath)
	}
	s.broker.Publish("log.deleted", map[string]any{"id": id})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleTogglePin(w http.ResponseWriter, r *http.Request) {
	userID := getUserID(r)
	id, err := paramID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	
	// Check if using Supabase
	cookie, _ := r.Cookie(sessionKey)
	isSupabase := false
	if cookie != nil {
		if sess, ok := s.sessions.GetSession(cookie.Value); ok {
			isSupabase = (sess.DBType == "supabase")
		}
	}
	
	existing, err := s.getDB(r).GetLog(r.Context(), id)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	
	// Only check ownership for local/turso, not for Supabase
	if !isSupabase && existing.UserID != userID {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}

	pinned, full, err := s.getDB(r).TogglePinAndGet(r.Context(), id)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "toggle failed")
		return
	}
	s.broker.Publish("log.pinned", full)
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "pinned": pinned, "log": full})
}

func (s *Server) handleGetLog(w http.ResponseWriter, r *http.Request) {
	userID := getUserID(r)
	id, err := paramID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	
	// Check if using Supabase
	cookie, _ := r.Cookie(sessionKey)
	isSupabase := false
	if cookie != nil {
		if sess, ok := s.sessions.GetSession(cookie.Value); ok {
			isSupabase = (sess.DBType == "supabase")
		}
	}
	
	l, err := s.getDB(r).GetLog(r.Context(), id)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	
	// Only check ownership for local/turso, not for Supabase
	if !isSupabase && l.UserID != userID {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	
	writeJSON(w, http.StatusOK, l)
}

func (s *Server) handleListLogs(w http.ResponseWriter, r *http.Request) {
	userID := getUserID(r)
	
	// For Supabase, don't filter by userID since all logs are shared
	// Check if using Supabase by looking at session
	cookie, _ := r.Cookie(sessionKey)
	isSupabase := false
	if cookie != nil {
		if sess, ok := s.sessions.GetSession(cookie.Value); ok {
			isSupabase = (sess.DBType == "supabase")
		}
	}
	
	f := models.Filter{
		Search: strings.TrimSpace(r.URL.Query().Get("q")),
		Level:  strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("level"))),
	}
	
	// Only filter by userID for local/turso databases
	if !isSupabase {
		f.UserID = userID
	}
	
	if cid := r.URL.Query().Get("category_id"); cid != "" {
		if v, err := strconv.ParseInt(cid, 10, 64); err == nil {
			f.CategoryID = v
		}
	}
	
	// Handle pinned filter
	if pinnedStr := r.URL.Query().Get("pinned"); pinnedStr != "" {
		if pinnedStr == "true" {
			pinnedVal := true
			f.Pinned = &pinnedVal
		} else if pinnedStr == "false" {
			pinnedVal := false
			f.Pinned = &pinnedVal
		}
	}
	
	// Handle has_file filter
	if hasFileStr := r.URL.Query().Get("has_file"); hasFileStr == "true" {
		f.HasFile = true
	}
	
	// Handle since filter
	if sinceStr := r.URL.Query().Get("since"); sinceStr != "" {
		if sinceTime, err := time.Parse(time.RFC3339, sinceStr); err == nil {
			f.Since = &sinceTime
		}
	}
	
	f.Limit = 500
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n < 2000 {
			f.Limit = n
		}
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			f.Offset = n
		}
	}

	// Add a timeout to prevent long-running queries from blocking
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	logs, total, err := s.getDB(r).ListLogs(ctx, f)
	if err != nil {
		slog.Warn("list logs", "err", err, "filter", f)
		writeError(w, http.StatusInternalServerError, "list failed")
		return
	}
	if logs == nil {
		logs = []models.Log{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"logs":  logs,
		"total": total,
		"time":  time.Now().UTC().Unix(),
	})
}

// printLogEntry prints a formatted log entry to the terminal with colors.
func printLogEntry(level, category, message string) {
	if category == "" {
		category = "General"
	}
	// Truncate message if too long
	if len(message) > 60 {
		message = message[:57] + "..."
	}
	timestamp := time.Now().Format("15:04:05")
	// Pad level for alignment
	levelPadded := fmt.Sprintf("%-8s", level)
	if !ColorEnabled {
		fmt.Printf("%s %s[%s] %s\n", timestamp, levelPadded, category, message)
		return
	}
	// Use ANSI colors for terminal output
	var colorCode string
	switch level {
	case "DEBUG":
		colorCode = "\033[90m" // Gray
	case "INFO":
		colorCode = "\033[32m" // Green
	case "WARNING":
		colorCode = "\033[33m" // Yellow
	case "ERROR":
		colorCode = "\033[31m" // Red
	case "CRITICAL":
		colorCode = "\033[91m" // Bright Red
	default:
		colorCode = "\033[0m"
	}
	reset := "\033[0m"
	fmt.Printf("%s%s %s[%s]%s %s%s\n", colorCode, timestamp, levelPadded, category, reset, message, reset)
}
