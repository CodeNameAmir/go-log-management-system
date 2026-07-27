package api

import (
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strings"

	"log-server/internal/database"
	"log-server/internal/models"
)

var colorHexRe = regexp.MustCompile(`^#([0-9a-fA-F]{3}|[0-9a-fA-F]{6})$`)

type categoryReq struct {
	Name  string `json:"name"`
	Color string `json:"color"`
}

func (s *Server) handleListCategories(w http.ResponseWriter, r *http.Request) {
	cats, err := s.getDB(r).ListCategories(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "list failed")
		return
	}
	if cats == nil {
		cats = []models.Category{}
	}
	writeJSON(w, http.StatusOK, cats)
}

func (s *Server) handleCreateCategory(w http.ResponseWriter, r *http.Request) {
	var req categoryReq
	if err := readJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.Color = strings.TrimSpace(req.Color)
	if req.Name == "" || len(req.Name) > 64 {
		writeError(w, http.StatusBadRequest, "name required (max 64 chars)")
		return
	}
	if !colorHexRe.MatchString(req.Color) {
		req.Color = "#3b82f6"
	}
	id, err := s.getDB(r).CreateCategory(r.Context(), req.Name, req.Color)
	if err != nil {
		if errors.Is(err, database.ErrDuplicate) {
			writeError(w, http.StatusConflict, "category already exists")
			return
		}
		slog.Error("create category", "err", err)
		writeError(w, http.StatusInternalServerError, "create failed")
		return
	}
	cat, _ := s.getDB(r).GetCategory(r.Context(), id)
	s.broker.Publish("category.created", cat)
	writeJSON(w, http.StatusCreated, cat)
}

func (s *Server) handleUpdateCategory(w http.ResponseWriter, r *http.Request) {
	id, err := paramID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var req categoryReq
	if err := readJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.Color = strings.TrimSpace(req.Color)
	if req.Name == "" || len(req.Name) > 64 {
		writeError(w, http.StatusBadRequest, "name required (max 64 chars)")
		return
	}
	if !colorHexRe.MatchString(req.Color) {
		req.Color = "#3b82f6"
	}
	if err := s.getDB(r).UpdateCategory(r.Context(), id, req.Name, req.Color); err != nil {
		if errors.Is(err, database.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		if errors.Is(err, database.ErrDuplicate) {
			writeError(w, http.StatusConflict, "category already exists")
			return
		}
		slog.Error("update category", "err", err)
		writeError(w, http.StatusInternalServerError, "update failed")
		return
	}
	cat, _ := s.getDB(r).GetCategory(r.Context(), id)
	s.broker.Publish("category.updated", cat)
	writeJSON(w, http.StatusOK, cat)
}

func (s *Server) handleDeleteCategory(w http.ResponseWriter, r *http.Request) {
	id, err := paramID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.getDB(r).DeleteCategory(r.Context(), id); err != nil {
		if errors.Is(err, database.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		if errors.Is(err, database.ErrInUse) {
			writeError(w, http.StatusConflict, "category has logs; reassign or delete logs first")
			return
		}
		slog.Error("delete category", "err", err)
		writeError(w, http.StatusInternalServerError, "delete failed")
		return
	}
	s.broker.Publish("category.deleted", map[string]any{"id": id})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
