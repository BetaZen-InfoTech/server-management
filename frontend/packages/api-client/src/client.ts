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

// Request interceptor to attach token from localStorage
apiClient.interceptors.request.use((config) => {
  if (!config.headers["Authorization"]) {
    const token = localStorage.getItem("access_token");
    if (token) {
      config.headers["Authorization"] = `Bearer ${token}`;
    }
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
