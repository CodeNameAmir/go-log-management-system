package supabase

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"log-server/internal/database"
	"log-server/internal/models"
)

var _ database.Storer = (*Client)(nil)

const defaultTimeout = 30 * time.Second

type Client struct {
	base    string
	anonKey string
	jwt     string
	userID  string
	httpc   *http.Client
}

func New(supabaseURL, anonKey string) (*Client, error) {
	base := strings.TrimRight(supabaseURL, "/")
	if base == "" {
		return nil, errors.New("supabase: URL required")
	}
	return &Client{
		base:    base,
		anonKey: anonKey,
		httpc: &http.Client{
			Timeout: defaultTimeout,
			Transport: &http.Transport{
				MaxIdleConns:        20,
				MaxIdleConnsPerHost: 10,
				IdleConnTimeout:     90 * time.Second,
			},
		},
	}, nil
}

func (c *Client) Close() error         { return nil }
func (c *Client) SetJWT(jwt string)  { c.jwt = jwt }
func (c *Client) SetUserID(id string) { c.userID = id }

// Clone creates a shallow copy of the Client with a new JWT.
// This is safe for concurrent use since http.Client is shared.
func (c *Client) Clone(jwt string) *Client {
	return &Client{
		base:    c.base,
		anonKey: c.anonKey,
		jwt:     jwt,
		httpc:   c.httpc,
	}
}

// do sends an authenticated request to the Supabase API.
func (c *Client) do(method, url string, body interface{}, v interface{}, prefer string) error {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, r)
	if err != nil {
		return err
	}
	req.Header.Set("apikey", c.anonKey)
	if c.jwt != "" {
		req.Header.Set("Authorization", "Bearer "+c.jwt)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if prefer != "" {
		req.Header.Set("Prefer", prefer)
	}

	resp, err := c.httpc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode >= 400 {
		slog.Error("supabase api error", "method", method, "url", url, "status", resp.StatusCode)
		return fmt.Errorf("supabase %d: %s", resp.StatusCode, string(data))
	}
	if v != nil && len(data) > 0 {
		return json.Unmarshal(data, v)
	}
	return nil
}

func (c *Client) restURL(table string) string {
	return fmt.Sprintf("%s/rest/v1/%s", c.base, table)
}

// ---- Auth (GoTrue) ----

func (c *Client) Signup(ctx context.Context, email, password string) (accessToken, userID string, err error) {
	var res struct {
		AccessToken string `json:"access_token"`
		User        struct {
			ID string `json:"id"`
		} `json:"user"`
	}
	if err := c.do("POST", c.base+"/auth/v1/signup", map[string]string{"email": email, "password": password}, &res, ""); err != nil {
		return "", "", err
	}
	c.jwt = res.AccessToken
	c.userID = res.User.ID
	return res.AccessToken, res.User.ID, nil
}

func (c *Client) Login(ctx context.Context, email, password string) (accessToken, userID string, err error) {
	var res struct {
		AccessToken string `json:"access_token"`
		User        struct {
			ID string `json:"id"`
		} `json:"user"`
	}
	if err := c.do("POST", c.base+"/auth/v1/token?grant_type=password", map[string]string{"email": email, "password": password}, &res, ""); err != nil {
		return "", "", err
	}
	c.jwt = res.AccessToken
	c.userID = res.User.ID
	return res.AccessToken, res.User.ID, nil
}

func (c *Client) Logout(ctx context.Context) error {
	return c.do("POST", c.base+"/auth/v1/logout", nil, nil, "")
}

func (c *Client) GetAuthUser(ctx context.Context) (id, email string, err error) {
	var res struct {
		ID    string `json:"id"`
		Email string `json:"email"`
	}
	if err := c.do("GET", c.base+"/auth/v1/user", nil, &res, ""); err != nil {
		return "", "", err
	}
	return res.ID, res.Email, nil
}

// ---- data operations ----

func (c *Client) ListCategories(ctx context.Context) ([]models.Category, error) {
	var rows []models.Category
	err := c.do("GET", c.restURL("categories")+"?select=id,name,color,created_at&order=name.asc", nil, &rows, "")
	return rows, err
}

func (c *Client) GetCategory(ctx context.Context, id int64) (models.Category, error) {
	var rows []models.Category
	err := c.do("GET", fmt.Sprintf("%s?id=eq.%d&select=id,name,color,created_at", c.restURL("categories"), id), nil, &rows, "")
	if err != nil {
		return models.Category{}, err
	}
	if len(rows) == 0 {
		return models.Category{}, database.ErrNotFound
	}
	return rows[0], nil
}

func (c *Client) CreateCategory(ctx context.Context, name, color string) (int64, error) {
	var rows []models.Category
	err := c.do("POST", c.restURL("categories?select=*"), map[string]string{"name": name, "color": color}, &rows, "return=representation")
	if err != nil {
		if strings.Contains(err.Error(), "duplicate") {
			return 0, database.ErrDuplicate
		}
		return 0, err
	}
	if len(rows) == 0 {
		return 0, errors.New("supabase: category not returned")
	}
	return rows[0].ID, nil
}

