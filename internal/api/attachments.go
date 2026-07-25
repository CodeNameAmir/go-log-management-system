package api

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"log-server/internal/database"
)

func (s *Server) handleUploadAttachment(w http.ResponseWriter, r *http.Request) {
	id, err := paramID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Verify log exists.
	if _, err := s.getDB(r).GetLog(r.Context(), id); err != nil {
		if errors.Is(err, database.ErrNotFound) {
			writeError(w, http.StatusNotFound, "log not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}

	// Limit request size - removed limit for unlimited uploads.
	r.Body = http.MaxBytesReader(w, r.Body, 1<<63-1)
	if err := r.ParseMultipartForm(1 << 30); err != nil {
		writeError(w, http.StatusBadRequest, "file too large or invalid upload")
		return
	}
	file, hdr, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "file field required")
		return
	}
	defer file.Close()

	// Read a small prefix to sniff magic bytes for extra safety.
	magic := make([]byte, 512)
	n, _ := file.Read(magic)
	detected := http.DetectContentType(magic[:n])

	// Reject anything that looks like executable HTML/script or a binary executable.
	if strings.HasPrefix(detected, "text/html") ||
		strings.HasPrefix(detected, "application/x-msdownload") ||
		strings.HasPrefix(detected, "application/x-executable") ||
		strings.HasPrefix(detected, "application/x-sh") ||
		strings.HasPrefix(detected, "application/x-msi") {
		writeError(w, http.StatusBadRequest, "executable content not allowed")
		return
	}

	// Sanitize the original filename to prevent path traversal.
	safeName := database.SafeFileName(hdr.Filename)
	if safeName == "" || safeName == "." {
		writeError(w, http.StatusBadRequest, "invalid filename")
		return
	}

	// Handle collisions: if a file with the same name already exists,
	// append a counter suffix before the extension.
	dstPath := filepath.Join(s.uploadDir, safeName)
	base := safeName[:len(safeName)-len(filepath.Ext(safeName))]
	ext2 := filepath.Ext(safeName)
	counter := 1
	for {
		if _, statErr := os.Stat(dstPath); statErr != nil {
			break // file does not exist, we can use this name
		}
		safeName = fmt.Sprintf("%s_%d%s", base, counter, ext2)
		dstPath = filepath.Join(s.uploadDir, safeName)
		counter++
	}

	dst, err := os.OpenFile(dstPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		slog.Error("create upload file", "err", err)
		writeError(w, http.StatusInternalServerError, "storage error")
		return
	}
	defer dst.Close()

	// Write sniffed prefix first, then the rest of the stream.
	if _, err := dst.Write(magic[:n]); err != nil {
		_ = os.Remove(dstPath)
		writeError(w, http.StatusInternalServerError, "write failed")
		return
	}
	written, err := io.Copy(dst, file)
	if err != nil {
		_ = os.Remove(dstPath)
		if err.Error() == "http: request body too large" {
			writeError(w, http.StatusRequestEntityTooLarge, "file too large")
			return
		}
		writeError(w, http.StatusInternalServerError, "write failed")
		return
	}
	totalSize := int64(n) + written

	// Remove any previous attachment on this log.
	oldPath, err := s.getDB(r).ClearAttachment(r.Context(), id)
	if err != nil && !errors.Is(err, database.ErrNotFound) {
		slog.Warn("clear old attachment", "err", err)
	}
	if oldPath != "" {
		_ = tryRemoveFile(oldPath)
	}

	if err := s.getDB(r).SetAttachment(r.Context(), id, hdr.Filename, safeName, totalSize); err != nil {
		_ = os.Remove(dstPath)
		slog.Error("set attachment", "err", err)
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}

	full, _ := s.getDB(r).GetLog(r.Context(), id)
	s.broker.Publish("log.updated", full)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":   true,
		"name": hdr.Filename,
		"size": totalSize,
		"url":  "/uploads/" + safeName,
		"log":  full,
	})
}

func (s *Server) handleDeleteAttachment(w http.ResponseWriter, r *http.Request) {
	id, err := paramID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	oldPath, err := s.getDB(r).ClearAttachment(r.Context(), id)
	if err != nil {
		if errors.Is(err, database.ErrNotFound) {
			writeError(w, http.StatusNotFound, "log not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "db error")
		return
	}
	if oldPath != "" {
		_ = tryRemoveFile(oldPath)
	}
	full, _ := s.getDB(r).GetLog(r.Context(), id)
	s.broker.Publish("log.updated", full)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "log": full})
}

func tryRemoveFile(path string) error {
	if path == "" {
		return nil
	}
	// Resolve to upload dir only.
	abs, err := filepath.Abs(filepath.Join("uploads", filepath.Base(path)))
	if err != nil {
		return err
	}
	base, _ := filepath.Abs("uploads")
	if !strings.HasPrefix(abs, base) {
		return errors.New("path escape")
	}
	return os.Remove(abs)
}
