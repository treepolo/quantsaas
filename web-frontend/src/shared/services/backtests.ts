import { apiFetch } from "./client";
import type { EquitySnapshot } from "./dashboard";

export type BacktestResult = {
  id: number;
  status: "pending" | "running" | "completed" | "failed";
  total_return: number;
  alpha: number;
  max_drawdown: number;
  sharpe?: number;
  nav: EquitySnapshot[];
  windows: Record<string, number>;
};

export const backtestsApi = {
  create(input: Record<string, unknown>) {
    return apiFetch<{ id: number }>("/backtests", {
      method: "POST",
      body: JSON.stringify(input)
    });
  },
  get(id: number) {
    return apiFetch<BacktestResult>(`/backtests/${id}`);
  }
};
