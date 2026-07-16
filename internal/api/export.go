package api

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"log-server/internal/database"
	"log-server/internal/models"
)

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	userID := getUserID(r)
	st, err := s.db.Stats(r.Context(), userID)
	if err != nil {
		slog.Error("stats", "err", err)
		writeError(w, http.StatusInternalServerError, "stats failed")
		return
	}
	if st.ByCategory == nil {
		st.ByCategory = []models.CategoryCount{}
	}
	if st.ByDay == nil {
		st.ByDay = []models.DayCount{}
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	format := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("format")))
	if format != "csv" && format != "json" {
		writeError(w, http.StatusBadRequest, "format must be csv or json")
		return
	}

	userID := getUserID(r)
	f := models.Filter{
		UserID: userID,
		Search: strings.TrimSpace(r.URL.Query().Get("q")),
		Level:  strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("level"))),
		Limit:  50000,
	}
	if cid := r.URL.Query().Get("category_id"); cid != "" {
		if v, err := strconv.ParseInt(cid, 10, 64); err == nil {
			f.CategoryID = v
		}
	}

	logs, _, err := s.db.ListLogs(r.Context(), f)
	if err != nil {
		slog.Error("export list", "err", err)
		writeError(w, http.StatusInternalServerError, "export failed")
		return
	}
	if logs == nil {
		logs = []models.Log{}
	}

	ts := time.Now().UTC().Format("2006-01-02_15-04-05")

	switch format {
	case "json":
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="logs_`+ts+`.json"`)
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		_ = enc.Encode(logs)

	case "csv":
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="logs_`+ts+`.csv"`)
		cw := csv.NewWriter(w)
		_ = cw.Write([]string{
			"id", "level", "category", "pinned", "message",
			"attachment", "attachment_size", "created_at", "updated_at",
		})
		for _, l := range logs {
			att := ""
			if l.AttachmentName != nil {
				att = *l.AttachmentName
			}
			attSize := ""
			if l.AttachmentSize != nil {
				attSize = strconv.FormatInt(*l.AttachmentSize, 10)
			}
			pinned := "false"
			if l.Pinned {
				pinned = "true"
			}
			_ = cw.Write([]string{
				strconv.FormatInt(l.ID, 10),
				l.Level,
				l.CategoryName,
				pinned,
				l.Message,
				att,
				attSize,
				l.CreatedAt.Format(time.RFC3339),
				l.UpdatedAt.Format(time.RFC3339),
			})
		}
		cw.Flush()
	}

	slog.Info("export completed", "format", format, "count", len(logs))
}

type importResult struct {
	Imported int64    `json:"imported"`
	Skipped  int64    `json:"skipped"`
	Failed   int64    `json:"failed"`
	Errors   []string `json:"errors,omitempty"`
}

