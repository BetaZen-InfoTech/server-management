# Claude Project Memory — ServerPanel

## Critical Bug Fixes (Don't Repeat)

- **Token field names**: JWT response uses `snake_case` (`access_token`, `refresh_token`), NOT `camelCase`. This caused a critical login bug (commit `0ec1569`).
- **Route mismatches**: Frontend API paths must exactly match backend route definitions. Fixed across all WHM pages (commit `fb3490c`).
- **MongoDB authSource**: Connection strings MUST include `?authSource=admin` or auth will silently fail.

## Architecture Decisions

- Single domain serves both WHM (`/whm/*`) and cPanel (`/cpanel/*`) via Go Fiber
- Agent daemon runs on managed VPS instances, communicates via mTLS on port 8443
- Frontend is a Turbo monorepo with shared packages (api-client, types, ui, tailwind-config)
- State management: Zustand (not Redux)
- Styling: Tailwind CSS with dark theme

## Patterns to Follow

### Backend (Go)
- Handler → Service → Database (clean separation)
- All handlers in `backend/internal/handlers/`
- All services in `backend/internal/services/`
- Routes split by panel: `whm_routes.go`, `cpanel_routes.go`, `auth_routes.go`, `agent_routes.go`
- Use `pkg/response` for all API responses
- Use `pkg/validator` for request validation

### Frontend (React/TS)
- Pages in `apps/whm/src/pages/` or `apps/cpanel/src/pages/`
- Shared API client in `packages/api-client/`
- Shared types in `packages/types/`
- Icons: Lucide React (reference: lucide.dev)
- Notifications: React Hot Toast
- Charts: Recharts

## Environment Strategy

- `.env` → gitignored, contains actual secrets
- `.env.example` → committed, template with placeholders
- `.env.local` → committed, local dev config (no secrets)
- `.env.dev` → committed, dev environment config (no secrets)
- `.env.prod` → committed, prod environment config (no secrets)

## Dev Commands

```bash
make dev              # Full dev (backend + frontend)
make build            # Production build
make docker-up        # Docker Compose
make test             # All tests
make lint             # All linters
```

## Known Issues / Notes

- Deploy workflow (`deploy.yml`) builds Linux binaries — dev may be on Windows
- `frontend/package-lock.json` is committed for reproducible CI
- WHM app pages: Dashboard, Servers, Domains, DNS, Databases, Email, SSL, Files, Backups, Firewall, Monitoring, SSH Keys, Cron Jobs, WordPress, Processes, Software, Users, Audit Log, Settings
