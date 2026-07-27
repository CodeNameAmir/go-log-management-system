package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
	_ "github.com/tursodatabase/libsql-client-go/libsql"

	"log-server/internal/models"
)

// Storer is the contract every backing store must satisfy so the HTTP layer
// can transparently talk to local SQLite, libsql (Turso) or Supabase PostgREST.
//
// All implementers (DB for SQLite/libsql and supabase.Client for Postgres via
// PostgREST) expose the same methods; the chosen backend is decided per
// request via the SessionManager.
type Storer interface {
	Close() error
	ListCategories(ctx context.Context) ([]models.Category, error)
	GetCategory(ctx context.Context, id int64) (models.Category, error)
	CreateCategory(ctx context.Context, name, color string) (int64, error)
	UpdateCategory(ctx context.Context, id int64, name, color string) error
	DeleteCategory(ctx context.Context, id int64) error
	CreateLog(ctx context.Context, l *models.Log) (int64, error)
	CreateLogAndGet(ctx context.Context, l *models.Log) (models.Log, error)
	UpdateLog(ctx context.Context, l *models.Log) error
	DeleteLog(ctx context.Context, id int64) (string, error)
	TogglePin(ctx context.Context, id int64) (bool, error)
	TogglePinAndGet(ctx context.Context, id int64) (bool, models.Log, error)
	UpdateLogAndGet(ctx context.Context, l *models.Log) (models.Log, error)
	GetLog(ctx context.Context, id int64) (models.Log, error)
	ListLogs(ctx context.Context, f models.Filter) ([]models.Log, int, error)
	SetAttachment(ctx context.Context, logID int64, name, path string, size int64) error
	ClearAttachment(ctx context.Context, logID int64) (string, error)
	Stats(ctx context.Context, userID int64) (models.Stats, error)
	CreateUser(ctx context.Context, username, passwordHash string) (int64, error)
	GetUserByUsername(ctx context.Context, username string) (models.User, error)
	GetUserByID(ctx context.Context, id int64) (models.User, error)
}

// Compile-time assertion: *DB implements Storer.
var _ Storer = (*DB)(nil)

// DB wraps a *sql.DB with prepared statements for common operations.
type DB struct {
	conn *sql.DB

	// Prepared statements (read).
	stmtListLogs          *sql.Stmt
	stmtCountLogs         *sql.Stmt
	stmtGetLog            *sql.Stmt
	stmtListCats          *sql.Stmt
	stmtGetCat            *sql.Stmt
	stmtStatsLevels       *sql.Stmt
	stmtStatsByCat        *sql.Stmt
	stmtStatsByDay        *sql.Stmt
	stmtCountCats         *sql.Stmt
	stmtCountLogsAll      *sql.Stmt
	stmtGetUserByUsername *sql.Stmt
	stmtGetUserByID       *sql.Stmt

	// Prepared statements (write).
	stmtInsertLog    *sql.Stmt
	stmtUpdateLog    *sql.Stmt
	stmtDeleteLog    *sql.Stmt
	stmtPinLog       *sql.Stmt
	stmtInsertCat    *sql.Stmt
	stmtUpdateCat    *sql.Stmt
	stmtDeleteCat    *sql.Stmt
	stmtInsertAttach *sql.Stmt
	stmtGetAttach    *sql.Stmt
	stmtDeleteAttach *sql.Stmt
	stmtInsertUser   *sql.Stmt
}

