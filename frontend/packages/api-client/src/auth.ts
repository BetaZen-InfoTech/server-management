import { apiClient } from "./client";
import type { LoginRequest, LoginResponse } from "@serverpanel/types";

export const login = (data: LoginRequest) =>
  apiClient.post<{ success: boolean; data: LoginResponse }>("/api/v1/auth/login", data);

export const refresh = (refreshToken: string) =>
  apiClient.post("/api/v1/auth/refresh", { refresh_token: refreshToken });

export const logout = (refreshToken: string) =>
  apiClient.post("/api/v1/auth/logout", { refresh_token: refreshToken });

export const forgotPassword = (email: string) =>
  apiClient.post("/api/v1/auth/forgot-password", { email });

export const resetPassword = (token: string, newPassword: string) =>
  apiClient.post("/api/v1/auth/reset-password", { token, new_password: newPassword });

export const enable2FA = () => apiClient.post("/api/v1/auth/2fa/enable");

export const verify2FA = (code: string) => apiClient.post("/api/v1/auth/2fa/verify", { code });

export const disable2FA = () => apiClient.post("/api/v1/auth/2fa/disable");
