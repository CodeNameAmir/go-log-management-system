# Log Management System

A self-hosted web application for managing, searching, and visualizing log entries with support for **Local SQLite**, **Turso**, and **Supabase** databases.

![Screenshot](screenshot.png)

## Features

- **Multi-backend support**: Local SQLite, Turso (remote SQLite), or Supabase (PostgreSQL + Auth)
- **Real-time updates**: SSE (Server-Sent Events) for live log streaming
- **Advanced filtering**: Search by message, level, category
- **Analytics dashboard**: Charts for log levels, categories, and daily trends
- **File attachments**: Upload/download files with any log entry
- **Import/Export**: CSV and JSON format support
- **Desktop integration**: System tray icon with quick access
- **Browser notifications**: Alerts for ERROR and CRITICAL logs

## Quick Start

### Option 1: Double-click (Windows)

Just run `log-server.exe`. It will start with Local SQLite database by default.

> **For Supabase**: Create a `config.json` file next to the exe (see example below) or pass CLI flags.

### Option 2: Command-line

```bash
# Local SQLite (default)
log-server.exe

# With Supabase
log-server.exe -supabase-url "https://your-project.supabase.co" -supabase-key "your-anon-key"

# With Turso
log-server.exe -turso
```

### Option 3: Config file

Create `config.json` in the same directory as the executable:

```json
{
    "supabase_url": "https://your-project.supabase.co",
    "supabase_key": "eyJhbGciOiJIUzI1NiIs..."
}
```

Then just double-click `log-server.exe`. The Supabase credentials are auto-loaded.

## Database Setup

### Local SQLite

Works out of the box — no setup required.

### Turso

Use the `-turso` flag or select "Turso" in the login page. Default credentials are pre-configured.

### Supabase

1. Create a project at [supabase.com](https://supabase.com)
2. Run the SQL schema from `supabase-schema.sql` in the SQL Editor
3. Enable **Email Auth** in Authentication → Providers → Email (disable "Confirm email")
4. Start the server with your Supabase URL and anon key
5. Open the web UI, select "Supabase", sign up, and log in

> ⚠️ **Important**: If the login page shows your Supabase URL/Key pre-filled, it means the server loaded them from `config.json`. You can override them in the form — your custom settings will be saved in the browser's localStorage.

## Usage

1. Open `http://localhost:8080` in your browser
2. Create an account (or use default credentials)
3. Start adding log entries
4. Use the dashboard to search, filter, and analyze logs

### Login page credentials are saved

- **Turso** and **Supabase** credentials you enter in the login form are saved to your browser's `localStorage`
- Next time you visit the page, they'll be auto-filled
- If you clear your browser data, the server's defaults (from `config.json`) will be used

## CLI Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-addr` | `:8080` | HTTP listen address |
| `-supabase-url` | `""` | Supabase project URL |
| `-supabase-key` | `""` | Supabase anon key |
| `-turso` | `false` | Use Turso remote database |
| `-db` | `logs.db` | Database file path |
| `-db-token` | `""` | Turso auth token |
| `-uploads` | `uploads` | Upload directory |
| `-max-upload` | `1073741824` | Max upload size (bytes) |
| `-debug` | `false` | Enable debug logging |

## Development

Build from source:

```bash
go build -o log-server.exe
```

## License

MIT