func (c *Client) UpdateCategory(ctx context.Context, id int64, name, color string) error {
	return c.do("PATCH", fmt.Sprintf("%s?id=eq.%d", c.restURL("categories"), id), map[string]string{"name": name, "color": color}, nil, "return=minimal")
}

func (c *Client) DeleteCategory(ctx context.Context, id int64) error {
	return c.do("DELETE", fmt.Sprintf("%s?id=eq.%d", c.restURL("categories"), id), nil, nil, "")
}

func (c *Client) CreateLog(ctx context.Context, l *models.Log) (int64, error) {
	full, err := c.CreateLogAndGet(ctx, l)
	if err != nil {
		return 0, err
	}
	return full.ID, nil
}

func (c *Client) CreateLogAndGet(ctx context.Context, l *models.Log) (models.Log, error) {
	pinned := 0
	if l.Pinned {
		pinned = 1
	}
	payload := map[string]interface{}{
		"message":     l.Message,
		"level":       l.Level,
		"category_id": l.CategoryID,
		"pinned":      pinned,
	}
	var rows []models.Log
	err := c.do("POST", c.restURL("logs?select=*"), payload, &rows, "return=representation")
	if err != nil {
		return models.Log{}, err
	}
	if len(rows) == 0 {
		return models.Log{}, errors.New("supabase: log not returned")
	}
	return rows[0], nil
}

func (c *Client) UpdateLog(ctx context.Context, l *models.Log) error {
	_, err := c.UpdateLogAndGet(ctx, l)
	return err
}

func (c *Client) DeleteLog(ctx context.Context, id int64) (string, error) {
	old, err := c.GetLog(ctx, id)
	if err != nil {
		return "", err
	}
	var oldPath string
	if old.AttachmentPath != nil {
		oldPath = *old.AttachmentPath
	}
	err = c.do("DELETE", fmt.Sprintf("%s?id=eq.%d", c.restURL("logs"), id), nil, nil, "")
	return oldPath, err
}

func (c *Client) TogglePin(ctx context.Context, id int64) (bool, error) {
	var rows []models.Log
	if err := c.do("GET", fmt.Sprintf("%s?id=eq.%d&select=pinned", c.restURL("logs"), id), nil, &rows, ""); err != nil {
		return false, err
	}
	if len(rows) == 0 {
		return false, database.ErrNotFound
	}
	next := 0
	if !rows[0].Pinned {
		next = 1
	}
	err := c.do("PATCH", fmt.Sprintf("%s?id=eq.%d", c.restURL("logs"), id), map[string]int{"pinned": next}, nil, "")
	return next == 1, err
}

func (c *Client) TogglePinAndGet(ctx context.Context, id int64) (bool, models.Log, error) {
	pinned, err := c.TogglePin(ctx, id)
	if err != nil {
		return false, models.Log{}, err
	}
	log, err := c.GetLog(ctx, id)
	return pinned, log, err
}

func (c *Client) UpdateLogAndGet(ctx context.Context, l *models.Log) (models.Log, error) {
	pinned := 0
	if l.Pinned {
		pinned = 1
	}
	var attName, attPath interface{}
	attName, attPath = nil, nil
	var attSize *int64
	if l.AttachmentName != nil {
		attName = *l.AttachmentName
	}
	if l.AttachmentPath != nil {
		attPath = *l.AttachmentPath
	}
	if l.AttachmentSize != nil {
		attSize = l.AttachmentSize
	}
	payload := map[string]interface{}{
		"message":     l.Message,
		"level":       l.Level,
		"category_id": l.CategoryID,
		"pinned":      pinned,
	}
	if attName != nil {
		payload["attachment_name"] = attName
	}
	if attPath != nil {
		payload["attachment_path"] = attPath
	}
	if attSize != nil {
		payload["attachment_size"] = *attSize
	}
	err := c.do("PATCH", fmt.Sprintf("%s?id=eq.%d", c.restURL("logs"), l.ID), payload, nil, "")
	if err != nil {
		return models.Log{}, err
	}
	return c.GetLog(ctx, l.ID)
}

func (c *Client) GetLog(ctx context.Context, id int64) (models.Log, error) {
	var rows []models.Log
	err := c.do("GET", fmt.Sprintf("%s?id=eq.%d&select=*", c.restURL("logs"), id), nil, &rows, "")
	if err != nil {
		return models.Log{}, err
	}
	if len(rows) == 0 {
		return models.Log{}, database.ErrNotFound
	}
	return rows[0], nil
}

