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
  role: "candidate" | "champion" | "archived" | "challenger" | "retired";
  created_at: string;
  score_total: number;
  max_drawdown: number;
  window_score: Record<string, number>;
};

export type CreateTaskInput = {
  strategy_id: string;
  pop_size: number;
  max_generations: number;
  spawn_mode: "inherit" | "random_once" | "manual";
  spawn_point?: Record<string, unknown>;
  test_mode?: boolean;
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
  }
};