// Open opens (or creates) the SQLite database at path, runs migrations,
// prepares statements, and seeds default categories when empty.
// For Turso (libsql:// URLs), the path should be the full URL with auth token.
func Open(path string) (*DB, error) {
	var driverName string
	var dsn string

	// Check if it's a Turso/libsql URL
	if strings.HasPrefix(path, "libsql://") || strings.HasPrefix(path, "libsqls://") {
		driverName = "libsql"
		dsn = path
	} else if strings.HasPrefix(path, "file:") {
		// Already a DSN with file: prefix (from main.go buildDSN)
		driverName = "sqlite3"
		dsn = path
	} else {
		// Local SQLite file
		driverName = "sqlite3"
		dsn = fmt.Sprintf("file:%s?_journal_mode=WAL&_foreign_keys=on&_busy_timeout=5000", path)
	}

	conn, err := sql.Open(driverName, dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	// Configure connection pool based on database type
	if strings.HasPrefix(path, "libsql://") || strings.HasPrefix(path, "libsqls://") {
		// Turso/libSQL: Use more connections for remote database to reduce latency
		conn.SetMaxOpenConns(25)
		conn.SetMaxIdleConns(10)
		conn.SetConnMaxLifetime(5 * time.Minute)
		conn.SetConnMaxIdleTime(2 * time.Minute)
	} else {
		// Local SQLite: allow concurrent reads + 1 writer via WAL
		conn.SetMaxOpenConns(4)
		conn.SetMaxIdleConns(4)
		conn.SetConnMaxLifetime(0)
		conn.SetConnMaxIdleTime(5 * time.Minute)
	}

	db := &DB{conn: conn}
	if err := db.migrate(context.Background()); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	if err := db.prepare(); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("prepare: %w", err)
	}
	if err := db.seed(context.Background()); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("seed: %w", err)
	}
	slog.Info("database ready", "path", path)
	return db, nil
}

// Close releases all resources.
func (db *DB) Close() error {
	stmts := []*sql.Stmt{
		db.stmtListLogs, db.stmtCountLogs, db.stmtGetLog,
		db.stmtListCats, db.stmtGetCat,
		db.stmtStatsLevels, db.stmtStatsByCat, db.stmtStatsByDay,
		db.stmtCountCats, db.stmtCountLogsAll,
		db.stmtInsertLog, db.stmtUpdateLog, db.stmtDeleteLog, db.stmtPinLog,
		db.stmtInsertCat, db.stmtUpdateCat, db.stmtDeleteCat,
		db.stmtInsertAttach, db.stmtGetAttach, db.stmtDeleteAttach,
	}
	for _, s := range stmts {
		if s != nil {
			_ = s.Close()
		}
	}
	if db.conn != nil {
		return db.conn.Close()
	}
	return nil
}

func (db *DB) migrate(ctx context.Context) error {
	// First, create tables without the user_id index (which may fail if column doesn't exist)
	schema := `
	CREATE TABLE IF NOT EXISTS users (
		id            INTEGER PRIMARY KEY AUTOINCREMENT,
		username      TEXT NOT NULL UNIQUE,
		password_hash TEXT NOT NULL,
		created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS categories (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		name       TEXT NOT NULL UNIQUE,
		color      TEXT NOT NULL DEFAULT '#3b82f6',
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS logs (
		id              INTEGER PRIMARY KEY AUTOINCREMENT,
		message         TEXT NOT NULL,
		level           TEXT NOT NULL,
		category_id     INTEGER NOT NULL,
		pinned          INTEGER NOT NULL DEFAULT 0,
		created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		attachment_name TEXT,
		attachment_path TEXT,
		attachment_size INTEGER,
		user_id         INTEGER,
		FOREIGN KEY (category_id) REFERENCES categories(id) ON DELETE RESTRICT,
		FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
	);

	CREATE INDEX IF NOT EXISTS idx_logs_pinned_id   ON logs(pinned DESC, id DESC);
	CREATE INDEX IF NOT EXISTS idx_logs_level       ON logs(level);
	CREATE INDEX IF NOT EXISTS idx_logs_category    ON logs(category_id);
	CREATE INDEX IF NOT EXISTS idx_logs_created_at  ON logs(created_at);
	CREATE INDEX IF NOT EXISTS idx_logs_message     ON logs(message);
	-- idx_logs_user_id is created separately after ensuring column exists
	`
	if _, err := db.conn.ExecContext(ctx, schema); err != nil {
		return err
	}

	// Ensure user_id column exists in logs table (for existing databases)
	var columnExists bool
	err := db.conn.QueryRowContext(ctx, `
		SELECT count(*) = 1 
		FROM pragma_table_info('logs') 
		WHERE name = 'user_id'
	`).Scan(&columnExists)

	if err != nil {
		slog.Error("failed to check for user_id column", "err", err)
	}

	if !columnExists {
		slog.Info("adding missing user_id column to logs table")
		// SQLite doesn't support adding FK constraints via ALTER TABLE,
		// so we add the column without FK and enforce it at application level
		if _, err := db.conn.ExecContext(ctx, "ALTER TABLE logs ADD COLUMN user_id INTEGER"); err != nil {
			return fmt.Errorf("failed to add user_id column: %w", err)
		}
	}

	// Create index for user_id now that we know the column exists
	if _, err := db.conn.ExecContext(ctx, "CREATE INDEX IF NOT EXISTS idx_logs_user_id ON logs(user_id)"); err != nil {
		slog.Warn("failed to create user_id index", "err", err)
	}

	return nil
}

