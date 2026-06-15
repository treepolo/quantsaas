import { useSystemStatusStore } from "../../stores/systemStatusStore";

export type AppFeature = "dashboard" | "strategies" | "agents" | "risk" | "backtesting" | "settings";

const roleDefaults: Record<string, AppFeature[]> = {
  saas: ["dashboard", "agents", "settings"],
  lab: ["dashboard", "strategies", "agents", "risk", "backtesting", "settings"],
  dev: ["dashboard", "strategies", "agents", "risk", "backtesting", "settings"]
};

export function getEnabledFeatures() {
  const status = useSystemStatusStore.getState().status;
  if (status.enabled_features?.length) return status.enabled_features;
  return roleDefaults[status.app_role ?? "dev"] ?? roleDefaults.dev;
}

export function hasFeature(feature: AppFeature) {
  return getEnabledFeatures().includes(feature);
}
