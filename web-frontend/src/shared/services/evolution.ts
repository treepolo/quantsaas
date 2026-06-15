import { apiFetch } from "./client";

export type EvolutionTask = {
  id: number;
  status: "pending" | "running" | "completed" | "failed";
  progress: number;
  current_generation?: number;
  max_generations?: number;
  best_score?: number;
  max_drawdown?: number;
  created_at: string;
  started_at?: string | null;
  finished_at?: string | null;
};

export type GenomeRecord = {
  id: number;
  role: "candidate" | "champion" | "archived" | "challenger";
  created_at: string;
  score_total: number;
  max_drawdown: number;
  window_score: Record<string, number>;
};

export type CreateTaskInput = {
  strategy_id: string;
  population_size: number;
  max_generations: number;
  inherit_mode: "champion" | "random" | "manual";
  manual_params?: Record<string, unknown>;
};

export const evolutionApi = {
  listTasks() {
    return apiFetch<EvolutionTask[]>("/evolution/tasks");
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
  promote(taskId: number) {
    return apiFetch<{ status: string }>(`/evolution/tasks/${taskId}/promote`, { method: "POST" });
  }
};
