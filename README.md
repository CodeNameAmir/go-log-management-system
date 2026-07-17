# Go Log Management System

A comprehensive, production-ready log management system built with Go (Golang) and a modern vanilla JavaScript frontend. Features real-time log monitoring, statistics dashboards, category management, import/export capabilities, and secure authentication.

---

## 🚀 Features

### Backend (Go)

#### Authentication & Security
- **JWT-based Authentication** — HS256 signed tokens with HttpOnly cookies
- **User Registration & Login** — Secure signup/login with bcrypt password hashing (cost 12)
- **Password Change** — Authenticated users can update their passwords
- **Protected Routes** — Middleware-based route protection
- **Session Management** — Secure cookie-based sessions with automatic cleanup

#### Log Management
- **Full CRUD Operations** — Create, read, update, delete logs
- **Advanced Filtering** — Filter by level (DEBUG/INFO/WARNING/ERROR/CRITICAL), category, search query
- **Pagination** — Configurable page size with offset-based pagination
- **Bulk Operations** — Bulk create and bulk delete with filters
- **Pin/Unpin Logs** — Pin important logs to keep them at the top
- **Real-time Polling Support** — Optimized endpoints for frontend polling

#### Category Management
- **Full Category CRUD** — Create, read, update, delete log categories
- **Color-coded Categories** — Hex color support for visual distinction
- **Default Categories** — 10 pre-configured categories (System, Application, Database, Network, Security, API, Auth, Cache, Queue, Email)
- **Usage Statistics** — Track log count per category

#### Statistics & Analytics
- **Level Distribution** — Count of logs by severity level
- **Category Breakdown** — Log counts grouped by category with colors
- **Daily Trends** — Last 30 days of log volume
- **Real-time Stats Endpoint** — `/api/stats` for dashboard charts

#### Import / Export
- **Export to CSV/JSON** — Filtered export with all log fields
- **Import from CSV/JSON** — Batch import with validation and error reporting
- **Flexible Field Mapping** — Supports level, category, message, metadata, timestamps

#### File Upload
- **Multipart File Upload** — Upload files to `/uploads` directory
- **Static File Serving** — Serve uploaded files via `/uploads/` and `/static/` routes
- **Configurable Limits** — 32MB max upload size

#### Database
- **SQLite** — Zero-configuration embedded database
- **Auto-migration** — Automatic schema creation and migration on startup
- **Indexes** — Optimized indexes for common query patterns
- **Foreign Keys** — Referential integrity between users, categories, and logs

---

### Frontend (Single-Page Application)

#### Dashboard
- **Real-time Log Table** — Sortable, filterable, paginated log view
- **Live Connection Indicator** — Shows online/connecting/offline status
- **Auto-refresh** — 5-second polling interval for real-time updates
- **New Row Highlight** — Visual flash animation for newly arrived logs

#### Log Interactions
- **Pin/Unpin** — Keep critical logs visible at the top with 📌 indicator
- **Inline Level Badges** — Color-coded severity badges (DEBUG/INFO/WARNING/ERROR/CRITICAL)
- **Category Pills** — Colored category tags with names
- **Metadata Viewer** — Modal to view full JSON metadata
- **Delete Confirmation** — Safe deletion with confirmation dialog

#### Browser Notifications
- **Push Notifications** — Native browser notifications for ERROR/CRITICAL logs
- **Permission Management** — One-click enable/disable with status indicator
- **Smart Filtering** — Only notifies for logs not created by current session
- **Auto-dismiss** — Configurable notification duration

#### Statistics Dashboard
- **Three Interactive Charts** (powered by Chart.js):
  - 🍩 **Doughnut Chart** — Log distribution by level
  - 🥧 **Pie Chart** — Log distribution by category (with category colors)
  - 📊 **Bar Chart** — Daily log volume for last 30 days
- **Auto-refresh** — Updates every 15 seconds when tab is active
- **Responsive Design** — Works on mobile and desktop

#### Category Management
- **Modal-based CRUD** — Add, edit, delete categories without page reload
- **Color Picker** — Visual hex color selection
- **Usage Indicators** — Shows log count per category
- **Protection** — Prevents deletion of categories with existing logs