func (c *Client) ListLogs(ctx context.Context, f models.Filter) ([]models.Log, int, error) {
	// First fetch all categories to map category_id -> name/color
	categories, err := c.ListCategories(ctx)
	if err != nil {
		slog.Warn("supabase list logs: failed to fetch categories for enrichment", "err", err)
	}
	catMap := make(map[int64]models.Category)
	for _, cat := range categories {
		catMap[cat.ID] = cat
	}

	path := c.restURL("logs") + "?select=*&order=pinned.desc,id.desc"
	if f.UserID != "" {
		path += "&user_id=eq." + f.UserID
	}
	if f.Search != "" {
		path += "&message=ilike.*" + f.Search + "*"
	}
	if f.Level != "" && models.IsValidLevel(f.Level) {
		path += "&level=eq." + f.Level
	}
	if f.CategoryID > 0 {
		path += "&category_id=eq." + strconv.FormatInt(f.CategoryID, 10)
	}
	if f.Limit > 0 {
		path += "&limit=" + strconv.Itoa(f.Limit)
	}
	if f.Offset > 0 {
		path += "&offset=" + strconv.Itoa(f.Offset)
	}

	req, err := http.NewRequestWithContext(ctx, "GET", path, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("apikey", c.anonKey)
	if c.jwt != "" {
		req.Header.Set("Authorization", "Bearer "+c.jwt)
	}
	req.Header.Set("Prefer", "count=exact")

	resp, err := c.httpc.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	total := 0
	if cr := resp.Header.Get("Content-Range"); cr != "" {
		if parts := strings.Split(cr, "/"); len(parts) == 2 {
			total, _ = strconv.Atoi(strings.TrimSpace(parts[1]))
		}
	}

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return nil, 0, fmt.Errorf("supabase: list logs %d: %s", resp.StatusCode, string(body))
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, 0, err
	}

	var logs []models.Log
	if err := json.Unmarshal(bodyBytes, &logs); err != nil {
		return nil, 0, err
	}

	// Enrich logs with category name/color from cached map
	for i := range logs {
		if cat, ok := catMap[logs[i].CategoryID]; ok {
			logs[i].CategoryName = cat.Name
			logs[i].CategoryColor = cat.Color
		}
	}

	if total == 0 {
		total = len(logs)
	}

	return logs, total, nil
}

func (c *Client) SetAttachment(ctx context.Context, logID int64, name, path string, size int64) error {
	return c.do("PATCH", fmt.Sprintf("%s?id=eq.%d", c.restURL("logs"), logID), map[string]interface{}{
		"attachment_name": name,
		"attachment_path": path,
		"attachment_size": size,
	}, nil, "")
}

func (c *Client) ClearAttachment(ctx context.Context, logID int64) (string, error) {
	old, err := c.GetLog(ctx, logID)
	if err != nil {
		return "", err
	}
	var oldPath string
	if old.AttachmentPath != nil {
		oldPath = *old.AttachmentPath
	}
	payload := map[string]interface{}{
		"attachment_name": nil,
		"attachment_path": nil,
		"attachment_size": nil,
	}
	err = c.do("PATCH", fmt.Sprintf("%s?id=eq.%d", c.restURL("logs"), logID), payload, nil, "")
	return oldPath, err
}

func (c *Client) Stats(ctx context.Context, userID int64) (models.Stats, error) {
	s := models.Stats{}
	// total logs
	reqURL := fmt.Sprintf("%s?select=count&user_id=%d", c.restURL("logs"), userID)
	req, _ := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	req.Header.Set("apikey", c.anonKey)
	if c.jwt != "" {
		req.Header.Set("Authorization", "Bearer "+c.jwt)
	}
	req.Header.Set("Prefer", "count=exact")
	resp, err := c.httpc.Do(req)
	if err == nil {
		cr := resp.Header.Get("Content-Range")
		if parts := strings.Split(cr, "/"); len(parts) == 2 {
			cnt, _ := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64)
			s.TotalLogs = cnt
		}
		resp.Body.Close()
	}
	// categories
	catReq, _ := http.NewRequestWithContext(ctx, "GET", c.restURL("categories?select=count"), nil)
	catReq.Header.Set("apikey", c.anonKey)
	if c.jwt != "" {
		catReq.Header.Set("Authorization", "Bearer "+c.jwt)
	}
	catReq.Header.Set("Prefer", "count=exact")
	catResp, err := c.httpc.Do(catReq)
	if err == nil {
		if cr := catResp.Header.Get("Content-Range"); cr != "" {
			parts := strings.Split(cr, "/")
			if len(parts) == 2 {
				cnt, _ := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64)
				s.TotalCategories = cnt
			}
		}
		catResp.Body.Close()
	}
	return s, nil
}

func (c *Client) CreateUser(ctx context.Context, username, passwordHash string) (int64, error) {
	return 0, errors.New("supabase: CreateUser not applicable; use Signup")
}

func (c *Client) GetUserByUsername(ctx context.Context, username string) (models.User, error) {
	return models.User{}, errors.New("supabase: GetUserByUsername not applicable; use GoTrue")
}

func (c *Client) GetUserByID(ctx context.Context, id int64) (models.User, error) {
	return models.User{}, errors.New("supabase: GetUserByID not applicable for numeric ID")
}