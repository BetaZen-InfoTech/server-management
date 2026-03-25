import axios from "axios";
import { useAuthStore } from "@/store/auth";
import { setAuthToken } from "@serverpanel/api-client";

const api = axios.create({
  baseURL: "/api/v1/whm",
  headers: { "Content-Type": "application/json" },
});

api.interceptors.request.use((config) => {
  const token = useAuthStore.getState().accessToken;
  if (token) config.headers.Authorization = `Bearer ${token}`;
  return config;
});

api.interceptors.response.use(
  (res) => res,
  async (error) => {
    const original = error.config;
    if (error.response?.status === 401 && !original._retry) {
      original._retry = true;
      const refreshToken = useAuthStore.getState().refreshToken
        || localStorage.getItem("refresh_token");
      if (refreshToken) {
        try {
          const { data } = await axios.post("/api/v1/auth/refresh", {
            refresh_token: refreshToken,
          });
          const newToken = data.data.access_token;
          const newRefresh = data.data.refresh_token;
          useAuthStore.getState().setAuth(
            useAuthStore.getState().user!,
            newToken,
            newRefresh
          );
          setAuthToken(newToken);
          original.headers.Authorization = `Bearer ${newToken}`;
          return api(original);
        } catch {
          // Refresh failed — force logout
        }
      }
      useAuthStore.getState().logout();
      window.location.href = "/whm/login";
    }
    return Promise.reject(error);
  }
);

export default api;
