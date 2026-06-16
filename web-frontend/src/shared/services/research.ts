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
  error?: string;
};

export type ResearchStatusResponse = {
  items: ResearchStatusItem[];
  updated_at: string;
};

export const researchApi = {
  status() {
    return apiFetch<ResearchStatusResponse>("/research/status");
  }
};
