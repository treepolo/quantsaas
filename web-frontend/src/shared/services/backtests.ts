import { apiFetch } from "./client";
import type { EquitySnapshot } from "./dashboard";

export type BacktestResult = {
  id: number;
  status: "running" | "completed" | "failed";
  strategy_id?: string;
  symbol?: string;
  instrument_id?: string;
  data_source?: string;
  execution_mode?: string;
  interval?: string;
  source?: string;
  total_return: number;
  alpha: number;
  max_drawdown: number;
  final_equity: number;
  benchmark: number;
  benchmark_return?: number;
  benchmark_max_drawdown?: number;
  benchmark_final_equity?: number;
  fee_rate?: number;
  spread_rate?: number;
  rebalance_threshold?: number;
  nav: EquitySnapshot[];
  windows: Record<string, number>;
};

export const backtestsApi = {
  create(input: Record<string, unknown>) {
    return apiFetch<BacktestResult>("/backtests", {
      method: "POST",
      body: JSON.stringify(input)
    });
  },
  get(id: number) {
    return apiFetch<BacktestResult>(`/backtests/${id}`);
  }
};
