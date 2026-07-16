import { apiFetch } from "./client";
import type { ComputePlanPreview, ComputeTask } from "./computeTasks";

export type PerturbationSource = { instrument_id:string; data_source:string; symbol:string; display_name:string; interval:string; version_id?:number; content_hash?:string; artifact_kind:string; immutable:boolean; has_perturbation_ancestor:boolean };
export type GroupPlan = { schema_version:string; source:PerturbationSource; actual_start_time_ms:number; actual_end_time_ms:number; bar_count:number; previous_close_present:boolean; previous_close?:number; source_content_hash:string; estimated_bytes:number; plan_hash:string; source_version:string; wick_warning:boolean };
export type Deviation = { median:number; p95:number; maximum:number; open_max:number; high_max:number; low_max:number; close_max:number };
export type PerturbationVariant = { id:number; group_id:number; seed:string; alpha:string; recipe_hash:string; output_version_id:number; output_instrument_id:string; generated_content_hash?:string; status:string; integrity_status:string; bar_count:number; deviation:Deviation; compute_task_id?:number; archived:boolean; error_code?:string; error_message?:string; created_at:string };
export type PerturbationGroup = { id:number; name:string; notes?:string; tags:string[]; algorithm_version:string; archived:boolean; snapshot:{id:number;source_content_hash:string;source_version_id:number;original_instrument_id:string;original_data_source:string;original_symbol:string;interval:string;start_time_ms:number;end_time_ms:number;bar_count:number;status:string};variants?:PerturbationVariant[];created_at:string };
export type VariantPlan = { schema_version:string;group_id:number;plan_hash:string;seeds:string[];alphas:string[];recipes:Array<{seed:string;alpha:string;recipe_hash:string;variant_id?:number;reusable:boolean}>;unique_variants:number;existing_variants:number;pending_variants:number;total_output_bars:number;estimated_bytes:number;requires_confirmation:boolean };
export type Subject = { id?:number;ordinal:number;source_kind:string;source_id:number;source_version:string;subject_hash:string;adoption_unit:unknown;dynamic:boolean;candidate_id?:number };
export type TestPlan = { schema_version:string;plan_hash:string;test_spec_hash:string;group_id:number;subject_count:number;variant_count:number;planned_runs:number;cache_hits:number;pending_runs:number;subjects:Subject[] };
export type PerturbationTest = {id:number;group_id:number;name:string;notes?:string;tags:string[];status:string;test_spec_hash:string;backtest:{strategy_id:string;execution_mode:string;start_time_ms:number;end_time_ms:number;initial_capital?:number;monthly_dca?:number;fee_rate?:number;spread_rate?:number;long_term_filter_enabled?:boolean;long_term_filter_months?:number};subjects?:Subject[];latest_snapshot_id?:number;archived:boolean;created_at:string};
export type BatchPlan={schema_version:string;test_id:number;plan_hash:string;manifest_hash:string;seeds:string[];alphas:string[];variant_ids:number[];subject_count:number;dataset_count:number;planned_runs:number;existing_runs:number;pending_runs:number;requires_confirmation:boolean};
export type PerturbationRun={id:number;test_id:number;batch_id:number;subject_id:number;dataset_version_id:number;dataset_content_hash:string;alpha:string;seed:string;status:string;backtest_result_id?:number;reused:boolean;metrics?:{parameter_final_nav?:number;dca_final_nav?:number;parameter_total_return?:number;dca_total_return?:number;parameter_max_drawdown?:number;dca_max_drawdown?:number;relative?:{final_nav_ratio?:number;log_final_nav_ratio?:number;drawdown_residual_ratio?:number;log_drawdown_residual_ratio?:number;performance_drawdown_composite?:number;qualification?:string;unavailable_reason?:string}};performance_report_id?:number;error_message?:string};
export type AnalysisSnapshot={id:number;test_id:number;snapshot_key:string;analysis_set_hash:string;statistics_version:string;completeness:string;planned_count:number;valid_count:number;failed_count:number;missing_count:number;content_hash:string;metrics?:Array<{subject_id:number;alpha:string;metric_key:string;planned_count:number;valid_count:number;failed_count:number;missing_count:number;statistics:{available:boolean;count:number;mean:number;median:number;std_dev:number;minimum:number;maximum:number;p05:number;p25:number;p75:number;p95:number}}>;
qualifications?:Array<{subject_id:number;alpha:string;valid_count:number;qualified:number;return_failed_only:number;drawdown_failed_only:number;both_failed:number}>;created_at:string};

