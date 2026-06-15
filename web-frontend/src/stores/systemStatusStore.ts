import { create } from "zustand";
import type { AppFeature } from "../shared/config/features";

export type EngineStatus = "running" | "paused" | "halted";

export type SystemStatus = {
  api_configured: boolean;
  api_connected: boolean;
  engine_status: EngineStatus;
  requires_reconcile?: boolean;
  app_role?: "saas" | "lab" | "dev";
  enabled_features?: AppFeature[];
  last_heartbeat_at?: string;
  agent_version?: string;
};

type SystemStatusState = {
  status: SystemStatus;
  setStatus: (status: Partial<SystemStatus>) => void;
};

export const defaultSystemStatus: SystemStatus = {
  api_configured: false,
  api_connected: false,
  engine_status: "paused",
  app_role: "dev",
  enabled_features: ["dashboard", "strategies", "agents", "risk", "backtesting", "settings"]
};

export const useSystemStatusStore = create<SystemStatusState>((set) => ({
  status: defaultSystemStatus,
  setStatus: (status) => set((state) => ({ status: { ...state.status, ...status } }))
}));
