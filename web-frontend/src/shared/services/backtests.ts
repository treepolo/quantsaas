import { apiFetch } from "./client";
import type { EquitySnapshot } from "./dashboard";

export type BacktestResult = {
  id: number;
  status: "running" | "completed" | "failed";
  total_return: number;
  alpha: number;
  max_drawdown: number;
  final_equity: number;
  benchmark: number;
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
