# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What This Is

A self-hosted URL shortener written in Go. It produces short codes for long URLs with features like custom aliases, multiple redirect types (HTTP 302, meta refresh, JS), OpenGraph metadata, password protection, and QR code generation. The web UI and all static assets are embedded directly in the binary.

## Build & Run

```bash
# Build
go build -o gourl .

# Run (with defaults: port 80, urls.db, localhost)
./gourl

# Common env vars
PORT=:8080 UI_HOST=http://links.localhost BASE_URL=http://localhost ./gourl
DB_FILE=/path/to/urls.db ./gourl
```

Environment variables (all optional, have defaults):
- `PORT` — listen address (default `:80`)
- `DB_FILE` — SQLite path (default `urls.db`)
- `BASE_URL` — public short URL base (default `http://localhost`)
- `UI_HOST` — web UI host (default `http://links.localhost`)
- `INTERNAL_HOST` — internal redirect host (default `http://go`)
- `ALIAS_HOST` — optional alternate public domain
- `PUBLIC_API_HOST` — optional dedicated API endpoint host

## Tests & Lint

No test files exist in this repository. No lint configuration exists — use `go vet ./...` and `gofmt` manually.

## Architecture

All Go code is in a single `main` package across 4 files:

- **`main.go`** — entry point: initializes DB, loads settings, starts HTTP server
- **`config.go`** — thread-safe `appConfig` struct (RWMutex) managing hostnames; settings are persisted to the DB via `loadSettings()`/`saveSetting()`
- **`db.go`** — SQLite schema (5 migrations, auto-applied on startup), CRUD for `urls` and `settings` tables
- **`handlers.go`** — all HTTP logic: `mainHandler` routes by `Host` header to one of four sub-routers

### Host-Based Routing

Requests are routed by the `Host` header (with `X-Forwarded-Host` fallback):

| Host | Router | Purpose |
|------|--------|---------|
| `UI_HOST` | `uiRouter` | Full web UI + all API endpoints |
| `INTERNAL_HOST` | `internalRouter` | Internal redirects + full API |
| `BASE_URL` / `ALIAS_HOST` | `publicRouter` | Public redirects only (`/{code}`) |
| `PUBLIC_API_HOST` | `publicAPIRouter` | `/pass/{code}` and `/qr/{code}` only |

Unknown hosts return 421.

### Data Model

`urls` table columns: `code`, `long_url`, `public_enabled`, `internal_enabled`, `created_at`, `redirect_type`, `og_title`, `og_description`, `og_image`, `password_hash`, `description`

`settings` table: key/value pairs for hostname configuration (mirrors env vars, overrides them at runtime).

Short codes are 6 characters from the charset `abcdefghijkmnpqrstuvwxyz23456789` (no ambiguous chars). Custom codes: 1–32 chars, alphanumeric plus `-` and `_`.

### Static Assets

`static/index.html`, `static/app.js`, `static/style.css` are embedded via `//go:embed` and served from memory. `index.html` uses Go `html/template` syntax for injecting hostname values server-side.

### Docker / CI

The GitHub Actions workflow (`.github/workflows/docker.yml`) cross-compiles for `linux/amd64` and `linux/arm64` with `CGO_ENABLED=0` and produces a `FROM scratch` image. The binary uses `modernc.org/sqlite` (pure Go SQLite — no CGo required).
