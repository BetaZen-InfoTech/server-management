import axios from "axios";
import { useAuthStore } from "@/store/auth";
import { setAuthToken } from "@serverpanel/api-client";

// timeout: this instance previously had none, so axios used its default of
// 0 — "wait forever". A request that the backend never finishes answering
// therefore sat in the browser until something upstream (nginx, a corporate
// forward proxy, Cloudflare) gave up and returned a 504, which surfaced as
// a bare "Discovery failed" toast with no hint that the wait was the
// problem. 60s is above every normal panel call and below the ~60-100s
// budget typical upstream proxies enforce, so we now fail with our own
// diagnosable ECONNABORTED instead of somebody else's gateway error.
// Genuinely long calls pass a per-request override.
const api = axios.create({
  baseURL: "/api/v1/whm",
  headers: { "Content-Type": "application/json" },
  timeout: 60000,
});

api.interceptors.request.use((config) => {
  const token = useAuthStore.getState().accessToken;
  if (token) config.headers.Authorization = `Bearer ${token}`;
  // FormData fix (v3.1.46): the axios instance defaults to
  // `Content-Type: application/json`. That default also wins for
  // FormData bodies — the browser then sends a multipart body with
  // `Content-Type: application/json` and Fiber's parser refuses
  // the request with "file is required" because there's no
  // boundary to find each form field. Strip the default here so
  // axios + the browser auto-set the right multipart Content-Type
  // with the random boundary marker. See client.ts in
  // @serverpanel/api-client for the same fix on the shared
  // instance.
  if (typeof FormData !== "undefined" && config.data instanceof FormData) {
    if (config.headers) {
      delete (config.headers as Record<string, unknown>)["Content-Type"];
      delete (config.headers as Record<string, unknown>)["content-type"];
    }
  }
  return config;
});

// In-flight refresh dedupe + retry queue.
//
// Without this, every 401 fired its own /auth/refresh. The backend rotates
// the refresh token on each call (single-use), so when a dashboard load
// hit five concurrent 401s, only the first refresh succeeded — the other
// four sent an already-rotated token, got "invalid refresh token" back,
// and the interceptor logged the user out. That's the root cause of the
// "after some time my account logs out" symptom.
//
// We now keep a single Promise<string|null> for the in-flight refresh and
// have every other 401 await it. Subscribers get the new access token
// once it lands and replay their original request with that token.
let refreshInFlight: Promise<string | null> | null = null;

// Exported so non-axios transports can renew a token too. The WebSocket
// terminal needs this: it authenticates with the access token in a query
// parameter, and a WebSocket gets no 401 to hang a retry interceptor off,
// so it has to ask for a fresh token explicitly.
export async function refreshAccessToken(): Promise<string | null> {
  if (!refreshInFlight) {
    refreshInFlight = performRefresh().finally(() => {
      setTimeout(() => {
        refreshInFlight = null;
      }, 0);
    });
  }
  return refreshInFlight;
}

async function performRefresh(): Promise<string | null> {
  const refreshToken = useAuthStore.getState().refreshToken
    || localStorage.getItem("refresh_token");
  if (!refreshToken) return null;
  try {
    const { data } = await axios.post("/api/v1/auth/refresh", {
      refresh_token: refreshToken,
    });
    const newToken = data.data.access_token as string;
    const newRefresh = data.data.refresh_token as string;
    const user = useAuthStore.getState().user;
    if (user) {
      useAuthStore.getState().setAuth(user, newToken, newRefresh);
    }
    setAuthToken(newToken);
    return newToken;
  } catch {
    return null;
  }
}

api.interceptors.response.use(
  (res) => res,
  async (error) => {
    const original = error.config;
    if (!original || error.response?.status !== 401 || original._retry) {
      return Promise.reject(error);
    }
    // Skip the refresh dance for the refresh endpoint itself — otherwise
    // a 401 from /auth/refresh would loop back into another refresh attempt.
    if (typeof original.url === "string" && original.url.includes("/auth/refresh")) {
      useAuthStore.getState().logout();
      window.location.href = "/whm/login";
      return Promise.reject(error);
    }

    original._retry = true;

    // Coalesce: every concurrent 401 awaits the same in-flight refresh.
    if (!refreshInFlight) {
      refreshInFlight = performRefresh().finally(() => {
        // Release the slot AFTER the promise settles so the next batch
        // of 401s can start a fresh refresh — but not until this one is
        // observable to all current subscribers.
        setTimeout(() => { refreshInFlight = null; }, 0);
      });
    }
    const newToken = await refreshInFlight;
    if (!newToken) {
      useAuthStore.getState().logout();
      window.location.href = "/whm/login";
      return Promise.reject(error);
    }
    original.headers = original.headers || {};
    original.headers.Authorization = `Bearer ${newToken}`;
    return api(original);
  }
);

export default api;
