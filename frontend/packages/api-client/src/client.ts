import axios from "axios";

const BASE_URL = import.meta.env.VITE_API_URL || "";

export const apiClient = axios.create({
  baseURL: BASE_URL,
  headers: { "Content-Type": "application/json" },
  timeout: 30000,
});

export function setAuthToken(token: string) {
  apiClient.defaults.headers.common["Authorization"] = `Bearer ${token}`;
}

export function clearAuthToken() {
  delete apiClient.defaults.headers.common["Authorization"];
}

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
          localStorage.setItem("access_token", newToken);
          localStorage.setItem("refresh_token", data.data.refresh_token);
          setAuthToken(newToken);
          error.config.headers["Authorization"] = `Bearer ${newToken}`;
          return apiClient(error.config);
        } catch {
          localStorage.clear();
          window.location.href = "/whm/login";
        }
      }
    }
    return Promise.reject(error);
  }
);
