import type { SystemStatus } from "../../stores/systemStatusStore";
import { apiFetch } from "./client";

export const systemApi = {
  status() {
    return apiFetch<SystemStatus>("/system/status");
  }
};
