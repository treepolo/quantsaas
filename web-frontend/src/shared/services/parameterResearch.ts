import { apiFetch } from "./client";
import type { ComputePlanPreview, ComputeTaskStatus } from "./computeTasks";
import type { BacktestSettings, ParameterSpace, RelativeMetrics, RobustnessMetric } from "./robustness";

export type ResearchConfiguration = {
  id: number; name: string; notes: string; tags: string[]; config_hash: string; schema_version: string;
  instrument_id: string; data_source: string; symbol: string; interval: string; dataset_hash: string;
  start_time_ms: number; end_time_ms: number; execution_mode: string; parameter_space_version: string;
  parameter_space_hash: string; parameter_space: ParameterSpace; base_coordinates: number[]; dynamic_mode: boolean;
  dynamic_package?: { study_id: number; policy_artifact_id: number; artifact_set_hash: string; prediction_snapshot_hash: string; parameter_space_hash: string };
  geometry_package?: { study_id: number; artifact_id: number; horizon: number; dataset_hash: string; content_hash: string; schema_version: string };
  archived: boolean; created_at: string;
};

export type ResearchPoint = { id: number; vector_hash: string; coordinates: number[]; parameters: Record<string, number>; status: string; qualified: boolean; backtest_result_id?: number; metrics?: RelativeMetrics };
export type ResearchStage = { id: number; ordinal: number; stage_key: string; stage_type: string; manifest_hash: string; compute_task_id?: number; status: ComputeTaskStatus; requested_count: number; unique_count: number; cache_hit_count: number; completed_count: number; failed_count: number; missing_count: number; sobol_start_index: number; sobol_end_index: number; error_message?: string };
export type ResearchRun = { id: number; configuration_id: number; run_key: string; sampler_version: string; next_sobol_index: number; global_unique_point_count: number; global_batch_count: number; exploration_status: string; status: ComputeTaskStatus; stages: ResearchStage[]; points?: ResearchPoint[]; created_at: string };
export type ResearchPlan = { plan_key: string; manifest_hash: string; stage_type: string; points: Array<{ coordinates: number[]; parameters: Record<string, number>; vector_hash: string; origin_type: string }>; compute: ComputePlanPreview; next_sobol_index: number; global?: { mode: string; requested_sobol: number; base_and_anchor_count: number; unique_point_count: number; duplicate_count: number; rejected_count: number; total_combinations: number } };
export type ResearchAnalysis = { id: number; configuration_id: number; point_set_hash: string; completeness: string; content_hash: string; robustness_study_id: number; robustness_snapshot_id: number; result: { points: Array<{ id: string; coordinates: number[]; metrics?: RelativeMetrics }>; regions: Array<{ id: string; point_ids: string[]; frontier_ids: string[]; center_ids: string[]; proposals: Array<{ point_id: string; roles: string[]; provisional: boolean }> }>; missing_coordinates: number[][] } };
export type Candidate = { id: number; configuration_id: number; point_id: number; analysis_snapshot_id?: number; region_id?: number; source_kind: string; completeness: string; roles: string[]; adoption_unit_hash: string; name: string; notes: string; tags: string[]; archived: boolean; gene_record_id?: number; analysis_links: Array<{ kind: "G"|"H"|"L"|"C"; status: string; source_id?: string; error_message?: string }> };
export type DynamicSpace = { study_id: number; policy_artifact_id: number; schema: { schema_version: string; variables: Array<{ stable_id: string; parameter_id: string; control_mode: string; role: string; lower: number; upper: number; minimum_step: number; display_decimals: number; prediction_input?: string }> }; base_values: Record<string, number> };
export type Surrogate = { id:number; configuration_id:number; run_id:number; status:ComputeTaskStatus; compute_task_id?:number; training_point_set_hash:string; can_guide_return:boolean; can_guide_drawdown:boolean; can_guide_conservative:boolean; content_hash?:string };
export type SurrogateProposal = { id:number; types:string[]; vector_hash:string; coordinates:number[]; parameters:Record<string,number>; prediction:{return_mean:number;return_dispersion:number;drawdown_mean:number;drawdown_dispersion:number}; actual_point_id?:number };
export type StagePlanInput = { kind:"append_global"|"local_refinement"|"surrogate_proposals"; requested_sobol?:number; center_point_id?:number; radius?:number; surrogate_id?:number; proposal_ids?:number[] };

