import axios from "axios";

// guestApi is the axios client for the no-login magic-link surface. It carries
// NO Authorization header and NO auth-store/refresh interceptor — the session
// lives entirely in the HttpOnly cookies set by /api/v1/guest/redeem, so every
// request must send credentials (cookies). Same-origin, so CORS doesn't apply.
//
// On 401/403 the page surfaces "link expired / opened elsewhere" rather than
// bouncing to /login (there is no login here).
const guestApi = axios.create({
  baseURL: "/api/v1/guest",
  headers: { "Content-Type": "application/json" },
  withCredentials: true,
});

export default guestApi;
