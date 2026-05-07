import axios from "axios";

const BASE_URL = import.meta.env.VITE_API_URL || "";

export const apiClient = axios.create({
  baseURL: BASE_URL,
  headers: { "Content-Type": "application/json" },
  timeout: 30000,
});

export function setAuthToken(token: string) {
  localStorage.setItem("access_token", token);
  apiClient.defaults.headers.common["Authorization"] = `Bearer ${token}`;
}

export function clearAuthToken() {
  localStorage.removeItem("access_token");
  localStorage.removeItem("refresh_token");
  delete apiClient.defaults.headers.common["Authorization"];
}

// Offline guard — short-circuit every request when navigator.onLine is
// false so the panel doesn't fire a doomed XHR that takes 30 s to time
// out, doesn't spin a loading state forever, and doesn't surface a
// generic "Network Error" toast that's indistinguishable from a real
// API outage. Throws an error shaped like an axios cancellation so
// existing per-request `.catch(err => err.message)` handlers keep
// working — `err.code === "ERR_OFFLINE"` is the discriminator a
// caller uses to render the offline-specific UI.
//
// Bypasses the check for the auth-refresh round-trip below so an
// ALREADY-IN-FLIGHT 401 → refresh → retry sequence doesn't spuriously
// fail when the user briefly disconnects mid-flow; the response
// interceptor handles its own offline case via the rejection.
apiClient.interceptors.request.use((config) => {
  if (!config.headers["Authorization"]) {
    const token = localStorage.getItem("access_token");
    if (token) {
      config.headers["Authorization"] = `Bearer ${token}`;
    }
  }
  if (typeof navigator !== "undefined" && navigator.onLine === false) {
    const err: any = new Error(
      "Network is offline. Reconnect and try again."
    );
    err.code = "ERR_OFFLINE";
    err.isOfflineError = true;
    err.config = config;
    // Throwing from a request interceptor is the supported way to
    // refuse a request — axios funnels it straight to the caller's
    // .catch without ever hitting the wire.
    throw err;
  }
  return config;
});

// Response interceptor for auto token refresh
apiClient.interceptors.response.use(
  (response) => response,
  async (error) => {
    if (error.response?.status === 401) {
      const refreshToken = localStorage.getItem("refresh_token");
      if (refreshToken && !error.config._retry) {
        error.config._retry = true;
        try {
          const { data } = await axios.post(`${BASE_URL}/api/v1/auth/refresh`, { refresh_token: refreshToken });
          const newToken = data.data.access_token;
          setAuthToken(newToken);
          localStorage.setItem("refresh_token", data.data.refresh_token);
          error.config.headers["Authorization"] = `Bearer ${newToken}`;
          return apiClient(error.config);
        } catch {
          clearAuthToken();
          window.location.href = "/whm/login";
        }
      }
    }
    return Promise.reject(error);
  }
);
