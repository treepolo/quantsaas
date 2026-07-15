import { apiFetch } from "./client";

export type ComputeExecutor = {
  type: string;
  version: string;
  result_schema_version: string;
};

export type ComputeTaskStatus =
  | "planned"
  | "queued"
  | "running"
  | "partial"
  | "completed"
  | "failed"
  | "cancelled"
  | "invalidated";

export type ComputeTask = {
  id: number;
  user_id: number;
  parent_task_id?: number;
  kind: "atomic" | "composite" | "stage";
  task_type: string;
  title: string;
  plan_key: string;
  task_schema_version: string;
  lifecycle_version: string;
  executor: ComputeExecutor;
  settings_hash: string;
  settings?: unknown;
  research_setting_id?: string;
  research_setting_hash?: string;
  stage_key?: string;
  stage_type?: string;
  stage_order: number;
  manifest_version?: string;
  manifest_hash?: string;
  manifest?: unknown;
  total_items: number;
  estimated_units: number;
  unknown_unit_items: number;
  cache_hit_count: number;
  new_item_count: number;
  valid_result_count: number;
  failed_count: number;
  missing_count: number;
  cancelled_count: number;
  progress: number;
  status: ComputeTaskStatus;
  error?: string;
  attempt: number;
  rng_algorithm?: string;
  rng_version?: string;
  root_seed?: number;
  rng_position: number;
  checkpoint_hash?: string;
  dependency_task_ids: number[];
  child_task_ids: number[];
  created_at: string;
  updated_at: string;
  started_at?: string;
  completed_at?: string;
  cancelled_at?: string;
  cancel_requested_at?: string;
};

export type ComputeTaskSnapshot = {
  id: number;
  plan_key: string;
  task_schema_version: string;
  lifecycle_version: string;
  settings_hash: string;
  settings: unknown;
  research_setting_id?: string;
  research_setting_hash?: string;
  manifest_version: string;
  manifest_hash: string;
  manifest: unknown;
  checkpoint_hash?: string;
  checkpoint?: unknown;
};

export type ComputeTaskItem = {
  id: number;
  task_id: number;
  index: number;
  key: string;
  cache_key: string;
  input_hash: string;
  estimated_units: number;
  status: string;
  progress: number;
  attempt: number;
  cache_entry_id?: number;
  result?: unknown;
  result_hash?: string;
  error?: string;
  checkpoint_hash?: string;
  rng_position: number;
  started_at?: string;
  completed_at?: string;
  failed_at?: string;
  cancelled_at?: string;
};

export type ComputePlanPreview = {
  plan_key: string;
  stage_key?: string;
  stage_type?: string;
  stage_order: number;
  task_schema_version: string;
  lifecycle_version: string;
  executor: ComputeExecutor;
  manifest_version: string;
  manifest_hash: string;
  total_items: number;
  estimated_units: number;
  unknown_unit_items: number;
  cache_hit_count: number;
  new_item_count: number;
  soft_item_limit: number;
  hard_item_limit: number;
  requires_confirmation: boolean;
  estimated_seconds?: number;
};

export type ComputeLimits = {
  soft_item_limit: number;
  hard_item_limit: number;
  workers: number;
};

function queryString(values: Record<string, string | number | boolean | undefined>) {
  const query = new URLSearchParams();
  Object.entries(values).forEach(([key, value]) => {
    if (value !== undefined && value !== "") query.set(key, String(value));
  });
  const encoded = query.toString();
  return encoded ? `?${encoded}` : "";
}

export const computeTasksApi = {
  limits() {
    return apiFetch<ComputeLimits>("/compute-tasks/limits");
  },
  list(input: { status?: string; parentTaskId?: number; rootOnly?: boolean; limit?: number; offset?: number } = {}) {
    return apiFetch<ComputeTask[]>(
      `/compute-tasks${queryString({
        status: input.status,
        parent_task_id: input.parentTaskId,
        root_only: input.rootOnly,
        limit: input.limit,
        offset: input.offset
      })}`
    );
  },
  get(id: number) {
    return apiFetch<ComputeTask>(`/compute-tasks/${id}`);
  },
  snapshot(id: number) {
    return apiFetch<ComputeTaskSnapshot>(`/compute-tasks/${id}/snapshot`);
  },
  preview(id: number) {
    return apiFetch<ComputePlanPreview>(`/compute-tasks/${id}/preview`);
  },
  items(id: number, input: { status?: string; limit?: number; offset?: number; includeResult?: boolean } = {}) {
    return apiFetch<ComputeTaskItem[]>(
      `/compute-tasks/${id}/items${queryString({
        status: input.status,
        limit: input.limit,
        offset: input.offset,
        include_result: input.includeResult
      })}`
    );
  },
  start(id: number) {
    return apiFetch<ComputeTask>(`/compute-tasks/${id}/start`, { method: "POST" });
  },
  cancel(id: number) {
    return apiFetch<ComputeTask>(`/compute-tasks/${id}/cancel`, { method: "POST" });
  },
  retry(id: number) {
    return apiFetch<ComputeTask>(`/compute-tasks/${id}/retry`, { method: "POST" });
  },
  lookup(cacheKey: string) {
    return apiFetch<{ cache_key: string; found: boolean; content_hash?: string; result?: unknown; completed_at?: string }>(
      "/compute-cache/lookup",
      { method: "POST", body: JSON.stringify({ cache_key: cacheKey }) }
    );
  }
};
