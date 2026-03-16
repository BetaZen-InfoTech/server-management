# Fix API

Diagnose and fix API integration issues between frontend and backend.

## Common Issues
1. **Route mismatch** — Frontend calling wrong endpoint path
2. **Token field names** — Must use snake_case (`access_token`, `refresh_token`)
3. **CORS errors** — Check middleware configuration
4. **Auth failures** — Verify JWT token format and expiry
5. **MongoDB connection** — Check `authSource=admin` in connection string

## Debugging Steps
1. Check backend route definitions in `backend/internal/routes/`
2. Check frontend API calls in `frontend/packages/api-client/`
3. Verify request/response field names match between frontend and backend
4. Check middleware chain in `backend/internal/middleware/`