const root="/market-data/perturbations";
export const perturbationApi={
 sources:()=>apiFetch<PerturbationSource[]>(`${root}/sources`),
 planGroup:(source:{instrument_id?:string;version_id?:number;interval:string;start_time_ms:number;end_time_ms:number})=>apiFetch<GroupPlan>(`${root}/groups/plan`,{method:"POST",body:JSON.stringify({source})}),
 createGroup:(input:{plan_request:{source:{instrument_id?:string;version_id?:number;interval:string;start_time_ms:number;end_time_ms:number}};plan_hash:string;name:string;notes?:string;tags?:string[]})=>apiFetch<PerturbationGroup>(`${root}/groups`,{method:"POST",body:JSON.stringify(input)}),
 groups:()=>apiFetch<{items:PerturbationGroup[]}>(`${root}/groups`),
 group:(id:number)=>apiFetch<PerturbationGroup>(`${root}/groups/${id}`),
 planVariants:(id:number,input:{seeds:string[];seed_count?:number;alphas:string[]})=>apiFetch<VariantPlan>(`${root}/groups/${id}/variants/plan`,{method:"POST",body:JSON.stringify(input)}),
 startVariants:(id:number,planRequest:{seeds:string[];seed_count?:number;alphas:string[]},planHash:string,confirm=false)=>apiFetch<{plan:VariantPlan;task:ComputeTask;task_preview:ComputePlanPreview}>(`${root}/groups/${id}/variants`,{method:"POST",body:JSON.stringify({plan_request:planRequest,plan_hash:planHash,confirm_soft_limit:confirm})}),
 verifyVariant:(id:number)=>apiFetch<PerturbationVariant>(`${root}/variants/${id}/verify`,{method:"POST"}),
 planTest:(input:any)=>apiFetch<TestPlan>("/perturbation-tests/plan",{method:"POST",body:JSON.stringify(input)}),
 createTest:(input:any,planHash:string)=>apiFetch<PerturbationTest>("/perturbation-tests",{method:"POST",body:JSON.stringify({...input,plan_hash:planHash})}),
 tests:()=>apiFetch<{items:PerturbationTest[]}>("/perturbation-tests"),
 test:(id:number)=>apiFetch<PerturbationTest>(`/perturbation-tests/${id}`),
 planBatch:(id:number,input:{seeds?:string[];alphas?:string[]})=>apiFetch<BatchPlan>(`/perturbation-tests/${id}/batches/plan`,{method:"POST",body:JSON.stringify(input)}),
 startBatch:(id:number,plan:BatchPlan,confirm=false)=>apiFetch<{batch_id:number;plan:BatchPlan;task?:ComputeTask;task_preview?:ComputePlanPreview}>(`/perturbation-tests/${id}/batches`,{method:"POST",body:JSON.stringify({seeds:plan.seeds,alphas:plan.alphas,plan_hash:plan.plan_hash,confirm_soft_limit:confirm})}),
 runs:(id:number,limit=100,offset=0)=>apiFetch<{items:PerturbationRun[]}>(`/perturbation-tests/${id}/runs?limit=${limit}&offset=${offset}`),
 snapshots:(id:number)=>apiFetch<{items:AnalysisSnapshot[]}>(`/perturbation-tests/${id}/analysis-snapshots`),
 snapshot:(id:number,snapshotId:number)=>apiFetch<AnalysisSnapshot>(`/perturbation-tests/${id}/analysis-snapshots/${snapshotId}`)
};