func (db *DB) prepare() error {
	var err error

	db.stmtInsertLog, err = db.conn.Prepare(`
		INSERT INTO logs (message, level, category_id, pinned, created_at, updated_at,
		                  attachment_name, attachment_path, attachment_size, user_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}

	db.stmtUpdateLog, err = db.conn.Prepare(`
		UPDATE logs
		SET message = ?, level = ?, category_id = ?, updated_at = ?,
		    attachment_name = ?, attachment_path = ?, attachment_size = ?
		WHERE id = ?`)
	if err != nil {
		return err
	}

	db.stmtDeleteLog, err = db.conn.Prepare(`DELETE FROM logs WHERE id = ?`)
	if err != nil {
		return err
	}

	db.stmtPinLog, err = db.conn.Prepare(`UPDATE logs SET pinned = ? WHERE id = ?`)
	if err != nil {
		return err
	}

	db.stmtGetLog, err = db.conn.Prepare(`
		SELECT l.id, l.user_id, l.message, l.level, l.category_id,
		       c.name, c.color,
		       l.pinned, l.created_at, l.updated_at,
		       l.attachment_name, l.attachment_path, l.attachment_size
		FROM logs l
		JOIN categories c ON c.id = l.category_id
		WHERE l.id = ?`)
	if err != nil {
		return err
	}

	// List/count logs are built dynamically due to dynamic filters, but
	// we keep reusable base SQL strings and prepare per-call for the
	// dynamic where-clause. Prepared base statements kept for static queries.
	db.stmtListLogs = nil  // built on the fly in ListLogs
	db.stmtCountLogs = nil // built on the fly in ListLogs

	db.stmtInsertCat, err = db.conn.Prepare(`
		INSERT INTO categories (name, color, created_at) VALUES (?, ?, ?)`)
	if err != nil {
		return err
	}
	db.stmtUpdateCat, err = db.conn.Prepare(`
		UPDATE categories SET name = ?, color = ? WHERE id = ?`)
	if err != nil {
		return err
	}
	db.stmtDeleteCat, err = db.conn.Prepare(`DELETE FROM categories WHERE id = ?`)
	if err != nil {
		return err
	}
	db.stmtGetCat, err = db.conn.Prepare(`
		SELECT id, name, color, created_at FROM categories WHERE id = ?`)
	if err != nil {
		return err
	}
	db.stmtListCats, err = db.conn.Prepare(`
		SELECT id, name, color, created_at FROM categories ORDER BY name ASC`)
	if err != nil {
		return err
	}
	db.stmtCountCats, err = db.conn.Prepare(`SELECT COUNT(*) FROM categories`)
	if err != nil {
		return err
	}
	db.stmtCountLogsAll, err = db.conn.Prepare(`SELECT COUNT(*) FROM logs WHERE user_id = ?`)
	if err != nil {
		return err
	}

	db.stmtStatsLevels, err = db.conn.Prepare(`
		SELECT level, COUNT(*) FROM logs WHERE user_id = ? GROUP BY level`)
	if err != nil {
		return err
	}
	db.stmtStatsByCat, err = db.conn.Prepare(`
		SELECT l.category_id, c.name, c.color, COUNT(*)
		FROM logs l JOIN categories c ON c.id = l.category_id
		WHERE l.user_id = ?
		GROUP BY l.category_id ORDER BY COUNT(*) DESC`)
	if err != nil {
		return err
	}
	db.stmtStatsByDay, err = db.conn.Prepare(`
		SELECT date(created_at) AS day, COUNT(*)
		FROM logs
		WHERE user_id = ?
		GROUP BY day
		ORDER BY day DESC
		LIMIT 30`)
	if err != nil {
		return err
	}

	db.stmtInsertUser, err = db.conn.Prepare(`
		INSERT INTO users (username, password_hash, created_at) VALUES (?, ?, ?)`)
	if err != nil {
		return err
	}
	db.stmtGetUserByUsername, err = db.conn.Prepare(`
		SELECT id, username, password_hash, created_at FROM users WHERE username = ?`)
	if err != nil {
		return err
	}
	db.stmtGetUserByID, err = db.conn.Prepare(`
		SELECT id, username, password_hash, created_at FROM users WHERE id = ?`)
	if err != nil {
		return err
	}

	db.stmtInsertAttach, err = db.conn.Prepare(`
		UPDATE logs
		SET attachment_name = ?, attachment_path = ?, attachment_size = ?, updated_at = ?
		WHERE id = ?`)
	if err != nil {
		return err
	}
	db.stmtGetAttach, err = db.conn.Prepare(`
		SELECT id, id, attachment_name, attachment_path, attachment_size
		FROM logs WHERE id = ? AND attachment_path IS NOT NULL`)
	if err != nil {
		return err
	}
	db.stmtDeleteAttach, err = db.conn.Prepare(`
		UPDATE logs SET attachment_name = NULL, attachment_path = NULL,
		                attachment_size = NULL, updated_at = ? WHERE id = ?`)
	if err != nil {
		return err
	}

	return nil
}

func (db *DB) seed(ctx context.Context) error {
	var n int
	if err := db.stmtCountCats.QueryRowContext(ctx).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	defaults := []struct{ name, color string }{
		{"General", "#3b82f6"},
		{"System", "#10b981"},
		{"Download", "#f59e0b"},
		{"API", "#8b5cf6"},
		{"Temp", "#ef4444"},
	}
	now := time.Now().UTC()
	for _, d := range defaults {
		if _, err := db.stmtInsertCat.ExecContext(ctx, d.name, d.color, now); err != nil {
			return err
		}
	}
	slog.Info("seeded default categories", "count", len(defaults))
	return nil
}

// -------- Categories --------

// ListCategories returns all categories ordered by name.
func (db *DB) ListCategories(ctx context.Context) ([]models.Category, error) {
	rows, err := db.stmtListCats.QueryContext(ctx)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.Category
	for rows.Next() {
		var c models.Category
		if err := rows.Scan(&c.ID, &c.Name, &c.Color, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetCategory returns a single category or ErrNotFound.
func (db *DB) GetCategory(ctx context.Context, id int64) (models.Category, error) {
	var c models.Category
	err := db.stmtGetCat.QueryRowContext(ctx, id).Scan(&c.ID, &c.Name, &c.Color, &c.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return c, ErrNotFound
	}
	return c, err
}

// CreateCategory inserts a category and returns its ID.
func (db *DB) CreateCategory(ctx context.Context, name, color string) (int64, error) {
	now := time.Now().UTC()
	res, err := db.stmtInsertCat.ExecContext(ctx, name, color, now)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint") {
			return 0, ErrDuplicate
		}
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil || id == 0 {
		// libsql remote fallback
		var fallbackID int64
		if err2 := db.conn.QueryRowContext(ctx,
			`SELECT id FROM categories WHERE name = ? AND created_at = ? ORDER BY id DESC LIMIT 1`,
			name, now).Scan(&fallbackID); err2 != nil {
			return 0, fmt.Errorf("create category: %w (fallback also failed: %v)", err, err2)
		}
		return fallbackID, nil
	}
	return id, nil
}

// UpdateCategory updates name/color.
func (db *DB) UpdateCategory(ctx context.Context, id int64, name, color string) error {
	res, err := db.stmtUpdateCat.ExecContext(ctx, name, color, id)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint") {
			return ErrDuplicate
		}
		return err
	}
	n, _ := res.RowsAffected()
	// libsql remote may return 0 rows affected even on success
	if n == 0 {
		var exists bool
		if err := db.conn.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM categories WHERE id = ?)`, id).Scan(&exists); err != nil || !exists {
			return ErrNotFound
		}
	}
	return nil
}

