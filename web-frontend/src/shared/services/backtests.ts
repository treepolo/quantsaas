import { apiFetch } from "./client";
import type { EquitySnapshot } from "./dashboard";

export type BacktestResult = {
  id: number;
  status: "running" | "completed" | "failed" | "cancelled";
  backtest_result_id?: number;
  backtest_key?: string;
  result_version?: string;
  result_content_hash?: string;
  result_status?: "pending" | "running" | "completed" | "failed" | "cancelled" | "invalidated" | "archived";
  reused_result?: boolean;
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
  fee_cost?: number;
  slippage_cost?: number;
  total_execution_cost?: number;
  rebalance_threshold?: number;
  force_full_threshold?: number;
  force_empty_threshold?: number;
  position_structure?: "dual_layer" | "floating_only" | "market_baseline";
  trade_count?: number;
  long_term_filter_enabled?: boolean;
  long_term_filter_months?: number;
  long_term_filter_version?: string;
  practical_total_return?: number;
  practical_max_drawdown?: number;
  practical_final_equity?: number;
  practical_trade_count?: number;
  w_mean?: number;
  w_momentum?: number;
  w_breakout?: number;
  nav: EquitySnapshot[];
  windows: Record<string, number>;
};

export type StandardBacktestResult = {
  id: number;
  status: "pending" | "running" | "completed" | "failed" | "cancelled" | "invalidated" | "archived";
  backtest_key: string;
  result_version: string;
  result_content_hash?: string;
  spec: Record<string, unknown>;
  summary?: Record<string, unknown>;
  path_manifest?: Record<string, unknown>;
  backtest_run_ids: number[];
  created_at: string;
  completed_at?: string;
};

export type StandardBacktestPathBlock = {
  result_id: number;
  block_index: number;
  content_hash: string;
  block: Record<string, unknown>;
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
  },
  getStandardResult(id: number) {
    return apiFetch<StandardBacktestResult>(`/backtest-results/${id}`);
  },
  getStandardPathBlock(id: number, blockIndex = 0) {
    return apiFetch<StandardBacktestPathBlock>(`/backtest-results/${id}/path?block_index=${blockIndex}`);
  },
  verifyStandardResult(id: number) {
    return apiFetch<Record<string, unknown>>(`/backtest-results/${id}/integrity`);
  }
};
