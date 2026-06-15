import { create } from "zustand";

export type AuthUser = {
  id?: number;
  email: string;
  role: string;
};

type AuthState = {
  token: string | null;
  user: AuthUser | null;
  loading: boolean;
  initialized: boolean;
  setLoading: (loading: boolean) => void;
  setInitialized: (initialized: boolean) => void;
  loginWithToken: (token: string, user: AuthUser) => void;
  setUser: (user: AuthUser | null) => void;
  logout: () => void;
};

const tokenKey = "qs_jwt";

export const useAuthStore = create<AuthState>((set) => ({
  token: window.localStorage.getItem(tokenKey),
  user: null,
  loading: true,
  initialized: false,
  setLoading: (loading) => set({ loading }),
  setInitialized: (initialized) => set({ initialized }),
  loginWithToken: (token, user) => {
    window.localStorage.setItem(tokenKey, token);
    set({ token, user, loading: false, initialized: true });
  },
  setUser: (user) => set({ user }),
  logout: () => {
    window.localStorage.removeItem(tokenKey);
    set({ token: null, user: null, loading: false, initialized: true });
  }
}));