// DeleteCategory removes a category. Fails if logs reference it.
func (db *DB) DeleteCategory(ctx context.Context, id int64) error {
	res, err := db.stmtDeleteCat.ExecContext(ctx, id)
	if err != nil {
		if strings.Contains(err.Error(), "FOREIGN KEY") {
			return ErrInUse
		}
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		var exists bool
		if err := db.conn.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM categories WHERE id = ?)`, id).Scan(&exists); err != nil || !exists {
			return ErrNotFound
		}
	}
	return nil
}

// -------- Logs --------

// CreateLog inserts a log and returns its ID.
func (db *DB) CreateLog(ctx context.Context, l *models.Log) (int64, error) {
	now := time.Now().UTC()
	pinned := 0
	if l.Pinned {
		pinned = 1
	}
	var uID sql.NullString
	if l.UserID != "" {
		uID.String = l.UserID
		uID.Valid = true
	}
	res, err := db.stmtInsertLog.ExecContext(ctx,
		l.Message, l.Level, l.CategoryID, pinned, now, now,
		l.AttachmentName, l.AttachmentPath, l.AttachmentSize, uID,
	)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil || id == 0 {
		// libsql remote doesn't support LastInsertId, use RETURNING fallback
		var fallbackID int64
		err2 := db.conn.QueryRowContext(ctx,
			`SELECT id FROM logs WHERE message = ? AND created_at = ? ORDER BY id DESC LIMIT 1`,
			l.Message, now).Scan(&fallbackID)
		if err2 != nil {
			return 0, fmt.Errorf("create log: %w (fallback also failed: %v)", err, err2)
		}
		return fallbackID, nil
	}
	return id, nil
}

// CreateLogAndGet inserts a log and returns the full log with category fields in one operation.
// This avoids a second round-trip to fetch the log after creation.
func (db *DB) CreateLogAndGet(ctx context.Context, l *models.Log) (models.Log, error) {
	now := time.Now().UTC()
	pinned := 0
	if l.Pinned {
		pinned = 1
	}
	var uID sql.NullString
	if l.UserID != "" {
		uID.String = l.UserID
		uID.Valid = true
	}
	res, err := db.stmtInsertLog.ExecContext(ctx,
		l.Message, l.Level, l.CategoryID, pinned, now, now,
		l.AttachmentName, l.AttachmentPath, l.AttachmentSize, uID,
	)
	if err != nil {
		return models.Log{}, err
	}
	id, _ := res.LastInsertId()

	// libsql remote may not return LastInsertId. Fall back to a SELECT.
	if id == 0 {
		if err := db.conn.QueryRowContext(ctx,
			`SELECT id FROM logs WHERE message = ? AND created_at = ? ORDER BY id DESC LIMIT 1`,
			l.Message, now).Scan(&id); err != nil {
			return models.Log{}, err
		}
	}

	// Fetch the full log with category join
	var full models.Log
	var nuID sql.NullString
	err = db.stmtGetLog.QueryRowContext(ctx, id).Scan(
		&full.ID, &nuID, &full.Message, &full.Level, &full.CategoryID,
		&full.CategoryName, &full.CategoryColor,
		&full.Pinned, &full.CreatedAt, &full.UpdatedAt,
		&full.AttachmentName, &full.AttachmentPath, &full.AttachmentSize,
	)
	if err != nil {
		return models.Log{}, err
	}
	if nuID.Valid {
		full.UserID = nuID.String
	}
	return full, nil
}

// UpdateLog updates a log's core fields (not pinned).
func (db *DB) UpdateLog(ctx context.Context, l *models.Log) error {
	now := time.Now().UTC()
	res, err := db.stmtUpdateLog.ExecContext(ctx,
		l.Message, l.Level, l.CategoryID, now,
		l.AttachmentName, l.AttachmentPath, l.AttachmentSize,
		l.ID,
	)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	// libsql remote may return 0 rows affected even on success
	if n == 0 {
		var exists bool
		if err := db.conn.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM logs WHERE id = ?)`, l.ID).Scan(&exists); err != nil || !exists {
			return ErrNotFound
		}
	}
	return nil
}