func (s *Server) handleImport(w http.ResponseWriter, r *http.Request) {
	format := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("format")))
	if format != "csv" && format != "json" {
		writeError(w, http.StatusBadRequest, "format must be csv or json")
		return
	}

	body, fileName, err := readImportBody(w, r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(body) == 0 {
		writeError(w, http.StatusBadRequest, "uploaded file is empty")
		return
	}
	if fileName != "" {
		ext := strings.ToLower(filepath.Ext(fileName))
		if format == "csv" && ext != "" && ext != ".csv" {
			writeError(w, http.StatusBadRequest, "selected format does not match uploaded file")
			return
		}
		if format == "json" && ext != "" && ext != ".json" {
			writeError(w, http.StatusBadRequest, "selected format does not match uploaded file")
			return
		}
	}

	var parsed []models.Log
	switch format {
	case "json":
		parsed, err = parseJSONImport(body)
	case "csv":
		parsed, err = parseCSV(body)
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if parsed == nil {
		parsed = []models.Log{}
	}

	cats, err := s.db.ListCategories(r.Context())
	if err != nil {
		slog.Error("import list categories", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to load categories")
		return
	}
	catByName := make(map[string]models.Category, len(cats))
	for _, c := range cats {
		catByName[strings.ToLower(strings.TrimSpace(c.Name))] = c
	}

	existing, _, err := s.db.ListLogs(r.Context(), models.Filter{Limit: 50000})
	if err != nil {
		slog.Error("import list existing logs", "err", err)
		writeError(w, http.StatusInternalServerError, "failed to load existing logs")
		return
	}
	seen := make(map[string]struct{}, len(existing)+len(parsed))
	for _, l := range existing {
		seen[logImportSignature(l.Level, l.CategoryID, l.Message, l.CreatedAt, l.UpdatedAt)] = struct{}{}
	}

	result := importResult{}
	for i, row := range parsed {
		line := i + 1
		row.Message = strings.TrimSpace(row.Message)
		row.Level = strings.ToUpper(strings.TrimSpace(row.Level))
		row.CategoryName = strings.TrimSpace(row.CategoryName)
		row.CategoryColor = strings.TrimSpace(row.CategoryColor)

		if row.Message == "" {
			result.Skipped++
			appendImportError(&result, fmt.Sprintf("row %d skipped: empty message", line))
			continue
		}
		if !models.IsValidLevel(row.Level) {
			result.Skipped++
			appendImportError(&result, fmt.Sprintf("row %d skipped: invalid level %q", line, row.Level))
			continue
		}

		catID, err := s.resolveImportCategory(r, catByName, row.CategoryID, row.CategoryName, row.CategoryColor)
		if err != nil {
			result.Failed++
			appendImportError(&result, fmt.Sprintf("row %d failed: %v", line, err))
			continue
		}

		sig := logImportSignature(row.Level, catID, row.Message, row.CreatedAt, row.UpdatedAt)
		if _, ok := seen[sig]; ok {
			result.Skipped++
			continue
		}

		if _, err := s.db.CreateLog(r.Context(), &models.Log{
			Message:    row.Message,
			Level:      row.Level,
			CategoryID: catID,
			Pinned:     row.Pinned,
		}); err != nil {
			result.Failed++
			appendImportError(&result, fmt.Sprintf("row %d failed: %v", line, err))
			continue
		}

		seen[sig] = struct{}{}
		result.Imported++
	}

	if result.Imported > 0 {
		s.broker.Publish("import.completed", result)
	}
	slog.Info("import completed", "format", format, "imported", result.Imported, "skipped", result.Skipped, "failed", result.Failed)
	writeJSON(w, http.StatusOK, result)
}

func readImportBody(w http.ResponseWriter, r *http.Request) ([]byte, string, error) {
	r.Body = http.MaxBytesReader(w, r.Body, 50<<20)
	ct := r.Header.Get("Content-Type")
	mediaType, _, _ := mime.ParseMediaType(ct)
	if strings.HasPrefix(mediaType, "multipart/") {
		if err := r.ParseMultipartForm(50 << 20); err != nil {
			return nil, "", fmt.Errorf("invalid upload: %w", err)
		}
		file, hdr, err := r.FormFile("file")
		if err != nil {
			return nil, "", errors.New("file field is required")
		}
		defer file.Close()
		b, err := io.ReadAll(file)
		if err != nil {
			return nil, "", fmt.Errorf("failed to read uploaded file: %w", err)
		}
		return b, hdr.Filename, nil
	}
	b, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, "", fmt.Errorf("failed to read request body: %w", err)
	}
	return b, "", nil
}

func parseJSONImport(data []byte) ([]models.Log, error) {
	var arr []models.Log
	if err := json.Unmarshal(data, &arr); err == nil {
		return arr, nil
	}
	var wrapped struct {
		Logs []models.Log `json:"logs"`
	}
	if err := json.Unmarshal(data, &wrapped); err == nil && wrapped.Logs != nil {
		return wrapped.Logs, nil
	}
	return nil, errors.New("invalid JSON import: expected an array of logs or an object with a logs field")
}

func parseCSV(data []byte) ([]models.Log, error) {
	reader := csv.NewReader(strings.NewReader(string(data)))
	reader.FieldsPerRecord = -1
	reader.TrimLeadingSpace = true

	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("invalid CSV: %w", err)
	}
	if len(records) == 0 {
		return []models.Log{}, nil
	}

	header := records[0]
	hasHeader := detectCSVHeader(header)
	start := 0
	if hasHeader {
		start = 1
	}

	capCount := len(records) - start
	if capCount < 0 {
		capCount = 0
	}
	logs := make([]models.Log, 0, capCount)
	for i := start; i < len(records); i++ {
		row := records[i]
		if isBlankCSVRow(row) {
			continue
		}
		var l models.Log
		if hasHeader {
			for j, val := range row {
				if j >= len(header) {
					continue
				}
				assignCSVField(&l, strings.ToLower(strings.TrimSpace(header[j])), val)
			}
		} else {
			for j, val := range row {
				switch j {
				case 0:
					if id, err := strconv.ParseInt(strings.TrimSpace(val), 10, 64); err == nil {
						l.ID = id
					}
				case 1:
					l.Level = strings.ToUpper(strings.TrimSpace(val))
				case 2:
					l.CategoryName = strings.TrimSpace(val)
				case 3:
					l.Pinned = parseBool(val)
				case 4:
					l.Message = val
				}
			}
		}
		logs = append(logs, l)
	}
	return logs, nil
}

