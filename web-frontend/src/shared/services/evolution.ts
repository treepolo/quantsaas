import { apiFetch } from "./client";

export type TraceMode = "off" | "summary" | "detailed" | "full";

export type TraceEvent = {
  id: number;
  time: string;
  level: string;
  source: string;
  scope: string;
  message: string;
  fields?: Record<string, unknown>;
};

export type TraceSnapshot = {
  task_id: number;
  mode: TraceMode;
  events: TraceEvent[];
};

export type EvolutionTask = {
  id: number;
  strategy_id?: string;
  status: "pending" | "running" | "completed" | "failed";
  progress: number;
  current_generation?: number;
  max_generations?: number;
  pop_size?: number;
  pair?: string;
  interval?: string;
  spawn_mode?: "inherit" | "random_once" | "manual";
  test_mode?: boolean;
  trace_mode?: TraceMode;
  best_score?: number;
  max_drawdown?: number;
  window_score?: Record<string, number>;
  best_param_pack?: Record<string, unknown> | null;
  gene_record_id?: number;
  mutation_probability?: number;
  mutation_scale?: number;
  evaluated_individuals?: number;
  planned_evaluations?: number;
  monitor_updated_at?: string;
  error?: string;
  created_at: string;
  started_at?: string | null;
  finished_at?: string | null;
};

export type GenomeRecord = {
  id: number;
  role: "candidate" | "champion" | "archived" | "challenger" | "retired";
  created_at: string;
  score_total: number;
  max_drawdown: number;
  window_score: Record<string, number>;
  param_pack?: Record<string, unknown> | null;
};

export type CreateTaskInput = {
  strategy_id: string;
  interval?: string;
  pop_size: number;
  max_generations: number;
  spawn_mode: "inherit" | "random_once" | "manual";
  spawn_point?: Record<string, unknown>;
  test_mode?: boolean;
  trace_mode?: TraceMode;
};

export type EvolutionOverview = {
  current_task: EvolutionTask | null;
  running: boolean;
  tasks: EvolutionTask[];
  latest_challenger: GenomeRecord | null;
  champion: GenomeRecord | null;
  window_summaries: Record<string, number>;
};

export const evolutionApi = {
  listTasks() {
    return apiFetch<EvolutionOverview>("/evolution/tasks");
  },
  createTask(input: CreateTaskInput) {
    return apiFetch<EvolutionTask>("/evolution/tasks", {
      method: "POST",
      body: JSON.stringify(input)
    });
  },
  listGenomes() {
    return apiFetch<GenomeRecord[]>("/evolution/genomes");
  },
  promote(genomeId: number) {
    return apiFetch<{ status: string; genome: GenomeRecord }>(`/evolution/tasks/${genomeId}/promote`, { method: "POST" });
  },
  trace(taskId: number, limit = 500) {
    return apiFetch<TraceSnapshot>(`/evolution/tasks/${taskId}/trace?limit=${limit}`);
  },
  setTraceMode(taskId: number, traceMode: TraceMode) {
    return apiFetch<{ task_id: number; mode: TraceMode }>(`/evolution/tasks/${taskId}/trace-mode`, {
      method: "PATCH",
      body: JSON.stringify({ trace_mode: traceMode })
    });
  }
};
