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
  research_dataset_id?: number;
  status: "pending" | "running" | "completed" | "failed" | "cancelled";
  progress: number;
  current_generation?: number;
  max_generations?: number;
	search_algorithm?: "layered_grid" | "genetic";
	layered_local_percent?: number;
  pop_size?: number;
  pair?: string;
  instrument_id?: string;
  data_source?: string;
  execution_mode?: string;
  train_start_ms?: number;
  train_end_ms?: number;
  initial_capital?: number;
  monthly_dca?: number;
  evolve_rebalance_threshold?: boolean;
  evolve_force_full_threshold?: boolean;
  evolve_force_empty_threshold?: boolean;
  evolve_gamma?: boolean;
  enable_w_mean?: boolean;
  enable_w_momentum?: boolean;
  enable_w_breakout?: boolean;
  position_structure?: "dual_layer" | "floating_only";
  trade_penalty?: number;
  fee_rate?: number;
  spread_rate?: number;
  long_term_filter_enabled?: boolean;
  long_term_filter_months?: number;
  interval?: string;
  spawn_mode?: "inherit" | "random_once" | "manual";
  test_mode?: boolean;
  trace_mode?: TraceMode;
  compute_monitor_enabled?: boolean;
  computed_units?: number;
  planned_compute_units?: number;
  units_per_individual?: number;
  compute_units_per_sec?: number;
  compute_remaining_sec?: number;
  compute_started_at?: string;
  compute_updated_at?: string;
  continuous_mode?: "" | "standardized_best" | "random" | "initial_seed";
  current_iteration?: number;
  continuous_iterations?: number;
  continuous_unlimited?: boolean;
  standard_start_ms?: number;
  standard_end_ms?: number;
  standard_champion_gene_id?: number;
  standard_champion_score?: number;
  seed_gene_id?: number;
  fixed_param_keys?: string[];
	market_region_enabled?: boolean;
	market_region_max_thresholds?: number;
	multi_market_enabled?: boolean;
	multi_market_instrument_ids?: string[];
	multi_market_selections?: MultiMarketSelection[];
  best_score?: number | null;
  max_drawdown?: number;
  window_score?: Record<string, number>;
	market_performance?: MarketPerformance[];
  best_param_pack?: Record<string, unknown> | null;
  gene_record_id?: number;
  mutation_probability?: number;
  mutation_scale?: number;
  evaluated_individuals?: number;
  planned_evaluations?: number;
  search_axes?: LayeredAxisStatus[];
  search_status?: LayeredSearchStatus;
  monitor_updated_at?: string;
  error?: string;
  created_at: string;
  started_at?: string | null;
  finished_at?: string | null;
};

export type LayeredAxisStatus = {
  key: string;
  centre: number;
  lower: number;
  upper: number;
  step: number;
  minimum?: number;
  maximum?: number;
};

export type LayeredSearchStatus = {
  axes: LayeredAxisStatus[];
  next_axis?: string;
  next_side?: string;
  local_percent: number;
  issued: number;
  global_cursor: number;
  duplicate_skips: number;
  current_slice_remaining: number;
};

export type MarketPerformance = {
	instrument_id: string;
	pair: string;
	total_return: number;
	annualized_return: number;
	max_drawdown: number;
};

export type GenomeRecord = {
  id: number;
  role: "candidate" | "champion" | "archived" | "challenger" | "retired";
  strategy_id?: string;
  instrument_id?: string;
  data_source?: string;
  interval?: string;
  execution_mode?: string;
  name?: string;
  notes?: string;
  tags?: string[];
  search_config?: {
    strategy_id?: string;
    symbol?: string;
    instrument_id?: string;
    data_source?: string;
    interval?: string;
    execution_mode?: string;
    train_start_ms?: number;
    train_end_ms?: number;
    initial_capital?: number;
    monthly_dca?: number;
    evolve_rebalance_threshold?: boolean;
    evolve_force_full_threshold?: boolean;
    evolve_force_empty_threshold?: boolean;
    evolve_gamma?: boolean;
    enable_w_mean?: boolean;
    enable_w_momentum?: boolean;
    enable_w_breakout?: boolean;
    position_structure?: "dual_layer" | "floating_only";
    trade_penalty?: number;
    gene_options?: {
      EvolveRebalanceThreshold?: boolean;
      EvolveForceFullThreshold?: boolean;
      EvolveForceEmptyThreshold?: boolean;
      EvolveGamma?: boolean;
      EnableWMean?: boolean;
      EnableWMomentum?: boolean;
      EnableWBreakout?: boolean;
      PositionStructure?: "dual_layer" | "floating_only";
      FixedParamKeys?: string[];
      evolve_rebalance_threshold?: boolean;
    };
    fee_rate?: number;
    spread_rate?: number;
    long_term_filter_enabled?: boolean;
    long_term_filter_months?: number;
    long_term_filter_version?: string;
    spawn_mode?: string;
    population?: number;
    generations?: number;
    seed_gene_id?: number;
    fixed_param_keys?: string[];
  } | null;
  created_at: string;
  activated_at?: string | null;
  score_total: number;
  max_drawdown: number;
	window_score: Record<string, number>;
	market_performance?: MarketPerformance[];
  param_pack?: Record<string, unknown> | null;
};

