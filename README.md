# Log Server

A production-ready, self-contained Log Management System written in Go.

## Features

- Create, edit, delete, pin, and search logs
- 5 log levels: DEBUG, INFO, WARNING, ERROR, CRITICAL
- Color-coded categories (create, edit, delete)
- File attachments (images, PDF, TXT, ZIP) — validated and safely stored
- **Import logs** from JSON or CSV files
- **Export logs** to JSON or CSV (respects current filters)
- Real-time multi-device sync via Server-Sent Events (SSE)
- Live connection status indicator with auto-reconnect
- Browser notifications for ERROR / CRITICAL logs + test button
- Statistics dashboard with Chart.js (logs by level / category / day)
- Full-text, case-insensitive, instant search
- Combined filters (search + level + category)
- Pinned logs always first, visually highlighted
- Modern **dark terminal-inspired theme** (black/white/gray)
- Fully responsive design

## Requirements

- Go 1.24+
- A C compiler for `go-sqlite3` (GCC on Linux, MSYS2/MinGW or TDM-GCC on Windows)

## Build & Run

```bash
# fetch dependencies
go mod tidy

# run directly
go run main.go

# or build a single executable
go build -o log-server
./log-server            # Linux / macOS
log-server.exe          # Windows
```

Flags:

| Flag          | Default    | Description                          |
| ------------- | ---------- | ------------------------------------ |
| `-addr`       | `:8080`    | HTTP listen address                  |
| `-db`         | `logs.db`  | SQLite database file                 |
| `-uploads`    | `uploads`  | Directory for file attachments       |
| `-max-upload` | `10485760` | Max upload size in bytes (10 MiB)    |
| `-debug`      | `false`    | Enable debug logging                 |

Open <http://localhost:8080> in your browser.

## Architecture

```
main.go                       entry point, HTTP server, colored output
internal/
  models/models.go            domain types
  database/database.go        SQLite repository, migrations, prepared stmts
  api/
    server.go                 router, middleware helpers
    logs.go                   /api/logs handlers
    categories.go             /api/categories handlers
    attachments.go            multipart upload with validation
    sse.go                    Server-Sent Events broker (pub/sub)
    export.go                 CSV/JSON export + import handlers
    stats.go                  dashboard statistics
web/templates/index.html      single-page UI (embedded via go:embed)
uploads/                      attachment storage (auto-created)
```

## API

| Method | Path                          | Description                    |
| ------ | ----------------------------- | ------------------------------ |
| GET    | `/api/events`                 | SSE stream                     |
| GET    | `/api/stats`                  | Dashboard statistics           |
| GET    | `/api/logs?q=&level=&category_id=` | List with filters         |
| POST   | `/api/logs`                   | Create log                     |
| GET    | `/api/logs/{id}`              | Get log                        |
| PUT    | `/api/logs/{id}`              | Update log                     |
| DELETE | `/api/logs/{id}`              | Delete log                     |
| POST   | `/api/logs/{id}/pin`          | Toggle pin                     |
| POST   | `/api/logs/{id}/attachments`  | Upload attachment (multipart)  |
| DELETE | `/api/logs/{id}/attachments`  | Remove attachment              |
| GET    | `/api/categories`             | List categories                |
| POST   | `/api/categories`             | Create category                |
| PUT    | `/api/categories/{id}`        | Update category                |
| DELETE | `/api/categories/{id}`        | Delete category                |
| GET    | `/api/export?format=csv\|json`| Export current filtered view   |
| POST   | `/api/import?format=csv\|json`| Import logs from file          |
| GET    | `/healthz`                    | Health check                   |

## Import Format

### JSON
Array of log objects:
```json
[
  {"message": "User logged in", "level": "INFO", "category_name": "Auth"},
  {"message": "DB connection failed", "level": "ERROR", "category_name": "Database", "pinned": true}
]
```

### CSV
Headers: `id, level, category, category_id, message, pinned, created_at`
```csv
id,level,category,message,pinned
1,INFO,Auth,User logged in,false
2,ERROR,Database,Connection timeout,true
```

## Security

- SQLite WAL mode + foreign keys enabled
- Prepared statements for all hot paths
- Upload validation by extension, magic bytes, and size
- Randomized safe filenames for stored attachments
- Uploads served with `Content-Disposition: attachment` and `X-Content-Type-Options: nosniff`
- Path traversal checks on file serving
- Request body size limits on JSON and multipart

## Terminal Output

The application features colored, structured terminal output:

- **Green**: Success messages, startup complete
- **Yellow**: Warnings
- **Red**: Errors
- **Cyan**: Informational messages

Use `--debug` flag for detailed HTTP request logging.

## Deployment

Copy the single `log-server` binary (or `log-server.exe`) plus the `uploads/`
directory to your target machine. Run it directly, or behind a reverse proxy
for TLS. No Docker, PHP, Node, or external web server required.

## Real-Time Sync

The application uses Server-Sent Events (SSE) for real-time synchronization:

- New logs appear automatically across all connected devices
- Deleted/edited/pinned logs update in real-time
- Connection status indicator shows live/offline state
- Auto-reconnect on connection loss (2.5s intervals)
- No page refresh required
- Filters and search are preserved during updates
