# CLAUDE.md — Betazen Server Panel (WHM/cPanel Management)

## Project Overview

Betazen Server Panel is a modern, self-hosted WHM/cPanel-style server management platform by **BetaZen InfoTech**. It serves two SPAs from a single domain:

- **`/whm/*`** — platform-owner (`vendor_owner`) panel.
- **`/user-panel/*`** — vendor / team / customer panel (`vendor_admin`, `vendor_staff`, `developer`, `support`, `customer`). Formerly `/cpanel/*`; the old path still 301-redirects here for one release.

Login is strictly split: the WHM login rejects non-owners, and the User Panel login rejects `vendor_owner`. The server-side root redirect (`GET /`) sends each role to its correct surface. The API mirrors this: `/api/v1/whm/*` (owner + any staff with explicit perms) vs `/api/v1/cpanel/*` (vendors + their team + customers).

## Architecture

```
┌─────────────────────────────────────────────┐
│  Single Domain (panel.betazeninfotech.com)  │
│  ┌──────────────┐  ┌──────────────────────┐ │
│  │ /whm/*       │  │ /user-panel/*        │ │
│  │ Owner only   │  │ Vendors / team /     │ │
│  │              │  │ customers            │ │
│  └──────┬───────┘  └──────────┬───────────┘ │
│         └──────────┬──────────┘              │
│            Go Fiber API Server               │
│            (JWT + RBAC Auth)                 │
│                    │                         │
│              MongoDB 8.0+                    │
└─────────────────────────────────────────────┘
         │ mTLS (port 8443)
         ▼
┌─────────────────┐
│  Agent Daemon   │  ← runs on each managed VPS
│  (lightweight)  │
└─────────────────┘
```

## Tech Stack

| Layer       | Technology                                      |
| ----------- | ----------------------------------------------- |
| Backend     | Go 1.22, Fiber v2, MongoDB driver               |
| Frontend    | React 18, TypeScript 5, Vite 5, Tailwind CSS 3  |
| Monorepo    | Turbo 2.8.10 (npm workspaces)                   |
| State       | Zustand                                          |
| Auth        | JWT (access + refresh tokens), RBAC (5 roles)   |
| Database    | MongoDB 8.0+ (local dev) / Atlas (production)   |
| Agent Comm  | mTLS on port 8443                                |
| CI/CD       | GitHub Actions → VPS deploy                      |
| Containers  | Docker Compose (dev), single binary (prod)       |

## Project Structure

```
whm-cPanel-management/
├── backend/                    # Go backend
│   ├── cmd/
│   │   ├── server/             # Main API server entry
│   │   ├── agent/              # VPS agent daemon
│   │   └── seed/               # Database seeder
│   ├── internal/
│   │   ├── config/             # Env-based configuration
│   │   ├── database/           # MongoDB connection
│   │   ├── handlers/           # HTTP handlers (25+)
│   │   ├── middleware/         # Auth, CORS, rate limit
│   │   ├── models/             # Data models (17+)
│   │   ├── routes/             # Route definitions
│   │   └── services/           # Business logic (25+)
│   ├── pkg/                    # Shared utilities
│   │   ├── jwt/                # Token generation/validation
│   │   ├── logger/             # Zerolog setup
│   │   ├── password/           # Bcrypt hashing
│   │   ├── response/           # Standardized API responses
│   │   └── validator/          # Request validation
│   ├── go.mod / go.sum
│   └── Dockerfile
├── frontend/                   # React monorepo
│   ├── apps/
│   │   ├── whm/                # WHM owner panel (React SPA, served at /whm/*)
│   │   └── cpanel/             # User Panel SPA (served at /user-panel/*; dir name is legacy)
│   ├── packages/
│   │   ├── api-client/         # Shared Axios API client
│   │   ├── types/              # Shared TypeScript types
│   │   ├── ui/                 # Shared UI components
│   │   └── tailwind-config/    # Shared Tailwind preset
│   ├── turbo.json
│   └── package.json
├── .github/workflows/deploy.yml
├── docker-compose.yml
├── Makefile
├── .env.example                # Template for environment vars
├── .env.local                  # Local dev overrides
├── .env.dev                    # Development environment
└── .env.prod                   # Production environment
```

## Common Commands

```bash
# Development
make dev                 # Start backend (Air) + frontend (Vite) concurrently
make dev-backend         # Backend only with hot-reload
make dev-frontend        # Frontend only (Turbo dev servers)

# Build
make build               # Build everything for production
make build-backend       # Go binaries: server + agent
make build-frontend      # Frontend SPAs via Turbo

# Docker
make docker-up           # Start all services
make docker-down         # Stop all services
make docker-build        # Build images

# Quality
make lint                # golangci-lint + turbo lint
make test                # go test + turbo test

# Setup
make setup               # go mod download + npm install
make clean               # Remove all build artifacts
```

## Key Conventions

### Backend (Go)

- **Framework:** Fiber v2 — Express-style routing
- **Pattern:** Handler → Service → Database (clean separation)
- **Auth:** JWT with access (15m) + refresh (7d) tokens; token fields use `snake_case` (`access_token`, `refresh_token`)
- **Response format:** All API responses use `pkg/response` helpers for consistency
- **Config:** All config loaded from env vars via `internal/config`
- **Logging:** Zerolog (structured JSON logging)
- **Validation:** go-playground/validator with struct tags
- **Error handling:** Services return errors, handlers translate to HTTP status codes

### Frontend (React/TypeScript)

- **Routing:** React Router v6 with path-based separation (`/whm/*`, `/user-panel/*` — `/cpanel/*` redirects to the latter)
- **State:** Zustand stores (e.g., `useAuthStore`)
- **API calls:** Centralized in `packages/api-client` using Axios
- **Styling:** Tailwind CSS with dark theme support
- **Icons:** Lucide React
- **Notifications:** React Hot Toast
- **Charts:** Recharts for data visualization

### API Routes

- `POST /api/auth/login` — Login (returns `access_token`, `refresh_token`)
- `POST /api/auth/refresh` — Refresh token
- `/api/v1/whm/*` — WHM endpoints (owner + any staff with the required perm; server-level ops like software/maintenance/resources.summary stay behind `server.manage`)
- `/api/v1/cpanel/*` — User Panel endpoints — allowlisted to `vendor_admin`, `vendor_staff`, `developer`, `support`, `customer`; tenant scoping is done in the service layer via `callerCtx(role, tenantID)`
- `/api/agent/*` — Agent communication (mTLS)

### Environment Files

- `.env` — **Gitignored** — actual secrets, never commit
- `.env.example` — Template with placeholder values (committed)
- `.env.local` — Local dev overrides (committed, no secrets)
- `.env.dev` — Development environment config (committed, no secrets)
- `.env.prod` — Production environment config (committed, no secrets)

## Important Notes

- Token field names use **snake_case** (`access_token`, not `accessToken`) — this was a critical bug fix
- Frontend apps are at `/whm` and `/user-panel` paths (`/cpanel` 301-redirects to `/user-panel`). The Go server serves both SPAs.
- Email is **globally unique** across every vendor, their team, and all customer accounts — enforced case-insensitively at the service layer plus a unique MongoDB index. Don't weaken this per-tenant; tenant identity is separate from email identity.
- Agent communication uses mTLS on port 8443 — never expose this publicly
- MongoDB auth uses `authSource=admin` — connection strings must include this
- The deploy workflow builds Linux binaries even though dev may be on Windows
- `frontend/package-lock.json` is committed for reproducible CI builds