#### Import / Export
- **Export Modal** — CSV/JSON export with current filters applied
- **Import Modal** — Drag-and-drop or file picker for CSV/JSON
- **Validation Feedback** — Detailed success/error counts with error details
- **Format Detection** — Automatic CSV/JSON format handling

#### File Upload
- **Drag & Drop Zone** — Visual upload area with progress
- **File List** — Shows uploaded files with download links
- **Multi-file Support** — Upload multiple files simultaneously

#### User Profile
- **Change Password** — Secure password update with current password verification
- **User Info Display** — Shows logged-in username in header

#### Authentication Pages
- **Clean Login/Signup** — Toggle between modes without page reload
- **Responsive Design** — Mobile-friendly auth cards
- **Toast Notifications** — Success/error feedback
- **Auto-redirect** — Seamless navigation after auth

---

## 🛠 Tech Stack

| Layer | Technology |
|-------|------------|
| **Language** | Go 1.21+ |
| **Web Framework** | Standard library `net/http` (no external framework) |
| **Database** | SQLite3 (`github.com/mattn/go-sqlite3`) |
| **Authentication** | JWT (`github.com/golang-jwt/jwt/v5`), bcrypt (`golang.org/x/crypto/bcrypt`) |
| **Frontend** | Vanilla JavaScript (ES6+), Chart.js (via CDN) |
| **Templating** | Go `html/template` |
| **Build** | Go toolchain, Windows resource compiler (optional) |

---

## 📁 Project Structure

```
go-log-management-system/
├── main.go                    # Application entry point
├── go.mod / go.sum            # Go module definitions
├── resource.rc                # Windows resource file (icon, manifest)
├── icon.ico                   # Application icon
├── rsrc_windows_*.syso        # Pre-compiled Windows resources
├── .gitignore
├── README.md
│
├── internal/
│   ├── api/
│   │   ├── handlers.go        # HTTP handlers (auth, logs, categories, stats, upload, import/export)
│   │   ├── middleware.go      # Auth middleware, CORS, logging
│   │   └── routes.go          # Route registration
│   ├── database/
│   │   ├── db.go              # Database connection & migration
│   │   └── queries.go         # SQL queries & data access
│   └── models/
│       └── models.go          # Data structures (User, Log, Category, API requests/responses)
│
├── web/
│   └── templates/
│       ├── index.html         # Main SPA (dashboard, stats, categories, import/export, upload, profile)
│       └── login.html         # Login/Signup page
│
└── uploads/                   # Uploaded files (created at runtime)
    └── .gitkeep
```

---

## ⚙️ Getting Started

