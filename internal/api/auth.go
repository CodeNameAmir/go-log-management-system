package api

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"log-server/internal/database"

	"golang.org/x/crypto/bcrypt"
)

type Session struct {
	UserID    int64
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

func (sm *SessionManager) Create(userID int64, duration time.Duration) string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	token := hex.EncodeToString(b)

	sm.mu.Lock()
	sm.sessions[token] = Session{
		UserID:    userID,
		ExpiresAt: time.Now().Add(duration),
	}
	sm.mu.Unlock()

	return token
}

func (sm *SessionManager) Get(token string) (int64, bool) {
	sm.mu.RLock()
	sess, ok := sm.sessions[token]
	sm.mu.RUnlock()

	if !ok {
		return 0, false
	}

	if time.Now().After(sess.ExpiresAt) {
		sm.mu.Lock()
		delete(sm.sessions, token)
		sm.mu.Unlock()
		return 0, false
	}

	return sess.UserID, true
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

		userID, ok := s.sessions.Get(cookie.Value)
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

		// Inject User ID into request headers so handlers can easily read it
		r.Header.Set("X-User-ID", strconv.FormatInt(userID, 10))
		next(w, r)
	}
}

// getUserID helper retrieves the authenticated userID from request headers.
func getUserID(r *http.Request) int64 {
	val := r.Header.Get("X-User-ID")
	if val == "" {
		return 0
	}
	id, _ := strconv.ParseInt(val, 10, 64)
	return id
}

type authReq struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (s *Server) handleSignup(w http.ResponseWriter, r *http.Request) {
	var req authReq
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

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "encryption failed")
		return
	}

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
	var req authReq
	if err := readJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}

	username := strings.TrimSpace(req.Username)
	password := req.Password

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

	token := s.sessions.Create(u.ID, 24*time.Hour)
	http.SetCookie(w, &http.Cookie{
		Name:     sessionKey,
		Value:    token,
		Path:     "/",
		Expires:  time.Now().Add(24 * time.Hour),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})

	log.Printf("User logged in: %s (ID: %d)", u.Username, u.ID)

	writeJSON(w, http.StatusOK, map[string]any{
		"id":       u.ID,
		"username": u.Username,
	})
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
	userID := getUserID(r)
	if userID == 0 {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	u, err := s.db.GetUserByID(r.Context(), userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"id":       u.ID,
		"username": u.Username,
	})
}
