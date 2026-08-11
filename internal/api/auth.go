package api

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"log-server/internal/database"
	"log-server/internal/supabase"

	"golang.org/x/crypto/bcrypt"
)

type Session struct {
	UserID    string
	DBType    string // "local" or "turso" or "supabase"
	JWT       string // JWT token for Supabase authentication
	ExpiresAt time.Time
}

type SessionManager struct {
	mu       sync.RWMutex
	sessions map[string]Session
}

func NewSessionManager() *SessionManager {
	return &SessionManager{
		sessions: make(map[string]Session),
	}
}

func (sm *SessionManager) Create(userID string, dbType string, duration time.Duration) string {
	return sm.CreateWithJWT(userID, dbType, "", duration)
}

func (sm *SessionManager) CreateWithJWT(userID string, dbType, jwt string, duration time.Duration) string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	token := hex.EncodeToString(b)

	if dbType == "" {
		dbType = "local"
	}

	sm.mu.Lock()
	sm.sessions[token] = Session{
		UserID:    userID,
		DBType:    dbType,
		JWT:       jwt,
		ExpiresAt: time.Now().Add(duration),
	}
	sm.mu.Unlock()

	return token
}

func (sm *SessionManager) Get(token string) (string, bool) {
	sm.mu.RLock()
	sess, ok := sm.sessions[token]
	sm.mu.RUnlock()

	if !ok {
		return "", false
	}

	if time.Now().After(sess.ExpiresAt) {
		sm.mu.Lock()
		delete(sm.sessions, token)
		sm.mu.Unlock()
		return "", false
	}

	return sess.UserID, true
}

func (sm *SessionManager) GetSession(token string) (Session, bool) {
	sm.mu.RLock()
	sess, ok := sm.sessions[token]
	sm.mu.RUnlock()

	if !ok {
		return Session{}, false
	}

	if time.Now().After(sess.ExpiresAt) {
		sm.mu.Lock()
		delete(sm.sessions, token)
		sm.mu.Unlock()
		return Session{}, false
	}

	return sess, true
}

func (sm *SessionManager) Delete(token string) {
	sm.mu.Lock()
	delete(sm.sessions, token)
	sm.mu.Unlock()
}

// Global session manager (initialized on server construct)
var sessionKey = "session_token"

// RequireAuth middleware extracts user session and places userID in context.
func (s *Server) RequireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionKey)
		if err != nil {
			if r.Header.Get("Accept") == "application/json" || strings.HasPrefix(r.URL.Path, "/api/") {
				writeError(w, http.StatusUnauthorized, "unauthorized")
			} else {
				http.Redirect(w, r, "/login", http.StatusSeeOther)
			}
			return
		}

		sess, ok := s.sessions.GetSession(cookie.Value)
		if !ok {
			// Clear invalid cookie
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

		// For Supabase users, userID will be empty string or UUID
		// We'll handle them specially in handlers that need user info
		r.Header.Set("X-User-ID", sess.UserID)
		next(w, r)
	}
}

// getUserID helper retrieves the authenticated userID from request headers.
func getUserID(r *http.Request) string {
	return r.Header.Get("X-User-ID")
}