func detectCSVHeader(row []string) bool {
	known := map[string]struct{}{
		"id": {}, "level": {}, "category": {}, "category_name": {}, "category_id": {},
		"category_color": {}, "pinned": {}, "message": {}, "attachment": {},
		"attachment_name": {}, "attachment_size": {}, "created_at": {}, "updated_at": {},
	}
	matches := 0
	for _, col := range row {
		if _, ok := known[strings.ToLower(strings.TrimSpace(col))]; ok {
			matches++
		}
	}
	return matches > 0
}

func isBlankCSVRow(row []string) bool {
	for _, v := range row {
		if strings.TrimSpace(v) != "" {
			return false
		}
	}
	return true
}

func assignCSVField(l *models.Log, col, val string) {
	val = strings.TrimSpace(val)
	switch col {
	case "id":
		if id, err := strconv.ParseInt(val, 10, 64); err == nil {
			l.ID = id
		}
	case "level":
		l.Level = strings.ToUpper(val)
	case "category", "category_name":
		l.CategoryName = val
	case "category_id":
		if id, err := strconv.ParseInt(val, 10, 64); err == nil {
			l.CategoryID = id
		}
	case "category_color":
		l.CategoryColor = val
	case "pinned":
		l.Pinned = parseBool(val)
	case "message":
		l.Message = val
	case "attachment", "attachment_name":
		if val != "" {
			v := val
			l.AttachmentName = &v
		}
	case "attachment_size":
		if size, err := strconv.ParseInt(val, 10, 64); err == nil {
			l.AttachmentSize = &size
		}
	case "created_at":
		l.CreatedAt = parseImportTime(val)
	case "updated_at":
		l.UpdatedAt = parseImportTime(val)
	}
}

func parseImportTime(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	layouts := []string{time.RFC3339, "2006-01-02 15:04:05", time.RFC3339Nano}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

func parseBool(s string) bool {
	s = strings.TrimSpace(strings.ToLower(s))
	return s == "1" || s == "true" || s == "yes" || s == "y"
}

func (s *Server) resolveImportCategory(r *http.Request, cache map[string]models.Category, categoryID int64, categoryName, categoryColor string) (int64, error) {
	if categoryID > 0 {
		if _, err := s.db.GetCategory(r.Context(), categoryID); err == nil {
			return categoryID, nil
		}
	}

	name := strings.TrimSpace(categoryName)
	if name != "" {
		if c, ok := cache[strings.ToLower(name)]; ok {
			return c.ID, nil
		}
		if categoryColor == "" {
			categoryColor = "#64748b"
		}
		newID, err := s.db.CreateCategory(r.Context(), name, categoryColor)
		if err != nil {
			if errors.Is(err, database.ErrDuplicate) {
				cats, listErr := s.db.ListCategories(r.Context())
				if listErr != nil {
					return 0, listErr
				}
				for _, c := range cats {
					cache[strings.ToLower(strings.TrimSpace(c.Name))] = c
				}
				if c, ok := cache[strings.ToLower(name)]; ok {
					return c.ID, nil
				}
				return 0, fmt.Errorf("category %q already exists but could not be reloaded", name)
			}
			return 0, err
		}
		c := models.Category{ID: newID, Name: name, Color: categoryColor}
		cache[strings.ToLower(name)] = c
		return newID, nil
	}

	for _, c := range cache {
		return c.ID, nil
	}
	return 0, errors.New("no category available")
}

func logImportSignature(level string, categoryID int64, message string, createdAt, updatedAt time.Time) string {
	t := createdAt
	if t.IsZero() {
		t = updatedAt
	}
	ts := ""
	if !t.IsZero() {
		ts = t.UTC().Format(time.RFC3339)
	}
	return strings.Join([]string{
		strings.ToUpper(strings.TrimSpace(level)),
		strconv.FormatInt(categoryID, 10),
		strings.TrimSpace(message),
		ts,
	}, "|")
}

func appendImportError(result *importResult, msg string) {
	if len(result.Errors) < 100 {
		result.Errors = append(result.Errors, msg)
	}
}
