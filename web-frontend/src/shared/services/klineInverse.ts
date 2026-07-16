import { apiFetch } from "./client";

export type Bounds = { g_min:number; g_max:number; b_min:number; b_max:number; u_min:number; u_max:number; d_min:number; d_max:number };
export type Batch = { id:number; ordinal:number; batch_type:string; budget:number; status:string; completed_count:number; cache_hit_count:number; error_count:number; rng_start:number; rng_end:number; checkpoint_position:number; compute_task_id?:number; error_message?:string };
export type Study = { id:number; name:string; notes:string; tags:string[]; status:string; current_stage:string; study_hash:string; source_kind:string; source_id:number; instrument_id:string; data_source:string; symbol:string; interval:string; execution_mode:string; warmup_length:number; evaluation_length:number; evaluation_start_ms:number; initial_budget:number; cell_count:number; parent_capacity:number; root_seed:number; observed_bounds:Bounds; final_bounds:Bounds; current_snapshot_id?:number; archived:boolean; batches:Batch[]; canonical:Record<string,unknown>; created_at:string; updated_at:string };
export type CompositePlan = { plan_key:string; total_items:number; estimated_units:number; cache_hit_count:number; new_item_count:number; requires_confirmation:boolean; stages:Array<{stage_key:string; total_items:number; estimated_units:number; cache_hit_count:number; new_item_count:number}> };
export type InitialPlan = { study:Study; plan:CompositePlan; feature_calculations:number; backtest_evaluations:number; storage_estimate_bytes:number };
export type BatchPlan = { batch_id:number; batch_type:string; plan:{plan_key:string; total_items:number; estimated_units:number; cache_hit_count:number; new_item_count:number; requires_confirmation:boolean}; manifest_hash:string; compatibility_hash:string; backtest_evaluations:number };
export type CreateDraft = { name:string; notes?:string; tags?:string[]; genome_id?:number; candidate_id?:number; backtest_result_id?:number; instrument_id:string; data_source:string; symbol:string; interval:string; execution_mode:string; evaluation_start_ms:number; evaluation_length:number; calibration_sources?:Array<{instrument_id:string;data_source:string;symbol:string;interval:string;start_time_ms:number;end_time_ms:number}>; final_bounds?:Bounds; initial_capital:number; fee_rate:number; slippage_rate:number; initial_budget:number; cell_count:number; parent_capacity:number; root_seed:number; mutation_amplitude:number };
export type Quantiles = { min?:number;p10?:number;median?:number;p90?:number;max?:number };
export type Overview = { study_id:number;snapshot_id:number;status:string;evaluated_count:number;touched_cell_count:number;search_reach:number;a_cell_count:number;b_cell_count:number;a_coverage:number;b_coverage:number;a_cell_per_touched:number;b_cell_per_touched:number;state_counts:Record<string,number>;feature_statistics:Record<string,Quantiles>;distance_statistics:Record<string,Quantiles>;q_relative_statistics:Quantiles;q_absolute_statistics:Quantiles;permanent_path_count:number;lineage_edge_count:number;cache_hit_count:number;error_count:number };
export type Cell = { cell_index:number;evaluation_count:number;a_count:number;b_count:number;active_pareto_count:number;best_q_rel:number;median_q_rel:number;best_q_abs:number;median_q_abs:number;median_nearest_distance:number;features:number[] };
export type MapData = { study_id:number;snapshot_id:number;axis_x:string;axis_y:string;target:string;color:string;cells:Cell[] };
export type PathSummary = { id:number;path_hash:string;evaluation_id:number;cell_index:number;outcome_state:string;pass_a:boolean;pass_b:boolean;q_rel:number;q_abs:number;backtest_result_id:number;permanent_reason:string };
export type PathPage = { items:PathSummary[];page:number;page_size:number;total:number;total_pages:number };
export type PathDetail = PathSummary & { warmup_length:number;evaluation_length:number;coordinates:Array<{g:number;b:number;u:number;d:number}>;ohlc:Array<{time_ms:number;open:number;high:number;low:number;close:number}>;features:Record<string,Record<string,number>>;performance_report_ids:number[] };
export type Lineage = { id:number;child_path_id:number;parent_path_id?:number;requested_operation:string;actual_operation:string;changed_start:number;changed_length:number;changed_channels:string[];amplitude:number;batch_id:number };
export type Boundary = { anchor:PathSummary;points:Array<{child_path_id:number;operation:string;distance:{d_w:number;d_h:number;d_total:number};q_rel:number;q_abs:number;state:string;changed_start:number;changed_length:number;amplitude:number;batch_id:number}>;nearest_failure_a?:number;nearest_failure_b?:number;pass_curve_a:Array<{radius:number;passed:number;total:number;rate:number}>;pass_curve_b:Array<{radius:number;passed:number;total:number;rate:number}> };

