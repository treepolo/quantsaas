import { useAuthStore } from "../../stores/authStore";

export class ApiRequestError extends Error {
  status: number;

  constructor(status: number, message: string) {
    super(message);
    this.name = "ApiRequestError";
    this.status = status;
  }
}

type ApiOptions = RequestInit & {
  skipAuth?: boolean;
};

const apiBase = "/api/v1";

async function parseResponse(response: Response) {
  const text = await response.text();
  if (!text) return undefined;
  try {
    return JSON.parse(text);
  } catch {
    return text;
  }
}

export async function apiFetch<T>(path: string, options: ApiOptions = {}): Promise<T> {
  const { skipAuth, headers, ...init } = options;
  const token = useAuthStore.getState().token;
  const response = await fetch(`${apiBase}${path}`, {
    ...init,
    headers: {
      "Content-Type": "application/json",
      ...(token && !skipAuth ? { Authorization: `Bearer ${token}` } : {}),
      ...headers
    }
  }).catch((error: Error) => {
    throw new ApiRequestError(0, error.message || "Network error");
  });

  const payload = await parseResponse(response);
  if (!response.ok) {
    const message =
      payload && typeof payload === "object" && "error" in payload
        ? String((payload as { error: unknown }).error)
        : response.statusText;
    if (response.status === 401 && !skipAuth) {
      useAuthStore.getState().logout();
      if (!window.location.pathname.startsWith("/login")) {
        window.location.assign("/login");
      }
    }
    throw new ApiRequestError(response.status, message);
  }
  return payload as T;
}
