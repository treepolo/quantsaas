import { apiFetch } from "./client";
import type { ComputePlanPreview, ComputeTask } from "./computeTasks";

export type GeometryStudy = {
  id: number;
  name: string;
  status: string;
  study_key: string;
  setting_hash: string;
  dataset_hash: string;
  compute_task_id?: number;
  artifact_set_hash?: string;
  prediction_id?: number;
  created_at: string;
  completed_at?: string;
  error_message?: string;
  artifacts?: Array<{
    id: number;
    horizon: number;
    lookback: number;
    content_hash: string;
    report: { regions: number; samples: number; validation_samples?: number; joint_nll: number; baseline_joint_nll?: number; area_nll?: number; slope_nll?: number; calibration_error?: number; out_of_sample?: boolean; baseline_gate_passed?: boolean; purge: number; walk_forward_version: string };
  }>;
  predictions?: {
    one_day: Array<{ index: number; time_ms: number; available: boolean; distribution?: { regions: Array<{ probability: number; area_center: number; slope_center: number; area_lower: number; area_upper: number; slope_lower: number; slope_upper: number; area_slope_covariance: number }> } }>;
    twenty_day: Array<{ index: number; time_ms: number; available: boolean; distribution?: { regions: Array<{ probability: number; area_center: number; slope_center: number; area_lower: number; area_upper: number; slope_lower: number; slope_upper: number; area_slope_covariance: number }> } }>;
  };
};

export type GeometryArtifact = { study_id: number; study_name: string; artifact_id: number; horizon: number; lookback: number; instrument_id: string; data_source: string; symbol: string; interval: string; dataset_hash: string; schema_version: string; content_hash: string; status: string };

export type CreateGeometryStudy = {
  name: string;
  instrument_id: string;
  data_source: string;
  symbol: string;
  interval: "1d";
  train_start_time_ms: number;
  train_end_time_ms: number;
  lookbacks: number[];
  folds: number;
  minimum_train: number;
  compute_monitor_enabled?: boolean;
  confirm_soft_limit?: boolean;
};

export const trendGeometryApi = {
  preview(input: CreateGeometryStudy) {
    return apiFetch<ComputePlanPreview>("/trend-geometry/studies/preview", { method: "POST", body: JSON.stringify(input) });
  },
  create(input: CreateGeometryStudy) {
    return apiFetch<{ study: GeometryStudy; preview: ComputePlanPreview; task?: ComputeTask }>("/trend-geometry/studies", { method: "POST", body: JSON.stringify(input) });
  },
  list(limit = 50) {
    return apiFetch<GeometryStudy[]>(`/trend-geometry/studies?limit=${limit}`);
  },
  get(id: number) {
    return apiFetch<GeometryStudy>(`/trend-geometry/studies/${id}`);
  },
  artifacts(input: { instrument_id?: string; data_source?: string; symbol?: string; interval?: string; dataset_hash?: string; horizon?: number } = {}) {
    const query = new URLSearchParams(Object.entries(input).filter(([, value]) => value !== undefined && value !== "") as Array<[string, string]>).toString();
    return apiFetch<GeometryArtifact[]>(`/trend-geometry/artifacts${query ? `?${query}` : ""}`);
  }
};