const root = "/kline-inverse";
export const klineInverseApi = {
  createDraft:(input:CreateDraft)=>apiFetch<Study>(`${root}/studies/drafts`,{method:"POST",body:JSON.stringify(input)}),
  list:(includeArchived=false)=>apiFetch<Study[]>(`${root}/studies?include_archived=${includeArchived}`),
  get:(id:number)=>apiFetch<Study>(`${root}/studies/${id}`),
  plan:(id:number)=>apiFetch<InitialPlan>(`${root}/studies/${id}/plan`,{method:"POST"}),
  start:(id:number,planKey:string,confirm=false)=>apiFetch<Study>(`${root}/studies/${id}/start`,{method:"POST",body:JSON.stringify({plan_key:planKey,confirm_soft_limit:confirm})}),
  startNext:(id:number)=>apiFetch<Study>(`${root}/studies/${id}/start-next`,{method:"POST"}),
  archive:(id:number)=>apiFetch<Study>(`${root}/studies/${id}/archive`,{method:"POST"}),
  cancel:(id:number,batchId:number)=>apiFetch<Study>(`${root}/studies/${id}/batches/${batchId}/cancel`,{method:"POST"}),
  resume:(id:number,batchId:number)=>apiFetch<Study>(`${root}/studies/${id}/batches/${batchId}/resume`,{method:"POST"}),
  planExtension:(id:number,budget:number)=>apiFetch<BatchPlan>(`${root}/studies/${id}/search-extensions/plan`,{method:"POST",body:JSON.stringify({additional_budget:budget})}),
  planProbe:(id:number,input:{anchor_path_id:number;budget:number;operations:string[];scope:string;min_length:number;max_length:number;amplitude:number})=>apiFetch<BatchPlan>(`${root}/studies/${id}/probes/plan`,{method:"POST",body:JSON.stringify(input)}),
  startExtension:(id:number,plan:BatchPlan,confirm=false)=>apiFetch<Study>(`${root}/studies/${id}/search-extensions/start`,{method:"POST",body:JSON.stringify({batch_id:plan.batch_id,plan_key:plan.plan.plan_key,confirm_soft_limit:confirm})}),
  startProbe:(id:number,plan:BatchPlan,confirm=false)=>apiFetch<Study>(`${root}/studies/${id}/probes/start`,{method:"POST",body:JSON.stringify({batch_id:plan.batch_id,plan_key:plan.plan.plan_key,confirm_soft_limit:confirm})}),
  overview:(id:number)=>apiFetch<Overview>(`${root}/studies/${id}/overview`),
  map:(id:number,x:string,y:string,target:string,color:string)=>apiFetch<MapData>(`${root}/studies/${id}/map?axis_x=${x}&axis_y=${y}&target=${target}&color=${color}`),
  paths:(id:number,page=1,target="all")=>apiFetch<PathPage>(`${root}/studies/${id}/paths?page=${page}&page_size=50&permanent=true&target=${target}`),
  path:(id:number,pathId:number)=>apiFetch<PathDetail>(`${root}/studies/${id}/paths/${pathId}`),
  lineage:(id:number,pathId:number)=>apiFetch<Lineage[]>(`${root}/studies/${id}/paths/${pathId}/lineage`),
  boundary:(id:number,pathId:number)=>apiFetch<Boundary>(`${root}/studies/${id}/anchors/${pathId}/boundary`),
  createPerformanceReport:(id:number,pathId:number)=>apiFetch<Record<string,unknown>>(`${root}/studies/${id}/paths/${pathId}/performance-reports`,{method:"POST",body:JSON.stringify({risk_free_annual_rate:0,histogram_bins:30})})
};