const root = "/lab/parameter-research";
export const parameterResearchApi = {
  listConfigurations: () => apiFetch<ResearchConfiguration[]>(`${root}/configurations`),
  dynamicSpace: (policyId: number) => apiFetch<DynamicSpace>(`${root}/dynamic-policy-spaces/${policyId}`),
  createConfiguration: (input: { name: string; notes?: string; tags?: string[]; genome_id: number; parameter_space: ParameterSpace; base_coordinates: number[]; backtest: BacktestSettings; dynamic?: { study_id: number; policy_artifact_id: number }; geometry?: { study_id: number; artifact_id: number; horizon: number; content_hash: string } }) => apiFetch<ResearchConfiguration>(`${root}/configurations`, { method: "POST", body: JSON.stringify(input) }),
  listRuns: (configurationId:number) => apiFetch<ResearchRun[]>(`${root}/configurations/${configurationId}/runs`),
  planRun: (configurationId: number, requestedSobol = 0) => apiFetch<ResearchPlan>(`${root}/configurations/${configurationId}/runs/plan`, { method: "POST", body: JSON.stringify({ requested_sobol: requestedSobol, root_seed: 0 }) }),
  startRun: (configurationId: number, plan: ResearchPlan, requestedSobol = 0, confirmSoftLimit = false) => apiFetch<ResearchRun>(`${root}/configurations/${configurationId}/runs`, { method: "POST", body: JSON.stringify({ plan: { requested_sobol: requestedSobol, root_seed: 0 }, plan_key: plan.plan_key, idempotency_key: crypto.randomUUID(), confirm_soft_limit: confirmSoftLimit }) }),
  getRun: (id: number) => apiFetch<ResearchRun>(`${root}/runs/${id}`),
  planStage: (id: number, input: StagePlanInput) => apiFetch<ResearchPlan>(`${root}/runs/${id}/stages/plan`, { method: "POST", body: JSON.stringify(input) }),
  startStage: (id: number, input: StagePlanInput, plan: ResearchPlan, confirmSoftLimit = false) => apiFetch<ResearchRun>(`${root}/runs/${id}/stages`, { method: "POST", body: JSON.stringify({ plan: input, plan_key: plan.plan_key, confirm_soft_limit: confirmSoftLimit }) }),
  pauseRun: (id: number) => apiFetch(`${root}/runs/${id}/pause`, { method: "POST" }),
  resumeRun: (id: number) => apiFetch<ResearchRun>(`${root}/runs/${id}/resume`, { method: "POST" }),
  cancelRun: (id: number) => apiFetch(`${root}/runs/${id}/cancel`, { method: "POST" }),
  analyze: (id: number, metric: RobustnessMetric) => apiFetch<ResearchAnalysis>(`${root}/runs/${id}/analyses`, { method: "POST", body: JSON.stringify({ metric, radii: [1,2,3,5,8,13] }) }),
  deriveCandidates: (snapshotId: number) => apiFetch<Candidate[]>(`${root}/analysis-snapshots/${snapshotId}/candidates/derive`, { method: "POST" }),
  manualCandidate: (pointId: number) => apiFetch<Candidate>(`${root}/points/${pointId}/candidates`, { method: "POST" }),
  listCandidates: (configurationId?: number) => apiFetch<Candidate[]>(`${root}/candidates${configurationId ? `?configuration_id=${configurationId}` : ""}`),
  getCandidate: (id: number) => apiFetch<Candidate>(`${root}/candidates/${id}`),
  exportCandidate: (id: number) => apiFetch<Candidate>(`${root}/candidates/${id}/export-to-library`, { method: "POST" }),
  promoteCandidate: (id: number) => apiFetch<Candidate>(`${root}/candidates/${id}/promote`, { method: "POST" }),
  planSurrogate: (runId: number, seed = 42) => apiFetch<ComputePlanPreview>(`${root}/runs/${runId}/surrogates/plan`, { method: "POST", body: JSON.stringify({ seed }) }),
  listSurrogates: (runId:number) => apiFetch<Surrogate[]>(`${root}/runs/${runId}/surrogates`),
  startSurrogate: (runId:number, plan:ComputePlanPreview, seed=42) => apiFetch<Surrogate>(`${root}/runs/${runId}/surrogates`, {method:"POST",body:JSON.stringify({seed,plan_key:plan.plan_key,confirm_soft_limit:false})}),
  getSurrogate: (id:number) => apiFetch<Surrogate>(`${root}/surrogates/${id}`),
  createProposals: (id:number, kind:"high_return"|"low_drawdown"|"high_uncertainty"|"pure_coverage", count=4) => apiFetch<SurrogateProposal[]>(`${root}/surrogates/${id}/proposals`,{method:"POST",body:JSON.stringify({kind,count})}),
  listProposals: (id:number) => apiFetch<SurrogateProposal[]>(`${root}/surrogates/${id}/proposals`),
  createSeries: (name: string, configurationIds: number[]) => apiFetch<Record<string, unknown>>(`${root}/series`, { method: "POST", body: JSON.stringify({ name, configuration_ids: configurationIds, changed_factors: [], factor_values: [] }) })
};