func (s *Server) handleSignup(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username     string `json:"username"`
		Password     string `json:"password"`
		DBType       string `json:"db_type"`
		SupabaseURL  string `json:"supabase_url"`
		SupabaseKey  string `json:"supabase_key"`
	}
	if err := readJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}

	username := strings.TrimSpace(req.Username)
	password := req.Password

	if username == "" || len(username) < 3 {
		writeError(w, http.StatusBadRequest, "username must be at least 3 characters")
		return
	}
	if len(password) < 6 {
		writeError(w, http.StatusBadRequest, "password must be at least 6 characters")
		return
	}

	dbType := strings.TrimSpace(req.DBType)
	if dbType != "turso" && dbType != "supabase" {
		dbType = "local"
	}

	// Supabase path: sign up through GoTrue API
	if dbType == "supabase" {
		// Use provided credentials or fall back to server's client
		var sc *supabase.Client
		if req.SupabaseURL != "" && req.SupabaseKey != "" {
			var err error
			sc, err = supabase.New(req.SupabaseURL, req.SupabaseKey)
			if err != nil {
				writeError(w, http.StatusBadRequest, "invalid supabase credentials")
				return
			}
		} else {
			sc = s.sb
		}
		
		if sc == nil {
			writeError(w, http.StatusServiceUnavailable, "supabase not configured")
			return
		}
		email := username + "@example.com"
		_, _, err := sc.Signup(r.Context(), email, password)
		if err != nil {
			slog.Error("supabase signup failed", "email", email, "error", err)
			// Return detailed error message to help debug
			writeError(w, http.StatusConflict, "signup failed: "+err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{
			"username": username,
		})
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "encryption failed")
		return
	}

	// Signup always uses local DB for user creation
	// Both local and Turso will share the same user table through the DB
	id, err := s.db.CreateUser(r.Context(), username, string(hash))
	if err != nil {
		if errors.Is(err, database.ErrDuplicate) {
			writeError(w, http.StatusConflict, "username already exists")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to create user")
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{
		"id":       id,
		"username": username,
	})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username     string `json:"username"`
		Password     string `json:"password"`
		DBType       string `json:"db_type"`
		TursoURL     string `json:"turso_url"`
		TursoToken   string `json:"turso_token"`
		SupabaseURL  string `json:"supabase_url"`
		SupabaseKey  string `json:"supabase_key"`
	}
	if err := readJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}

	username := strings.TrimSpace(req.Username)
	password := req.Password
	dbType := strings.TrimSpace(req.DBType)
	if dbType != "turso" && dbType != "supabase" {
		dbType = "local"
	}

	// Supabase path: authenticate through GoTrue
	if dbType == "supabase" {
		// Use provided credentials or fall back to server's client
		var sc *supabase.Client
		if req.SupabaseURL != "" && req.SupabaseKey != "" {
			var err error
			sc, err = supabase.New(req.SupabaseURL, req.SupabaseKey)
			if err != nil {
				writeError(w, http.StatusBadRequest, "invalid supabase credentials")
				return
			}
		} else {
			sc = s.sb
		}
		
		if sc == nil {
			writeError(w, http.StatusServiceUnavailable, "supabase not configured")
			return
		}
		email := username + "@example.com"
		jwt, userID, err := sc.Login(r.Context(), email, password)
		if err != nil {
			slog.Error("supabase login", "err", err)
			writeError(w, http.StatusUnauthorized, "invalid credentials")
			return
		}
		// Store JWT in session so we can use it for authenticated requests
		sessionToken := s.sessions.CreateWithJWT("", dbType, jwt, 24*time.Hour)
		log.Printf("Session for supabase user %s (auth_id: %s)", username, userID)
		http.SetCookie(w, &http.Cookie{
			Name:     sessionKey,
			Value:    sessionToken,
			Path:     "/",
			Expires:  time.Now().Add(24 * time.Hour),
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
		})
		writeJSON(w, http.StatusOK, map[string]any{
			"username": username,
			"db_type":  dbType,
		})
		return
	}

	u, err := s.db.GetUserByUsername(r.Context(), username)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			writeError(w, http.StatusUnauthorized, "invalid username or password")
			return
		}
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)); err != nil {
		writeError(w, http.StatusUnauthorized, "invalid username or password")
		return
	}

	// If Turso is selected, ensure user exists in Turso DB too.
	tursoUserID := u.ID
	if dbType == "turso" && s.dbTurso != nil {
		tursoUser, err := s.dbTurso.GetUserByUsername(r.Context(), u.Username)
		if errors.Is(err, database.ErrNotFound) {
			newID, createErr := s.dbTurso.CreateUser(r.Context(), u.Username, u.PasswordHash)
			if createErr == nil {
				tursoUserID = newID
				log.Printf("User synced to Turso: %s (local ID: %d, turso ID: %d)", u.Username, u.ID, tursoUserID)
			} else {
				log.Printf("Warning: failed to sync user to Turso: %v", createErr)
			}
		} else if err == nil {
			tursoUserID = tursoUser.ID
			log.Printf("User found in Turso: %s (local ID: %d, turso ID: %d)", u.Username, u.ID, tursoUserID)
		}
	}

	token := s.sessions.Create(fmt.Sprintf("%d", tursoUserID), dbType, 24*time.Hour)
	log.Printf("Session created for user_id=%d (db=%s)", tursoUserID, dbType)
	http.SetCookie(w, &http.Cookie{
		Name:     sessionKey,
		Value:    token,
		Path:     "/",
		Expires:  time.Now().Add(24 * time.Hour),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})

	log.Printf("User logged in: %s (ID: %d, DB: %s)", u.Username, u.ID, dbType)

	writeJSON(w, http.StatusOK, map[string]any{
		"id":       u.ID,
		"username": u.Username,
		"db_type":  dbType,
	})
}

