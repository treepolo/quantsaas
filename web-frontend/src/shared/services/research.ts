import { apiFetch } from "./client";
import type { GenomeRecord } from "./evolution";
import type { ResearchInstrument } from "./marketData";

export type ResearchStatusItem = {
  status: "ready" | "missing_champion" | "missing_data" | string;
  instrument: ResearchInstrument;
  instrument_id: string;
  symbol: string;
  data_source: string;
  interval: string;
  execution_mode: string;
  champion?: GenomeRecord;
  latest_bar?: {
    open_time_ms: number;
    time: string;
    close: number;
  };
  market_state?: string;
  target_weight?: number;
  current_weight?: number;
  delta_weight?: number;
  empty_reference_target_weight?: number;
  empty_reference_target_weight_change?: number;
  diagnostics?: Record<string, number>;
  parameter_values?: Record<string, unknown>;
  model_simulation?: ResearchModelSimulation;
  position_simulation?: PositionSimulationSummary;
  interval_states?: ResearchIntervalState[];
  error?: string;
};

export type ResearchIntervalState = Omit<ResearchStatusItem, "instrument" | "instrument_id" | "symbol" | "data_source" | "interval_states" | "position_simulation">;

export type ResearchModelPoint = {
  time_ms: number;
  time: string;
  price?: number;
  model_nav: number;
  benchmark: number;
  model_nav_change_pct: number;
  benchmark_change_pct: number;
  model_target_weight: number;
  model_target_weight_change: number;
  empty_reference_target_weight: number;
  empty_reference_target_weight_change: number;
};

export type ResearchModelSimulation = {
  start_time_ms: number;
  latest_time_ms: number;
  latest_time: string;
  initial_capital: number;
  monthly_dca: number;
  latest_nav: number;
  previous_nav?: number;
  nav_change_pct: number;
  latest_benchmark: number;
  benchmark_change_pct: number;
  latest_model_target_weight: number;
  previous_model_target_weight?: number;
  latest_model_target_weight_change: number;
  latest_empty_reference_target_weight: number;
  previous_empty_reference_target_weight?: number;
  latest_empty_reference_target_weight_change: number;
  points: number;
  chart_points: ResearchModelPoint[];
};

export type PositionSimulationSummary = {
  start_time_ms: number;
  latest_time_ms: number;
  latest_time: string;
  initial_capital: number;
  monthly_dca: number;
  invested_capital: number;
  latest_nav: number;
  previous_nav: number;
  nav_change_pct: number;
  latest_contribution: number;
  latest_target_weight: number;
  previous_target_weight: number;
  target_weight_delta: number;
  latest_actual_weight: number;
  previous_actual_weight: number;
  cash_balance: number;
  asset_quantity: number;
  points: number;
};

export type ResearchStatusResponse = {
  items: ResearchStatusItem[];
  updated_at: string;
};

export type ResearchStatusInput = {
  instrument_id?: string;
  simulation_start_ms?: number;
  simulation_initial_capital?: number;
  simulation_monthly_dca?: number;
};

export const researchApi = {
  status(input: ResearchStatusInput = {}) {
    const params = new URLSearchParams();
    if (input.instrument_id) params.set("instrument_id", input.instrument_id);
    if (input.simulation_start_ms) params.set("simulation_start_ms", String(input.simulation_start_ms));
    if (input.simulation_initial_capital) params.set("simulation_initial_capital", String(input.simulation_initial_capital));
    if (input.simulation_monthly_dca !== undefined) params.set("simulation_monthly_dca", String(input.simulation_monthly_dca));
    const query = params.toString();
    return apiFetch<ResearchStatusResponse>(`/research/status${query ? `?${query}` : ""}`);
  }
};
