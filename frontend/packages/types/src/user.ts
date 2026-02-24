export interface User {
  id: string;
  email: string;
  name: string;
  role: UserRole;
  permissions: string[];
  domains: string[];
  is_active: boolean;
  two_factor_enabled: boolean;
  last_login: string | null;
  created_at: string;
  updated_at: string;
}

export type UserRole = "vendor_owner" | "vendor_admin" | "developer" | "support" | "customer";

export interface LoginRequest {
  email: string;
  password: string;
  totp_code?: string;
}

export interface LoginResponse {
  access_token: string;
  refresh_token: string;
  expires_in: number;
  token_type: string;
  user: User;
}

export interface CreateUserRequest {
  email: string;
  password: string;
  name: string;
  role: UserRole;
  permissions?: string[];
  domains?: string[];
  notify?: boolean;
}
