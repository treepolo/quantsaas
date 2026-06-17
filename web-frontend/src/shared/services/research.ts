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
  diagnostics?: Record<string, number>;
  parameter_values?: Record<string, unknown>;
  position_simulation?: PositionSimulationSummary;
  error?: string;
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
  latest_target_weight: number;
  previous_target_weight: number;
  target_weight_delta: number;
  cash_balance: number;
  asset_quantity: number;
  points: number;
};

export type ResearchStatusResponse = {
  items: ResearchStatusItem[];
  updated_at: string;
};

export type ResearchStatusInput = {
  simulation_start_ms?: number;
  simulation_initial_capital?: number;
  simulation_monthly_dca?: number;
};

export const researchApi = {
  status(input: ResearchStatusInput = {}) {
    const params = new URLSearchParams();
    if (input.simulation_start_ms) params.set("simulation_start_ms", String(input.simulation_start_ms));
    if (input.simulation_initial_capital) params.set("simulation_initial_capital", String(input.simulation_initial_capital));
    if (input.simulation_monthly_dca !== undefined) params.set("simulation_monthly_dca", String(input.simulation_monthly_dca));
    const query = params.toString();
    return apiFetch<ResearchStatusResponse>(`/research/status${query ? `?${query}` : ""}`);
  }
};