### Prerequisites
- **Go 1.21 or higher**
- **GCC** (required for `mattn/go-sqlite3` CGO compilation)
  - Windows: [TDM-GCC](https://jmeubank.github.io/tdm-gcc/) or [MinGW-w64](https://www.mingw-w64.org/)
  - Linux: `build-essential` package
  - macOS: Xcode Command Line Tools (`xcode-select --install`)

### Installation

```bash
# Clone the repository
git clone https://github.com/yourusername/go-log-management-system.git
cd go-log-management-system

# Download dependencies
go mod download

# Build the application
go build -o log-server .

# Run the server
./log-server
```

### Running in Development

```bash
# Run directly with Go (auto-reload not included)
go run main.go

# Or use air for hot reload (install first: go install github.com/air-verse/air@latest)
air
```

The server starts on **http://localhost:8080**

---

## 🔧 Configuration

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `PORT` | `8080` | HTTP server port |
| `DB_PATH` | `logs.db` | SQLite database file path |
| `JWT_SECRET` | `change-this-secret-in-production` | JWT signing key (min 32 chars for production) |
| `UPLOAD_DIR` | `./uploads` | Directory for file uploads |

### Example `.env` file

```env
PORT=8080
DB_PATH=./data/logs.db
JWT_SECRET=your-super-secret-jwt-key-at-least-32-characters-long
UPLOAD_DIR=./uploads
```

---

## 📚 API Documentation

### Base URL
```
http://localhost:8080/api
```

### Authentication Endpoints

| Method | Endpoint | Description | Auth |
|--------|----------|-------------|------|
| `POST` | `/api/signup` | Register new user | ❌ |
| `POST` | `/api/login` | Login user | ❌ |
| `POST` | `/api/logout` | Logout (clears cookie) | ✅ |
| `GET` | `/api/me` | Get current user info | ✅ |
| `POST` | `/api/change-password` | Change password | ✅ |

#### Signup Request
```json
POST /api/signup
Content-Type: application/json

{
  "username": "admin",
  "password": "securepassword123"
}
```

#### Login Request
```json
POST /api/login
Content-Type: application/json

{
  "username": "admin",
  "password": "securepassword123"
}
```

#### Change Password Request
```json
POST /api/change-password
Content-Type: application/json
Cookie: token=...

{
  "current_password": "oldpassword",
  "new_password": "newsecurepassword123"
}
```

---

### Log Endpoints

| Method | Endpoint | Description | Auth |
|--------|----------|-------------|------|
| `GET` | `/api/logs` | List logs (paginated, filtered) | ✅ |
| `POST` | `/api/logs` | Create single log | ✅ |
| `POST` | `/api/logs/bulk` | Create multiple logs | ✅ |
| `GET` | `/api/logs/:id` | Get single log by ID | ✅ |
| `PATCH` | `/api/logs/:id` | Update log (level, message, metadata, pinned) | ✅ |
| `PATCH` | `/api/logs/:id/pin` | Toggle pin status | ✅ |
| `DELETE` | `/api/logs/:id` | Delete single log | ✅ |
| `DELETE` | `/api/logs` | Bulk delete with filters | ✅ |

#### Query Parameters (GET /api/logs)

| Parameter | Type | Description |
|-----------|------|-------------|
| `page` | int | Page number (default: 1) |
| `limit` | int | Items per page (default: 50, max: 500) |
| `q` | string | Search query (matches message) |
| `level` | string | Filter by level: DEBUG, INFO, WARNING, ERROR, CRITICAL |
| `category_id` | int | Filter by category ID |
| `format` | string | Export format: csv, json (for `/api/export`) |

#### Create Log Request
```json
POST /api/logs
Content-Type: application/json

{
  "level": "ERROR",
  "category_id": 1,
  "message": "Database connection failed",
  "metadata": {
    "host": "db-primary",
    "retry_count": 3
  }
}
```

#### Bulk Create Logs Request
```json
POST /api/logs/bulk
Content-Type: application/json

{
  "logs": [
    {"level": "INFO", "category_id": 1, "message": "Service started"},
    {"level": "WARNING", "category_id": 2, "message": "High memory usage", "metadata": {"usage": "85%"}
  ]
}
```

---

### Category Endpoints

| Method | Endpoint | Description | Auth |
|--------|----------|-------------|------|
| `GET` | `/api/categories` | List all categories with log counts | ✅ |
| `POST` | `/api/categories` | Create category | ✅ |
| `PATCH` | `/api/categories/:id` | Update category | ✅ |
| `DELETE` | `/api/categories/:id` | Delete category (fails if logs exist) | ✅ |

#### Create Category Request
```json
POST /api/categories
Content-Type: application/json

{
  "name": "Payment",
  "color": "#f59e0b"
}
```

---

### Statistics Endpoint

| Method | Endpoint | Description | Auth |
|--------|----------|-------------|------|
| `GET` | `/api/stats` | Get dashboard statistics | ✅ |

#### Response
```json
{
  "total_logs": 1250,
  "total_categories": 8,
  "debug": 120,
  "info": 450,
  "warning": 200,
  "error": 380,
  "critical": 100,
  "by_category": [
    {"category_id": 1, "category_name": "System", "category_color": "#3b82f6", "count": 300},
    {"category_id": 2, "category_name": "Database", "category_color": "#ef4444", "count": 150}
  ],
  "by_day": [
    {"day": "2024-01-15", "count": 45},
    {"day": "2024-01-16", "count": 52}
  ]
}
```

---

### Import / Export Endpoints

| Method | Endpoint | Description | Auth |
|--------|----------|-------------|------|
| `GET` | `/api/export` | Export logs (CSV/JSON) with filters | ✅ |
| `POST` | `/api/import` | Import logs from CSV/JSON file | ✅ |

#### Export Example
```
GET /api/export?format=csv&level=ERROR&category_id=1&q=timeout
```

#### Import Request (multipart/form-data)
```
POST /api/import
Content-Type: multipart/form-data

file: <CSV or JSON file>
format: csv (or json, auto-detected if omitted)
```

#### Import Response
```json
{
  "success": 95,
  "errors": 3,
  "details": [
    {"row": 5, "error": "invalid level: 'UNKNOWN'"},
    {"row": 12, "error": "category not found: 999"}
  ]
}
```

---

### File Upload Endpoints

| Method | Endpoint | Description | Auth |
|--------|----------|-------------|------|
| `POST` | `/api/upload` | Upload file(s) | ✅ |
| `GET` | `/uploads/*` | Serve uploaded files | ❌ |
| `GET` | `/static/*` | Serve static files | ❌ |

#### Upload Request
```
POST /api/upload
Content-Type: multipart/form-data

files: <one or more files>
```

#### Upload Response
```json
{
  "files": [
    {"name": "error.log", "size": 1024, "url": "/uploads/error.log"}
  ]
}
```

---

## 🎨 Frontend Features Walkthrough

### Dashboard (`/`)
Main log management interface with:
- **Header**: App title, connection status, notification toggle, user menu, data menu
- **Filter Bar**: Search input, level dropdown, category dropdown, refresh button
- **Log Table**: Paginated, sortable table with pin, level badge, category, message, metadata, timestamp, actions
- **Pagination**: Page navigation with info
- **Tabs**: Dashboard / Statistics / Categories

### Statistics Tab
Three Chart.js visualizations:
1. **Level Distribution** (Doughnut) — Color-coded by severity
2. **Category Distribution** (Pie) — Uses category colors
3. **Daily Volume** (Bar) — Last 30 days trend

### Categories Tab
Modal-based management:
- **Add Category** — Name + color picker
- **Edit Category** — Inline edit with color picker
- **Delete Category** — With confirmation, blocked if logs exist
- **Usage Count** — Shows log count per category

### Data Menu (Header)
- **Import CSV / Import JSON** — Opens import modal
- **Export CSV / Export JSON** — Downloads filtered data

### Import Modal
- Format selector (CSV/JSON)
- File picker / drag-and-drop
- Real-time validation feedback
- Success/error summary with details

### Upload Modal
- Drag-and-drop zone
- Multiple file support
- Progress indication
- File list with download links

### Profile Modal
- Change password form
- Current password verification
- Toast feedback

### Login Page (`/login`)
- Toggle between Login / Signup
- Clean, centered card design
- Toast notifications
- Auto-redirect to dashboard on success

---

## 🗄 Database Schema

### `users` table
```sql
CREATE TABLE users (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    username TEXT UNIQUE NOT NULL,
    password_hash TEXT NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
```

### `categories` table
```sql
CREATE TABLE categories (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT UNIQUE NOT NULL,
    color TEXT DEFAULT '#666666',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
```

### `logs` table
```sql
CREATE TABLE logs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    category_id INTEGER REFERENCES categories(id) ON DELETE SET NULL,
    level TEXT NOT NULL CHECK(level IN ('DEBUG','INFO','WARNING','ERROR','CRITICAL')),
    message TEXT NOT NULL,
    metadata TEXT,  -- JSON string
    pinned BOOLEAN DEFAULT 0,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
```

### Indexes
```sql
CREATE INDEX idx_logs_user_id ON logs(user_id);
CREATE INDEX idx_logs_category_id ON logs(category_id);
CREATE INDEX idx_logs_level ON logs(level);
CREATE INDEX idx_logs_created_at ON logs(created_at);
CREATE INDEX idx_logs_pinned ON logs(pinned);
```

### Default Categories (auto-inserted on migration)
| Name | Color |
|------|-------|
| System | #3b82f6 |
| Application | #22c55e |
| Database | #ef4444 |
| Network | #06b6d4 |
| Security | #f59e0b |
| API | #a855f7 |
| Auth | #ec4899 |
| Cache | #14b8a6 |
| Queue | #8b5cf6 |
| Email | #eab308 |

---

## 🔐 Authentication Details

### JWT Token
- **Algorithm**: HS256
- **Claims**: `user_id`, `username`, `exp` (24 hours), `iat`
- **Storage**: HttpOnly, Secure (in production), SameSite=Lax cookie named `token`
- **Secret**: Configurable via `JWT_SECRET` env var

### Password Hashing
- **Algorithm**: bcrypt
- **Cost**: 12 (adjustable in `handlers.go`)
- **Validation**: Minimum 8 characters (enforced on signup/change)

### Protected Routes
All `/api/*` routes except `/api/login`, `/api/signup` require valid JWT cookie.

---

## 🏗 Build & Deploy

### Development Build
```bash
go build -o log-server .
./log-server
```

### Production Build (Linux/macOS)
```bash
CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o log-server .
```

### Production Build (Windows)
```bash
CGO_ENABLED=1 GOOS=windows GOARCH=amd64 go build -ldflags="-s -w" -o log-server.exe .
```

### With Windows Resources (icon, manifest)
```bash
# Requires windres (mingw-w64)
windres -i resource.rc -o rsrc_windows_amd64.syso -O coff
go build -ldflags="-s -w" -o log-server.exe .
```

### Docker
```dockerfile
FROM golang:1.21-alpine AS builder
RUN apk add --no-cache gcc musl-dev sqlite-dev
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=1 go build -ldflags="-s -w" -o log-server .

FROM alpine:3.18
RUN apk add --no-cache sqlite-libs ca-certificates
WORKDIR /app
COPY --from=builder /app/log-server .
COPY --from=builder /app/web ./web
COPY --from=builder /app/uploads ./uploads
EXPOSE 8080
CMD ["./log-server"]
```

```bash
docker build -t log-server .
docker run -d -p 8080:8080 -v $(pwd)/data:/app/data -v $(pwd)/uploads:/app/uploads --name log-server log-server
```

### Systemd Service (Linux)
```ini
# /etc/systemd/system/log-server.service
[Unit]
Description=Go Log Management System
After=network.target

[Service]
Type=simple
User=loguser
WorkingDirectory=/opt/log-server
ExecStart=/opt/log-server/log-server
Environment=PORT=8080
Environment=DB_PATH=/opt/log-server/data/logs.db
Environment=JWT_SECRET=your-production-secret-key-here
Environment=UPLOAD_DIR=/opt/log-server/uploads
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now log-server
```

### Reverse Proxy (Nginx)
```nginx
server {
    listen 80;
    server_name logs.yourdomain.com;

    location / {
        proxy_pass http://localhost:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        
        # WebSocket support (if needed later)
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
    }
    
    # Optional: Serve static files directly
    location /uploads/ {
        alias /opt/log-server/uploads/;
        expires 30d;
    }
    
    location /static/ {
        alias /opt/log-server/web/static/;
        expires 30d;
    }
}
```

---

## 🧪 Development

### Running Tests
```bash
# Run all tests
go test ./...

# Run with coverage
go test -cover ./...

# Run specific package
go test ./internal/database/...
```

### Code Style
```bash
# Format code
gofmt -w .

# Vet for issues
go vet ./...

# Lint (install golangci-lint first)
golangci-lint run
```

### Database Migration
Migrations run automatically on startup. To reset:
```bash
rm logs.db  # Deletes database, will be recreated on next start
```

---

## 📝 API Usage Examples

### cURL Examples

#### Login
```bash
curl -X POST http://localhost:8080/api/login \
  -H "Content-Type: application/json" \
  -d '{"username":"admin","password":"password"}' \
  -c cookies.txt
```

#### Create Log
```bash
curl -X POST http://localhost:8080/api/logs \
  -H "Content-Type: application/json" \
  -b cookies.txt \
  -d '{"level":"ERROR","category_id":1,"message":"Test error","metadata":{"key":"value"}}'
```

#### List Logs with Filters
```bash
curl "http://localhost:8080/api/logs?page=1&limit=20&level=ERROR&category_id=1&q=timeout" \
  -b cookies.txt
```

#### Get Statistics
```bash
curl http://localhost:8080/api/stats -b cookies.txt
```

#### Export CSV
```bash
curl "http://localhost:8080/api/export?format=csv&level=ERROR" \
  -b cookies.txt -o errors.csv
```

#### Import JSON
```bash
curl -X POST http://localhost:8080/api/import \
  -b cookies.txt \
  -F "file=@logs.json" \
  -F "format=json"
```

#### Upload File
```bash
curl -X POST http://localhost:8080/api/upload \
  -b cookies.txt \
  -F "files=@application.log"
```

---
