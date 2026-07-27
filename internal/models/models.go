package models

import "time"

// Valid log levels (ordered by severity).
const (
	LevelDebug    = "DEBUG"
	LevelInfo     = "INFO"
	LevelWarning  = "WARNING"
	LevelError    = "ERROR"
	LevelCritical = "CRITICAL"
)

var ValidLevels = []string{
	LevelDebug, LevelInfo, LevelWarning, LevelError, LevelCritical,
}

// IsValidLevel returns true if s is a known level.
func IsValidLevel(s string) bool {
	for _, v := range ValidLevels {
		if v == s {
			return true
		}
	}
	return false
}

// Category represents a log category.
type Category struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Color     string    `json:"color"`
	CreatedAt time.Time `json:"created_at"`
}

// User represents a registered user.
type User struct {
	ID           int64     `json:"id"`
	Username     string    `json:"username"`
	PasswordHash string    `json:"-"`
	CreatedAt    time.Time `json:"created_at"`
}

// Log represents a single log entry.
type Log struct {
	ID             int64     `json:"id"`
	UserID         string    `json:"user_id,omitempty"`
	Message        string    `json:"message"`
	Level          string    `json:"level"`
	CategoryID     int64     `json:"category_id"`
	CategoryName   string    `json:"category_name,omitempty"`
	CategoryColor  string    `json:"category_color,omitempty"`
	Pinned         bool      `json:"pinned"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
	AttachmentName *string   `json:"attachment_name,omitempty"`
	AttachmentPath *string   `json:"attachment_path,omitempty"`
	AttachmentSize *int64    `json:"attachment_size,omitempty"`
}

// Attachment represents an uploaded file associated with a log.
type Attachment struct {
	ID    int64  `json:"id"`
	LogID int64  `json:"log_id"`
	Name  string `json:"name"`
	Path  string `json:"path"`
	Size  int64  `json:"size"`
}

// Filter describes search + filter parameters for listing logs.
type Filter struct {
	UserID     string
	Search     string
	Level      string
	CategoryID int64 // 0 = any
	Limit      int
	Offset     int
}

// Stats contains aggregate counters for the dashboard.
type Stats struct {
	TotalLogs       int64           `json:"total_logs"`
	TotalCategories int64           `json:"total_categories"`
	Debug           int64           `json:"debug"`
	Info            int64           `json:"info"`
	Warning         int64           `json:"warning"`
	Error           int64           `json:"error"`
	Critical        int64           `json:"critical"`
	ByCategory      []CategoryCount `json:"by_category"`
	ByDay           []DayCount      `json:"by_day"`
}

// CategoryCount is a (category, count) pair for charts.
type CategoryCount struct {
	CategoryID    int64  `json:"category_id"`
	CategoryName  string `json:"category_name"`
	CategoryColor string `json:"category_color"`
	Count         int64  `json:"count"`
}

// DayCount is a (day, count) pair for charts.
type DayCount struct {
	Day   string `json:"day"`
	Count int64  `json:"count"`
}

// SSEEvent describes a server-sent event payload.
type SSEEvent struct {
	Type string      `json:"type"`
	Data interface{} `json:"data"`
}
