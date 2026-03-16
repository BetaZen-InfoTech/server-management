# Add Feature

Guide for adding a new feature to ServerPanel following the established patterns.

## Backend (Go)
1. Create model in `backend/internal/models/`
2. Create service in `backend/internal/services/`
3. Create handler in `backend/internal/handlers/`
4. Register routes in `backend/internal/routes/` (whm_routes.go or cpanel_routes.go)
5. Follow the Handler → Service → Database pattern

## Frontend (React)
1. Create page component in `frontend/apps/whm/src/pages/` or `frontend/apps/cpanel/src/pages/`
2. Add API functions in `frontend/packages/api-client/`
3. Add TypeScript types in `frontend/packages/types/`
4. Register route in `App.tsx`
5. Use Zustand for state, Tailwind for styling, Lucide for icons
