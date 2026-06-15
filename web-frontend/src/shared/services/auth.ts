import type { AuthUser } from "../../stores/authStore";
import { apiFetch } from "./client";

export type LoginResponse = {
  token: string;
  user: AuthUser;
};

export const authApi = {
  login(email: string, password: string) {
    return apiFetch<LoginResponse>("/auth/login", {
      method: "POST",
      skipAuth: true,
      body: JSON.stringify({ email, password })
    });
  },
  register(email: string, password: string) {
    return apiFetch<{ id: number; email: string }>("/auth/register", {
      method: "POST",
      skipAuth: true,
      body: JSON.stringify({ email, password })
    });
  },
  me() {
    return apiFetch<AuthUser>("/auth/me");
  }
};
