import { apiFetch } from "./client";
import type { ComputePlanPreview, ComputeTask } from "./computeTasks";

export type ModelReport = {
  schema_version: string;
  artifact_hash: string;
  route: string;
  horizon: number;
  target_kind: string;
  mean_loss: number;
  mean_baseline_loss: number;
  standard_error: number;
  baseline_gate_passed: boolean;
  predictive_status: string;
  content_hash: string;
};

export type DynamicStudy = {
  id: number;
  name: string;
  status: string;
  route: "explainable_gam" | "causal_tcn";
  study_key: string;
  setting_hash: string;
  dataset_hash: string;
  compute_task_id?: number;
  materialization_task_id?: number;
  artifact_set_hash?: string;
  prediction_snapshot_id?: number;
  policy_artifact_id?: number;
  materialization_id?: number;
  reports?: ModelReport[];
  comparison?: {
    source_kind: string;
    source_id: number;
    source_version: string;
    snapshot_id: number;
    content_hash: string;
    available_blocks: string[];
  };
  error_message?: string;
  created_at: string;
  completed_at?: string;
};

export type ParameterMode = "fixed" | "global" | "continuous" | "six_state";

export type CreateDynamicStudy = {
  name: string;
  genome_id: number;
  route: "explainable_gam" | "causal_tcn";
  lookbacks: number[];
  folds: number;
  minimum_train: number;
  instrument_id: string;
  data_source: string;
  symbol: string;
  interval: "1d";
  execution_mode: "close_same_bar" | "close_next_open";
  train_start_time_ms: number;
  train_end_time_ms: number;
  activity_kappa: number;
  region_rule: { direction_boundary: number; magnitude_boundary: number };
  policy: {
    schema_version: "p09-dynamic-policy-v1";
    version: string;
    controls: Array<Record<string, unknown>>;
    evolve_gamma: boolean;
  };
  long_term_filter_enabled: boolean;
  long_term_filter_months: number;
  compute_monitor_enabled?: boolean;
  confirm_soft_limit?: boolean;
};

export type ReportBlock = {
  block_id: string;
  block_kind: string;
  schema_version: string;
  formula_version: string;
  content_hash: string;
  point_count: number;
  payload: unknown;
};

export const dynamicParametersApi = {
  preview(input: CreateDynamicStudy) {
    return apiFetch<ComputePlanPreview>("/dynamic-parameters/studies/preview", { method: "POST", body: JSON.stringify(input) });
  },
  create(input: CreateDynamicStudy) {
    return apiFetch<{ study: DynamicStudy; preview: ComputePlanPreview; task?: ComputeTask }>("/dynamic-parameters/studies", { method: "POST", body: JSON.stringify(input) });
  },
  list(limit = 50) {
    return apiFetch<DynamicStudy[]>(`/dynamic-parameters/studies?limit=${limit}`);
  },
  get(id: number) {
    return apiFetch<DynamicStudy>(`/dynamic-parameters/studies/${id}`);
  },
  materialize(id: number, confirmSoftLimit = false, computeMonitorEnabled = true) {
    return apiFetch<{ study: DynamicStudy; task: ComputeTask }>(`/dynamic-parameters/studies/${id}/materialize`, { method: "POST", body: JSON.stringify({ confirm_soft_limit: confirmSoftLimit, compute_monitor_enabled: computeMonitorEnabled }) });
  },
  previewMaterialize(id: number, computeMonitorEnabled = true) {
    return apiFetch<ComputePlanPreview>(`/dynamic-parameters/studies/${id}/materialize/preview`, { method: "POST", body: JSON.stringify({ compute_monitor_enabled: computeMonitorEnabled }) });
  },
  reportBlock(id: number, blockId: string) {
    return apiFetch<ReportBlock>(`/dynamic-parameters/studies/${id}/report-blocks/${blockId}`);
  }
};
