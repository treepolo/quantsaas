import { apiFetch } from "./client";
import type { ComputePlanPreview, ComputeTask } from "./computeTasks";

export type RobustnessMetric =
  | "log_final_nav_ratio"
  | "drawdown_residual_ratio"
  | "log_drawdown_residual_ratio"
  | "performance_drawdown_composite"
  | "qualification";

export type ParameterDefinition = {
  name: string;
  label: string;
  type: "float" | "int";
  legal_min: number;
  legal_max: number;
  default_step: number;
  active: boolean;
};

export type ParameterAxis = ParameterDefinition & {
  values: number[];
  step: number;
  study_start: number;
  study_end: number;
};

export type ParameterSpace = {
  schema_version: string;
  axes: ParameterAxis[];
  fixed: Record<string, number>;
  excluded_coordinates?: number[][];
};

export type RelativeMetrics = {
  version: string;
  final_nav_ratio: number;
  log_final_nav_ratio: number;
  drawdown_residual_ratio: number;
  log_drawdown_residual_ratio: number;
  performance_drawdown_composite: number;
  qualified: boolean;
};

export type EvaluationPoint = {
  id: string;
  kind: "actual" | "predicted" | "proposed";
  state: "qualified" | "unqualified" | "unknown";
  coordinates: number[];
  parameters: Record<string, number>;
  metrics?: RelativeMetrics;
  backtest_result_id?: number;
  source_stage?: string;
  sampling_batch?: string;
};

export type ScaleStatistic = {
  radius: number;
  expected_points: number;
  observed_points: number;
  unknown_points: number;
  qualified_points: number;
  qualification_ratio: number;
  area_ratio: number;
  mean: number;
  median: number;
  standard_deviation: number;
  center_to_mean?: number;
  center_to_median?: number;
  complete: boolean;
};

export type PointGeometry = {
  point_id: string;
  directions: { axis: string; direction: string; steps: number; stop_reason: string }[];
  axis_failure_depth: number;
  axis_failure_exact: boolean;
  guaranteed_box_radius: number;
  guaranteed_box_exact: boolean;
  neighborhood_quality: number;
  neighborhood_stability: number;
  neighborhood_dispersion: number;
  medoid_cost: number;
  completeness: string;
  truncation_reasons?: string[];
};

export type ConnectedRegion = {
  id: string;
  point_ids: string[];
  geometries: PointGeometry[];
  frontier_ids: string[];
  center_ids: string[];
  proposals: { point_id: string; roles: string[]; provisional: boolean }[];
};

export type AnalysisResult = {
  analysis_version: string;
  connectivity_version: string;
  distance_version: string;
  frontier_version: string;
  center_version: string;
  metric: RobustnessMetric;
  center_point_id?: string;
  points: EvaluationPoint[];
  scales: ScaleStatistic[];
  regions: ConnectedRegion[];
  missing_coordinates: number[][];
  observed_point_set_hash?: string;
};

export type AnalysisDescriptor = {
  id: number;
  analysis_key: string;
  point_set_hash: string;
  settings_hash: string;
  metric: RobustnessMetric;
  radii: number[];
  content_hash: string;
  result: AnalysisResult;
  created_at: string;
};

export type RobustnessStudy = {
  id: number;
  name: string;
  mode: "one_dimensional" | "two_dimensional" | "multidimensional" | "imported_evaluations";
  status: string;
  study_key: string;
  setting_version: string;
  setting_hash: string;
  settings?: unknown;
  space_version: string;
  space_hash: string;
  parameter_space: ParameterSpace;
  center_point_key: string;
  source_genome_id?: number;
  compute_task_id?: number;
  expected_point_count: number;
  actual_point_count: number;
  predicted_point_count: number;
  points?: EvaluationPoint[];
  latest_analysis?: AnalysisDescriptor;
  created_at: string;
  completed_at?: string;
};

export type BacktestSettings = {
  instrument_id: string;
  data_source?: string;
  execution_mode: string;
  start_time_ms: number;
  end_time_ms: number;
  symbol: string;
  interval: string;
  initial_capital?: number;
  monthly_dca?: number;
  fee_rate?: number;
  spread_rate?: number;
  long_term_filter_enabled?: boolean;
  long_term_filter_months?: number;
};

export type CreateRobustnessStudy = {
  name: string;
  mode: "one_dimensional" | "two_dimensional" | "multidimensional";
  genome_id: number;
  axes: string[];
  radius: number;
  radii: number[];
  metric: RobustnessMetric;
  custom_steps?: Record<string, number>;
  sample_count?: number;
  sample_offset?: number;
  confirm_soft_limit?: boolean;
  backtest: BacktestSettings;
};

export const robustnessApi = {
  parameters(genomeId: number) {
    return apiFetch<{ definitions: ParameterDefinition[]; values: Record<string, number> }>(`/robustness/parameters?genome_id=${genomeId}`);
  },
  preview(input: CreateRobustnessStudy) {
    return apiFetch<ComputePlanPreview>("/robustness/studies/preview", { method: "POST", body: JSON.stringify(input) });
  },
  create(input: CreateRobustnessStudy) {
    return apiFetch<{ study: RobustnessStudy; preview: ComputePlanPreview; task?: ComputeTask }>("/robustness/studies", {
      method: "POST",
      body: JSON.stringify(input)
    });
  },
  list(limit = 50) {
    return apiFetch<RobustnessStudy[]>(`/robustness/studies?limit=${limit}`);
  },
  get(id: number) {
    return apiFetch<RobustnessStudy>(`/robustness/studies/${id}`);
  },
  analyze(id: number, metric: RobustnessMetric, radii: number[]) {
    return apiFetch<AnalysisDescriptor>(`/robustness/studies/${id}/analyze`, {
      method: "POST",
      body: JSON.stringify({ metric, radii })
    });
  }
};