// DeleteLog deletes a log row. Returns the attachment path (if any) so the
// caller can remove the file on disk.
func (db *DB) DeleteLog(ctx context.Context, id int64) (string, error) {
	// Read current attachment path.
	var path sql.NullString
	row := db.conn.QueryRowContext(ctx,
		`SELECT attachment_path FROM logs WHERE id = ?`, id)
	if err := row.Scan(&path); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", err
	}
	res, err := db.stmtDeleteLog.ExecContext(ctx, id)
	if err != nil {
		return "", err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		var exists bool
		if err := db.conn.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM logs WHERE id = ?)`, id).Scan(&exists); err != nil || !exists {
			return "", ErrNotFound
		}
	}
	if path.Valid {
		return path.String, nil
	}
	return "", nil
}

// TogglePin flips the pinned flag and returns the new state.
func (db *DB) TogglePin(ctx context.Context, id int64) (bool, error) {
	var cur int
	err := db.conn.QueryRowContext(ctx, `SELECT pinned FROM logs WHERE id = ?`, id).Scan(&cur)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrNotFound
	}
	if err != nil {
		return false, err
	}
	next := 0
	if cur == 0 {
		next = 1
	}
	if _, err := db.stmtPinLog.ExecContext(ctx, next, id); err != nil {
		return false, err
	}
	return next == 1, nil
}

// TogglePinAndGet flips the pinned flag and returns the full updated log in one operation.
func (db *DB) TogglePinAndGet(ctx context.Context, id int64) (bool, models.Log, error) {
	var cur int
	err := db.conn.QueryRowContext(ctx, `SELECT pinned FROM logs WHERE id = ?`, id).Scan(&cur)
	if errors.Is(err, sql.ErrNoRows) {
		return false, models.Log{}, ErrNotFound
	}
	if err != nil {
		return false, models.Log{}, err
	}
	next := 0
	if cur == 0 {
		next = 1
	}
	if _, err := db.stmtPinLog.ExecContext(ctx, next, id); err != nil {
		return false, models.Log{}, err
	}

	// Fetch the full log with category join
	var full models.Log
	var nuID sql.NullString
	err = db.stmtGetLog.QueryRowContext(ctx, id).Scan(
		&full.ID, &nuID, &full.Message, &full.Level, &full.CategoryID,
		&full.CategoryName, &full.CategoryColor,
		&full.Pinned, &full.CreatedAt, &full.UpdatedAt,
		&full.AttachmentName, &full.AttachmentPath, &full.AttachmentSize,
	)
	if err != nil {
		return false, models.Log{}, err
	}
	if nuID.Valid {
		full.UserID = nuID.String
	}
	return next == 1, full, nil
}

// UpdateLogAndGet updates a log and returns the full updated log in one operation.
func (db *DB) UpdateLogAndGet(ctx context.Context, l *models.Log) (models.Log, error) {
	now := time.Now().UTC()
	res, err := db.stmtUpdateLog.ExecContext(ctx,
		l.Message, l.Level, l.CategoryID, now,
		l.AttachmentName, l.AttachmentPath, l.AttachmentSize,
		l.ID,
	)
	if err != nil {
		return models.Log{}, err
	}
	n, _ := res.RowsAffected()
	// libsql remote may return 0 rows affected even on success
	if n == 0 {
		var exists bool
		if err := db.conn.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM logs WHERE id = ?)`, l.ID).Scan(&exists); err != nil || !exists {
			return models.Log{}, ErrNotFound
		}
	}

	// Fetch the full log with category join
	var full models.Log
	var nuID sql.NullString
	err = db.stmtGetLog.QueryRowContext(ctx, l.ID).Scan(
		&full.ID, &nuID, &full.Message, &full.Level, &full.CategoryID,
		&full.CategoryName, &full.CategoryColor,
		&full.Pinned, &full.CreatedAt, &full.UpdatedAt,
		&full.AttachmentName, &full.AttachmentPath, &full.AttachmentSize,
	)
	if err != nil {
		return models.Log{}, err
	}
	if nuID.Valid {
		full.UserID = nuID.String
	}
	return full, nil
}

