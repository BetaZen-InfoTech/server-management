import { create } from "zustand";
import { persist } from "zustand/middleware";
import { setAuthToken, clearAuthToken } from "@serverpanel/api-client";

interface User {
  id: string;
  username: string;
  email: string;
  name: string;
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
  hasPermission: (perm: string) => boolean;
}

export const useAuthStore = create<AuthState>()(
  persist(
    (set, get) => ({
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
      hasPermission: (perm) => {
        const { user } = get();
        return user?.permissions?.includes(perm) ?? false;
      },
    }),
    { name: "whm-auth" }
  )
);
