import axios from "axios";
import { useAuthStore } from "@/store/auth";

const api = axios.create({
  baseURL: "/api/v1/cpanel",
  headers: { "Content-Type": "application/json" },
});

api.interceptors.request.use((config) => {
  const token = useAuthStore.getState().accessToken;
  if (token) config.headers.Authorization = `Bearer ${token}`;
  // FormData fix (v3.1.46) — see comment on the WHM client.ts +
  // @serverpanel/api-client for the full explanation. Same bug
  // applied here so cpanel mailbox / forwarder / file uploads also
  // auto-set the multipart boundary instead of inheriting the
  // application/json default.
  if (typeof FormData !== "undefined" && config.data instanceof FormData) {
    if (config.headers) {
      delete (config.headers as Record<string, unknown>)["Content-Type"];
      delete (config.headers as Record<string, unknown>)["content-type"];
    }
  }
  return config;
});

// Same in-flight refresh dedupe used by the WHM client. Without this,
// every 401 fired its own /auth/refresh and the second one always lost
// the rotated-token race → forced logout. The cPanel page also used to
// just log the customer out on every 401 without attempting a refresh
// at all, so they were re-typing their password every 15 minutes.
let refreshInFlight: Promise<string | null> | null = null;

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
    if (typeof original.url === "string" && original.url.includes("/auth/refresh")) {
      useAuthStore.getState().logout();
      window.location.href = "/user-panel/login";
      return Promise.reject(error);
    }
    original._retry = true;
    if (!refreshInFlight) {
      refreshInFlight = performRefresh().finally(() => {
        setTimeout(() => { refreshInFlight = null; }, 0);
      });
    }
    const newToken = await refreshInFlight;
    if (!newToken) {
      useAuthStore.getState().logout();
      window.location.href = "/user-panel/login";
      return Promise.reject(error);
    }
    original.headers = original.headers || {};
    original.headers.Authorization = `Bearer ${newToken}`;
    return api(original);
  }
);

export default api;