// GetLog returns a full log with category fields.
func (db *DB) GetLog(ctx context.Context, id int64) (models.Log, error) {
	var l models.Log
	var uID sql.NullString
	err := db.stmtGetLog.QueryRowContext(ctx, id).Scan(
		&l.ID, &uID, &l.Message, &l.Level, &l.CategoryID,
		&l.CategoryName, &l.CategoryColor,
		&l.Pinned, &l.CreatedAt, &l.UpdatedAt,
		&l.AttachmentName, &l.AttachmentPath, &l.AttachmentSize,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return l, ErrNotFound
	}
	if err == nil && uID.Valid {
		l.UserID = uID.String
	}
	return l, err
}

// ListLogs returns logs matching the filter, ordered by pinned DESC, id DESC.
func (db *DB) ListLogs(ctx context.Context, f models.Filter) ([]models.Log, int, error) {
	where, args := buildWhere(f)

	var total int
	countSQL := `SELECT COUNT(*) FROM logs l ` + where
	if err := db.conn.QueryRowContext(ctx, countSQL, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	listSQL := `
		SELECT l.id, l.user_id, l.message, l.level, l.category_id,
		       c.name, c.color,
		       l.pinned, l.created_at, l.updated_at,
		       l.attachment_name, l.attachment_path, l.attachment_size
		FROM logs l
		JOIN categories c ON c.id = l.category_id
	` + where + ` ORDER BY l.pinned DESC, l.id DESC`

	if f.Limit > 0 {
		listSQL += ` LIMIT ? OFFSET ?`
		args = append(args, f.Limit, f.Offset)
	}

	rows, err := db.conn.QueryContext(ctx, listSQL, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []models.Log
	for rows.Next() {
		var l models.Log
		var uID sql.NullString
		if err := rows.Scan(
			&l.ID, &uID, &l.Message, &l.Level, &l.CategoryID,
			&l.CategoryName, &l.CategoryColor,
			&l.Pinned, &l.CreatedAt, &l.UpdatedAt,
			&l.AttachmentName, &l.AttachmentPath, &l.AttachmentSize,
		); err != nil {
			return nil, 0, err
		}
		if uID.Valid {
			l.UserID = uID.String
		}
		out = append(out, l)
	}
	return out, total, rows.Err()
}

func buildWhere(f models.Filter) (string, []interface{}) {
	clauses := []string{}
	args := []interface{}{}
	if f.UserID != "" {
		clauses = append(clauses, `l.user_id = ?`)
		args = append(args, f.UserID)
	}
	if f.Search != "" {
		clauses = append(clauses, `l.message LIKE ?`)
		args = append(args, "%"+f.Search+"%")
	}
	if f.Level != "" && models.IsValidLevel(f.Level) {
		clauses = append(clauses, `l.level = ?`)
		args = append(args, f.Level)
	}
	if f.CategoryID > 0 {
		clauses = append(clauses, `l.category_id = ?`)
		args = append(args, f.CategoryID)
	}
	if len(clauses) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(clauses, " AND "), args
}

// -------- Attachments --------

// SetAttachment stores attachment metadata on a log.
func (db *DB) SetAttachment(ctx context.Context, logID int64, name, path string, size int64) error {
	now := time.Now().UTC()
	_, err := db.stmtInsertAttach.ExecContext(ctx, name, path, size, now, logID)
	return err
}

// ClearAttachment removes attachment metadata from a log and returns the
// previous path so the caller can delete the file.
func (db *DB) ClearAttachment(ctx context.Context, logID int64) (string, error) {
	var path sql.NullString
	err := db.conn.QueryRowContext(ctx,
		`SELECT attachment_path FROM logs WHERE id = ?`, logID).Scan(&path)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	now := time.Now().UTC()
	if _, err := db.stmtDeleteAttach.ExecContext(ctx, now, logID); err != nil {
		return "", err
	}
	if path.Valid {
		return path.String, nil
	}
	return "", nil
}

// -------- Stats --------

// Stats returns aggregate stats for the dashboard.
func (db *DB) Stats(ctx context.Context, userID int64) (models.Stats, error) {
	s := models.Stats{}
	if err := db.stmtCountLogsAll.QueryRowContext(ctx, userID).Scan(&s.TotalLogs); err != nil {
		return s, err
	}
	if err := db.stmtCountCats.QueryRowContext(ctx).Scan(&s.TotalCategories); err != nil {
		return s, err
	}
	rows, err := db.stmtStatsLevels.QueryContext(ctx, userID)
	if err != nil {
		return s, err
	}
	for rows.Next() {
		var lvl string
		var n int64
		if err := rows.Scan(&lvl, &n); err != nil {
			rows.Close()
			return s, err
		}
		switch lvl {
		case models.LevelDebug:
			s.Debug = n
		case models.LevelInfo:
			s.Info = n
		case models.LevelWarning:
			s.Warning = n
		case models.LevelError:
			s.Error = n
		case models.LevelCritical:
			s.Critical = n
		}
	}
	rows.Close()

	rows, err = db.stmtStatsByCat.QueryContext(ctx, userID)
	if err != nil {
		return s, err
	}
	for rows.Next() {
		var cc models.CategoryCount
		if err := rows.Scan(&cc.CategoryID, &cc.CategoryName, &cc.CategoryColor, &cc.Count); err != nil {
			rows.Close()
			return s, err
		}
		s.ByCategory = append(s.ByCategory, cc)
	}
	rows.Close()

	rows, err = db.stmtStatsByDay.QueryContext(ctx, userID)
	if err != nil {
		return s, err
	}
	for rows.Next() {
		var dc models.DayCount
		if err := rows.Scan(&dc.Day, &dc.Count); err != nil {
			rows.Close()
			return s, err
		}
		s.ByDay = append(s.ByDay, dc)
	}
	rows.Close()
	// reverse by day so chart is chronological
	for i, j := 0, len(s.ByDay)-1; i < j; i, j = i+1, j-1 {
		s.ByDay[i], s.ByDay[j] = s.ByDay[j], s.ByDay[i]
	}
	return s, nil
}

// -------- Users --------

// CreateUser creates a new user and returns the ID.
func (db *DB) CreateUser(ctx context.Context, username, passwordHash string) (int64, error) {
	now := time.Now().UTC()
	res, err := db.stmtInsertUser.ExecContext(ctx, username, passwordHash, now)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint") {
			return 0, ErrDuplicate
		}
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil || id == 0 {
		var fallbackID int64
		if err2 := db.conn.QueryRowContext(ctx,
			`SELECT id FROM users WHERE username = ?`, username).Scan(&fallbackID); err2 != nil {
			return 0, fmt.Errorf("create user: %w (fallback also failed: %v)", err, err2)
		}
		return fallbackID, nil
	}
	return id, nil
}

// GetUserByUsername retrieves a user by username.
func (db *DB) GetUserByUsername(ctx context.Context, username string) (models.User, error) {
	var u models.User
	err := db.stmtGetUserByUsername.QueryRowContext(ctx, username).Scan(&u.ID, &u.Username, &u.PasswordHash, &u.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return u, ErrNotFound
	}
	return u, err
}

// GetUserByID retrieves a user by ID.
func (db *DB) GetUserByID(ctx context.Context, id int64) (models.User, error) {
	var u models.User
	err := db.stmtGetUserByID.QueryRowContext(ctx, id).Scan(&u.ID, &u.Username, &u.PasswordHash, &u.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return u, ErrNotFound
	}
	return u, err
}

// SafeFileName returns a sanitized base name suitable for storage.
func SafeFileName(name string) string {
	return filepath.Base(strings.ReplaceAll(strings.ReplaceAll(name, "\\", "_"), "/", "_"))
}

// sentinel errors
var (
	ErrNotFound  = errors.New("not found")
	ErrDuplicate = errors.New("duplicate")
	ErrInUse     = errors.New("in use")
)
