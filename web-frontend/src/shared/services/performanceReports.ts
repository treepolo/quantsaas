import { apiFetch } from "./client";

export type DistributionStats = {
  period: "daily" | "weekly" | "monthly";
  count: number;
  mean: number;
  median: number;
  std_dev: number;
  skewness: number;
  excess_kurtosis: number;
  minimum: number;
  maximum: number;
  quantiles: Record<string, number>;
  stats_version: string;
};

export type PerformanceSummary = {
  schema_version: string;
  analysis_version: string;
  aggregation_version: string;
  relative_performance: {
    final_nav_ratio: number;
    log_final_nav_ratio: number;
    strategy_no_cash_flow_annualized?: number;
    benchmark_no_cash_flow_annualized?: number;
    no_cash_flow_annualized_difference?: number;
    annualization_formula_version: string;
    annualization_uses_no_cash_flow_result: boolean;
  };
  distributions: Record<"daily" | "weekly" | "monthly", DistributionStats>;
  longest_underwater: {
    longest_days: number;
    longest_points: number;
    started_at_ms?: number;
    recovered_at_ms?: number;
    recovery_completed: boolean;
  };
  sortino: {
    value?: number;
    risk_free_annual_rate: number;
    periods_per_year: number;
    observation_count: number;
    formula_version: string;
    unavailable_reason?: string;
  };
  beta: {
    value?: number;
    observation_count: number;
    formula_version: string;
    unavailable_reason?: string;
  };
  exposure: {
    exposure_days_ratio: number;
    average_actual_exposure: number;
    exposure_adjusted_return?: number;
    exposure_adjusted_readable: boolean;
  };
};

export type PerformanceSettings = {
  schema_version: string;
  aggregation_version: string;
  statistics_version: string;
  sortino_version: string;
  beta_version: string;
  annualization_version: string;
  risk_free_annual_rate: number;
  histogram_bins: number;
  beta_benchmark?: {
    instrument_id: string;
    symbol: string;
    data_source: string;
    interval: string;
    start_time_ms: number;
    end_time_ms: number;
    dataset_version: string;
    dataset_hash: string;
  };
};

export type ChartManifest = {
  schema_version: string;
  block_count: number;
  blocks: Array<{ kind: string; schema_version: string; content_hash: string; point_count: number }>;
};

export type PerformanceReport = {
  id: number;
  status: "pending" | "running" | "completed" | "failed" | "cancelled" | "invalidated" | "archived";
  analysis_key: string;
  analysis_version: string;
  schema_version: string;
  settings_hash: string;
  settings: PerformanceSettings;
  content_hash?: string;
  backtest_result_id: number;
  annualization_backtest_result_id: number;
  summary?: PerformanceSummary;
  chart_manifest?: ChartManifest;
  source_result: {
    id: number;
    status: string;
    result_version: string;
    content_hash: string;
    spec: Record<string, unknown>;
    summary?: Record<string, unknown>;
    backtest_run_ids: number[];
  };
  created_at: string;
  completed_at?: string;
  error?: string;
  reused: boolean;
};

export type HistogramChart = {
  schema_version: string;
  kind: string;
  period: string;
  bins: Array<{ lower: number; upper: number; count: number }>;
};

export type AccumulationChart = {
  schema_version: string;
  kind: string;
  points: Array<{ time_ms: number; daily_return: number; arithmetic_sum: number; compounded_return: number }>;
};

export type UnderwaterChart = {
  schema_version: string;
  kind: string;
  points: Array<{ time_ms: number; drawdown: number; underwater_days: number }>;
};

export type ExposureChart = {
  schema_version: string;
  kind: string;
  points: Array<{ time_ms: number; actual_exposure_weight: number }>;
};

export type PerformanceChartResponse<T> = {
  report_id: number;
  kind: string;
  content_hash: string;
  point_count: number;
  data: T;
};

export type GenomePerformanceSummary = {
  genome_id: number;
  backtest_run_id?: number;
  backtest_result_id?: number;
  report?: PerformanceReport;
};

export const performanceReportsApi = {
  create(backtestResultId: number, input: { risk_free_annual_rate: number; histogram_bins?: number; beta_benchmark_instrument_id?: string }) {
    return apiFetch<PerformanceReport>(`/backtest-results/${backtestResultId}/performance-reports`, {
      method: "POST",
      body: JSON.stringify(input)
    });
  },
  list(backtestResultId: number) {
    return apiFetch<PerformanceReport[]>(`/backtest-results/${backtestResultId}/performance-reports`);
  },
  get(reportId: number) {
    return apiFetch<PerformanceReport>(`/performance-reports/${reportId}`);
  },
  chart<T>(reportId: number, kind: string) {
    return apiFetch<PerformanceChartResponse<T>>(`/performance-reports/${reportId}/charts/${kind}`);
  },
  verify(reportId: number) {
    return apiFetch<Record<string, unknown>>(`/performance-reports/${reportId}/integrity`);
  },
  latestForGenome(genomeId: number) {
    return apiFetch<GenomePerformanceSummary>(`/evolution/genomes/${genomeId}/performance-report`);
  }
};
