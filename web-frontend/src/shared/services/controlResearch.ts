import { apiFetch } from "./client";
import type { ComputePlanPreview } from "./computeTasks";
import type { BacktestSettings } from "./robustness";

export type ReturnDistribution = {
  period: string; count: number; mean: number; median: number; std_dev: number; skewness: number;
  excess_kurtosis: number; minimum: number; maximum: number; quantiles: Record<string, number>;
};

export type ControlMetrics = {
  roi: number; final_equity: number; final_nav_ratio: number; log_final_nav_ratio: number;
  max_drawdown: number; sortino?: number; longest_underwater_days: number; trade_count: number;
  exposure_days_ratio: number; average_exposure: number; fee_cost: number; slippage_cost: number;
  return_distributions: Record<string, ReturnDistribution>;
};

export type ControlDistribution = { count: number; min: number; max: number; median: number; p05: number; p25: number; p75: number; p95: number };
export type DistributionSet = { log_final_nav_ratio: ControlDistribution; max_drawdown: ControlDistribution; sortino?: ControlDistribution; longest_underwater_days: ControlDistribution };
export type PercentileSet = { log_final_nav_ratio: number; max_drawdown: number; sortino?: number; longest_underwater_days: number };
export type RuleResult = { evaluation_id: number; rule_type: string; backtest_result_id: number; metrics: ControlMetrics };
export type SnapshotSummary = {
  baseline_evaluation_id: number; baseline_result_id: number; baseline: ControlMetrics;
  random_distribution?: DistributionSet; random_percentiles?: PercentileSet;
  shuffle_distribution?: DistributionSet; shuffle_percentiles?: PercentileSet;
  rules: RuleResult[]; conclusion_labels: string[];
};
export type ControlSnapshot = {
  id: number; completeness: string; statistics_version: string; random_completed_count: number;
  shuffle_completed_count: number; rule_completed_count: number; failed_count: number;
  cancelled_count: number; cache_hit_count: number; content_hash: string; summary: SnapshotSummary; created_at: string;
};
export type ControlStage = { id: number; key: string; type: string; status: string; completed_count: number; total_count: number; failed_count: number; progress: number; error?: string };
export type ControlTask = {
  id: number; name: string; notes: string; tags: string[]; status: string; source_kind: string;
  source_genome_id?: number; candidate_id?: number; research_configuration_id?: number;
  random_batch_id: number; random_target_count: number; shuffle_target_count: number;
  toggle_every_n_bars: number; same_structure: boolean; compute_task_id?: number;
  stages: ControlStage[]; latest_snapshot?: ControlSnapshot; archived: boolean; created_at: string; completed_at?: string;
};
export type CompositePreview = {
  plan_key: string; stages: ComputePlanPreview[]; total_items: number; estimated_units: number;
  cache_hit_count: number; new_item_count: number; soft_item_limit: number; hard_item_limit: number; requires_confirmation: boolean;
};
export type ControlPlan = {
  plan_key: string; task_key: string; batch_key: string; random_count: number; shuffle_count: number;
  attempt_count: number; rejection_count: number; reject_reasons: Record<string, number>;
  fixed_dimensions: string[]; random_dimensions: string[]; same_structure: boolean; compute: CompositePreview;
};
export type RandomRecord = { id: number; batch_id: number; sequence_index: number; coordinates: number[]; parameters: Record<string, number>; content_hash: string; backtest_result_id?: number };
export type ControlEvaluation = { id: number; kind: string; sequence_index: number; rule_type?: string; backtest_result_id: number; metrics: ControlMetrics; representative_role?: string };
export type ControlDetail = { task_id: number; snapshot_id: number; evaluations: ControlEvaluation[] };

export type CreateControlTask = {
  name: string; notes?: string; tags?: string[]; genome_id?: number; candidate_id?: number;
  backtest: BacktestSettings; random_seed: number; random_count: number; shuffle_seed: number;
  shuffle_count: number; toggle_every_n_bars: number; confirm_soft_limit?: boolean; expected_plan_key?: string;
};

const root = "/lab/control-analysis";
export const controlResearchApi = {
  preview: (input: CreateControlTask) => apiFetch<ControlPlan>(`${root}/tasks/preview`, { method: "POST", body: JSON.stringify(input) }),
  create: (input: CreateControlTask) => apiFetch<ControlTask>(`${root}/tasks`, { method: "POST", body: JSON.stringify(input) }),
  list: () => apiFetch<ControlTask[]>(`${root}/tasks`),
  get: (id: number) => apiFetch<ControlTask>(`${root}/tasks/${id}`),
  startNext: (id: number) => apiFetch<ControlTask>(`${root}/tasks/${id}/start-next`, { method: "POST" }),
  cancel: (id: number) => apiFetch<ControlTask>(`${root}/tasks/${id}/cancel`, { method: "POST" }),
  retry: (id: number) => apiFetch<ControlTask>(`${root}/tasks/${id}/retry`, { method: "POST" }),
  previewExtension: (id: number, randomCount: number, shuffleCount: number) => apiFetch<ControlPlan>(`${root}/tasks/${id}/extensions/preview`, { method: "POST", body: JSON.stringify({ random_count: randomCount, shuffle_count: shuffleCount }) }),
  extend: (id: number, randomCount: number, shuffleCount: number, confirmSoftLimit = false) => apiFetch<ControlTask>(`${root}/tasks/${id}/extensions`, { method: "POST", body: JSON.stringify({ random_count: randomCount, shuffle_count: shuffleCount, confirm_soft_limit: confirmSoftLimit }) }),
  snapshots: (id: number) => apiFetch<ControlSnapshot[]>(`${root}/tasks/${id}/snapshots`),
  randomRecords: (batchId: number, offset = 0) => apiFetch<RandomRecord[]>(`${root}/random-batches/${batchId}/records?limit=100&offset=${offset}`),
  detail: (taskId: number, snapshotId: number) => apiFetch<ControlDetail>(`${root}/tasks/${taskId}/snapshots/${snapshotId}/detail`),
  pathBlock: (evaluationId: number, block = 0) => apiFetch<Record<string, unknown>>(`${root}/evaluations/${evaluationId}/path?block=${block}`),
  updateMetadata: (id: number, input: { name?: string; notes?: string; tags?: string[]; archived?: boolean }) => apiFetch<ControlTask>(`${root}/tasks/${id}`, { method: "PATCH", body: JSON.stringify(input) }),
	deletePathDetails: (id: number) => apiFetch<Record<string, unknown>>(`${root}/tasks/${id}/path-details?confirm=true`, { method: "DELETE" }),
	deleteTask: (id: number) => apiFetch<Record<string, unknown>>(`${root}/tasks/${id}?confirm=true`, { method: "DELETE" }),
	deleteUnusedBatch: (id: number) => apiFetch<Record<string, unknown>>(`${root}/random-batches/${id}?confirm=true`, { method: "DELETE" })
};
