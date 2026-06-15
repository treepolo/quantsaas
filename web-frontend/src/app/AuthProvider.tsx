import { createContext, useCallback, useContext, useEffect, type ReactNode } from "react";
import { authApi } from "../shared/services/auth";
import { useAuthStore, type AuthUser } from "../stores/authStore";

type AuthContextValue = {
  user: AuthUser | null;
  loading: boolean;
  login: (email: string, password: string) => Promise<void>;
  register: (email: string, password: string) => Promise<void>;
  logout: () => void;
};

const AuthContext = createContext<AuthContextValue | null>(null);

export function AuthProvider({ children }: { children: ReactNode }) {
  const token = useAuthStore((state) => state.token);
  const user = useAuthStore((state) => state.user);
  const loading = useAuthStore((state) => state.loading);
  const initialized = useAuthStore((state) => state.initialized);
  const setLoading = useAuthStore((state) => state.setLoading);
  const setInitialized = useAuthStore((state) => state.setInitialized);
  const loginWithToken = useAuthStore((state) => state.loginWithToken);
  const setUser = useAuthStore((state) => state.setUser);
  const storeLogout = useAuthStore((state) => state.logout);

  useEffect(() => {
    let cancelled = false;
    async function restore() {
      if (!token) {
        setLoading(false);
        setInitialized(true);
        return;
      }
      try {
        const profile = await authApi.me();
        if (!cancelled) {
          setUser(profile);
          setLoading(false);
          setInitialized(true);
        }
      } catch {
        if (!cancelled) {
          storeLogout();
        }
      }
    }

    if (!initialized) {
      void restore();
    }
    return () => {
      cancelled = true;
    };
  }, [initialized, setInitialized, setLoading, setUser, storeLogout, token]);

  const login = useCallback(
    async (email: string, password: string) => {
      setLoading(true);
      try {
        const response = await authApi.login(email, password);
        loginWithToken(response.token, response.user);
      } finally {
        setLoading(false);
      }
    },
    [loginWithToken, setLoading]
  );

  const register = useCallback(
    async (email: string, password: string) => {
      setLoading(true);
      try {
        await authApi.register(email, password);
        const response = await authApi.login(email, password);
        loginWithToken(response.token, response.user);
      } finally {
        setLoading(false);
      }
    },
    [loginWithToken, setLoading]
  );

  const logout = useCallback(() => {
    storeLogout();
  }, [storeLogout]);

  return <AuthContext.Provider value={{ user, loading, login, register, logout }}>{children}</AuthContext.Provider>;
}

export function useAuth() {
  const context = useContext(AuthContext);
  if (!context) {
    throw new Error("useAuth must be used inside AuthProvider");
  }
  return context;
}