func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := readJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if len(req.NewPassword) < 6 {
		writeError(w, http.StatusBadRequest, "password must be at least 6 characters")
		return
	}

	cookie, err := r.Cookie(sessionKey)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	sess, ok := s.sessions.GetSession(cookie.Value)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	dbType := sess.DBType
	if dbType == "" {
		dbType = "local"
	}

	// Supabase path: change password through GoTrue
	if dbType == "supabase" {
		if s.sb == nil {
			writeError(w, http.StatusServiceUnavailable, "supabase not configured")
			return
		}
		sc := s.sb.Clone(sess.JWT)
		if err := sc.UpdateUserPassword(r.Context(), req.NewPassword); err != nil {
			slog.Error("supabase change password", "err", err)
			writeError(w, http.StatusInternalServerError, "failed to update password")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}

	// Local/Turso path
	userID := sess.UserID
	if userID == "" {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	userIDInt, err := strconv.ParseInt(userID, 10, 64)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "invalid user id")
		return
	}

	db := s.getDB(r)
	u, err := db.GetUserByID(r.Context(), userIDInt)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(req.CurrentPassword)); err != nil {
		writeError(w, http.StatusUnauthorized, "current password is incorrect")
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "encryption failed")
		return
	}

	if err := db.UpdatePassword(r.Context(), userIDInt, string(hash)); err != nil {
		slog.Error("failed to update password", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to update password")
		return
	}

	// Keep the password hash in sync across local and Turso DBs.
	for _, syncDB := range []database.Storer{s.db, s.dbTurso} {
		if syncDB == nil || syncDB == db {
			continue
		}
		if su, err := syncDB.GetUserByUsername(r.Context(), u.Username); err == nil {
			_ = syncDB.UpdatePassword(r.Context(), su.ID, string(hash))
		}
	}

	log.Printf("Password changed for user: %s (ID: %d, DB: %s)", u.Username, u.ID, dbType)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(sessionKey)
	if err == nil {
		s.sessions.Delete(cookie.Value)
	}

	http.SetCookie(w, &http.Cookie{
		Name:     sessionKey,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
	})

	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	// Get session info
	cookie, err := r.Cookie(sessionKey)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	sess, ok := s.sessions.GetSession(cookie.Value)
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	dbType := sess.DBType
	if dbType == "" {
		dbType = "local"
	}

	// Handle supabase case differently since users come from GoTrue not our DB
	if dbType == "supabase" {
		if s.sb != nil {
			// Clone client with session JWT to avoid race conditions on shared client
			sc := s.sb.Clone(sess.JWT)
			authID, email, err := sc.GetAuthUser(r.Context())
			if err != nil {
				slog.Error("failed to get supabase user", "err", err)
				writeError(w, http.StatusInternalServerError, "failed to get user")
				return
			}
			// Remove @example.com suffix to show original username
			username := strings.TrimSuffix(email, "@example.com")
			writeJSON(w, http.StatusOK, map[string]any{
				"id":       authID,
				"username": username,
				"db_type":  dbType,
			})
			return
		}
		writeError(w, http.StatusServiceUnavailable, "supabase not configured")
		return
	}

	// For local/turso users
	userID := sess.UserID
	if userID == "" {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	// Convert string userID to int64 for local/turso databases
	userIDInt, err := strconv.ParseInt(userID, 10, 64)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "invalid user id")
		return
	}

	db := s.getDB(r)
	u, err := db.GetUserByID(r.Context(), userIDInt)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"id":       u.ID,
		"username": u.Username,
		"db_type":  dbType,
	})
}
