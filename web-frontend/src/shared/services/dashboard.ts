import { apiFetch } from "./client";

export type EquitySnapshot = {
  time: string;
  price?: number;
  total_assets: number;
  benchmark?: number;
  strategy_change_pct?: number;
  benchmark_change_pct?: number;
  practical_target_weight?: number;
  practical_target_weight_change?: number;
  model_target_weight?: number;
  model_target_weight_change?: number;
  empty_reference_target_weight?: number;
  empty_reference_target_weight_change?: number;
};

export type PortfolioSummary = {
  total_assets: number;
  long_term: number;
  active_position: number;
  available_funds: number;
  sealed_assets: number;
  last_decision_at?: string | null;
  decisions_count: number;
  monthly_trades: number;
  first_run_at?: string | null;
};

export const dashboardApi = {
  snapshots(instanceId: number, range = "30d") {
    return apiFetch<EquitySnapshot[]>(`/dashboard/equity-snapshots?instance_id=${instanceId}&range=${range}`);
  },
  portfolio(instanceId: number) {
    return apiFetch<PortfolioSummary>(`/dashboard/portfolio?instance_id=${instanceId}`);
  }
};