export type CreateTaskInput = {
  strategy_id: string;
  research_dataset_id?: number;
  pair?: string;
  instrument_id?: string;
  data_source?: string;
  interval?: string;
  execution_mode?: string;
  train_start_ms?: number;
  train_end_ms?: number;
  monthly_dca?: number;
  evolve_rebalance_threshold?: boolean;
  evolve_force_full_threshold?: boolean;
  evolve_force_empty_threshold?: boolean;
  evolve_gamma?: boolean;
  enable_w_mean?: boolean;
  enable_w_momentum?: boolean;
  enable_w_breakout?: boolean;
  position_structure?: "dual_layer" | "floating_only";
  trade_penalty?: number;
  fee_rate?: number;
  spread_rate?: number;
  long_term_filter_enabled?: boolean;
  long_term_filter_months?: number;
  pop_size: number;
  max_generations: number;
	search_algorithm?: "layered_grid" | "genetic";
	layered_local_percent?: number;
  spawn_mode: "inherit" | "random_once" | "manual";
  spawn_point?: Record<string, unknown>;
  test_mode?: boolean;
  trace_mode?: TraceMode;
  compute_monitor_enabled?: boolean;
  continuous_mode?: "" | "standardized_best" | "random" | "initial_seed";
  continuous_iterations?: number;
  continuous_unlimited?: boolean;
  standard_start_ms?: number;
  standard_end_ms?: number;
  seed_gene_id?: number;
  fixed_param_keys?: string[];
	market_region_enabled?: boolean;
	market_region_max_thresholds?: number;
	multi_market_enabled?: boolean;
	multi_market_instrument_ids?: string[];
	multi_market_selections?: MultiMarketSelection[];
};

export type MultiMarketSelection = {
	instrument_id: string;
	use_all_data: boolean;
	start_time_ms?: number;
	end_time_ms?: number;
};

export type ComputeEstimate = {
  enabled: boolean;
  units_per_individual: number;
  planned_units: number;
};

export type GeneObservationAxis = {
  key: string;
  label: string;
  min: number;
  max: number;
};

export type GeneObservation = {
  id: number;
  created_at: string;
  task_id: number;
  generation: number;
  individual: number;
  fingerprint: string;
  score_total: number;
  max_drawdown: number;
  fatal: boolean;
  param_values: Record<string, number>;
  param_pack?: Record<string, unknown> | null;
  instrument_id?: string;
  data_source?: string;
  interval?: string;
  execution_mode?: string;
};

export type GeneObservationResponse = {
  schema: GeneObservationAxis[];
  observations: GeneObservation[];
  count: number;
};

export type GeneObservationQuery = {
  strategy_id?: string;
  instrument_id?: string;
  data_source?: string;
  interval?: string;
  execution_mode?: string;
  train_start_ms?: number;
  train_end_ms?: number;
  spawn_mode?: string;
  limit?: number;
};

export type ParameterGridPoint = { value: number; count: number };
export type ParameterGridAxis = { key: string; label: string; status: "演化中" | "固定中性化" | "停用"; min: number; max: number; points: ParameterGridPoint[] };
export type ParameterGridResponse = { task_id: number; axes: ParameterGridAxis[]; grid_point_count: number };

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
  estimateCompute(input: CreateTaskInput) {
    return apiFetch<ComputeEstimate>("/evolution/tasks/compute-estimate", {
      method: "POST",
      body: JSON.stringify(input)
    });
  },
  listGenomes() {
    return apiFetch<GenomeRecord[]>("/evolution/genomes");
  },
  listGeneObservations(input: GeneObservationQuery) {
    const params = new URLSearchParams();
    Object.entries(input).forEach(([key, value]) => {
      if (value !== undefined && value !== "" && value !== 0) params.set(key, String(value));
    });
    return apiFetch<GeneObservationResponse>(`/evolution/gene-observations?${params.toString()}`);
  },
  parameterGrid(taskId: number) {
    return apiFetch<ParameterGridResponse>(`/evolution/tasks/${taskId}/parameter-grid`);
  },
  updateGenome(genomeId: number, input: { name?: string; notes?: string; tags?: string[] }) {
    return apiFetch<{ status: string; genome: GenomeRecord }>(`/evolution/genomes/${genomeId}`, {
      method: "PATCH",
      body: JSON.stringify(input)
    });
  },
  deleteGenome(genomeId: number) {
    return apiFetch<{ status: string; id: number }>(`/evolution/genomes/${genomeId}`, { method: "DELETE" });
  },
  promote(genomeId: number) {
    return apiFetch<{ status: string; genome: GenomeRecord }>(`/evolution/tasks/${genomeId}/promote`, { method: "POST" });
  },
  cancel(taskId: number) {
    return apiFetch<{ status: string; task_id: number }>(`/evolution/tasks/${taskId}/cancel`, { method: "POST" });
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
