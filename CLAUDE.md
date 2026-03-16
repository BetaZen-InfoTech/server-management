# CLAUDE.md — ServerPanel (WHM/cPanel Management)

## Project Overview

ServerPanel is a modern, self-hosted WHM/cPanel-style server management platform by **BetaZen InfoTech**. It provides a vendor admin panel (WHM) and customer self-service panel (cPanel) served from a single domain with path-based routing (`/whm/*`, `/cpanel/*`) and an agent-based architecture for secure VPS management.

## Architecture

```
┌─────────────────────────────────────────────┐
│  Single Domain (panel.betazeninfotech.com)  │
│  ┌──────────────┐  ┌─────────────────────┐  │
│  │ /whm/*       │  │ /cpanel/*           │  │
│  │ Vendor Panel │  │ Customer Panel      │  │
│  └──────┬───────┘  └──────────┬──────────┘  │
│         └──────────┬──────────┘              │
│            Go Fiber API Server               │
│            (JWT + RBAC Auth)                 │
│                    │                         │
│              MongoDB 7.0+                    │
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
| Database    | MongoDB 7.0+ (local dev) / Atlas (production)   |
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
│   │   ├── whm/                # WHM vendor panel (React SPA)
│   │   └── cpanel/             # cPanel customer panel (React SPA)
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

- **Routing:** React Router v6 with path-based separation (`/whm/*`, `/cpanel/*`)
- **State:** Zustand stores (e.g., `useAuthStore`)
- **API calls:** Centralized in `packages/api-client` using Axios
- **Styling:** Tailwind CSS with dark theme support
- **Icons:** Lucide React
- **Notifications:** React Hot Toast
- **Charts:** Recharts for data visualization

### API Routes

- `POST /api/auth/login` — Login (returns `access_token`, `refresh_token`)
- `POST /api/auth/refresh` — Refresh token
- `/api/whm/*` — WHM vendor panel endpoints (admin-only)
- `/api/cpanel/*` — cPanel customer endpoints (user-scoped)
- `/api/agent/*` — Agent communication (mTLS)

### Environment Files

- `.env` — **Gitignored** — actual secrets, never commit
- `.env.example` — Template with placeholder values (committed)
- `.env.local` — Local dev overrides (committed, no secrets)
- `.env.dev` — Development environment config (committed, no secrets)
- `.env.prod` — Production environment config (committed, no secrets)

## Important Notes

- Token field names use **snake_case** (`access_token`, not `accessToken`) — this was a critical bug fix
- Frontend apps are at `/whm` and `/cpanel` paths — the Go server serves both SPAs
- Agent communication uses mTLS on port 8443 — never expose this publicly
- MongoDB auth uses `authSource=admin` — connection strings must include this
- The deploy workflow builds Linux binaries even though dev may be on Windows
- `frontend/package-lock.json` is committed for reproducible CI builds
