import { create } from "zustand";
import { persist } from "zustand/middleware";
import { setAuthToken, clearAuthToken } from "@serverpanel/api-client";

interface User {
  id: string;
  email: string;
  name: string;
  // Linux username — used by the File Manager / Terminal / DB prefix to
  // anchor every per-user resource path (/home/<username>, <username>_db,
  // etc.). Backend always returns this on /auth/login; older saved
  // sessions may have it absent so the field is optional.
  username?: string;
  role: string;
  permissions: string[];
}

interface AuthState {
  user: User | null;
  accessToken: string | null;
  refreshToken: string | null;
  isAuthenticated: boolean;
  setAuth: (user: User, accessToken: string, refreshToken: string) => void;
  logout: () => void;
}

export const useAuthStore = create<AuthState>()(
  persist(
    (set) => ({
      user: null,
      accessToken: null,
      refreshToken: null,
      isAuthenticated: false,
      setAuth: (user, accessToken, refreshToken) => {
        setAuthToken(accessToken);
        localStorage.setItem("refresh_token", refreshToken);
        set({ user, accessToken, refreshToken, isAuthenticated: true });
      },
      logout: () => {
        clearAuthToken();
        set({ user: null, accessToken: null, refreshToken: null, isAuthenticated: false });
      },
    }),
    { name: "cpanel-auth" }
  )
);
