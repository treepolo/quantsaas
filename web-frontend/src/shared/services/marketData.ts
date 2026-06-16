import { apiFetch } from "./client";

export type DatasetSummary = {
  instrument_id: string;
  data_source: string;
  symbol: string;
  interval: string;
  count: number;
  preclose_snapshot_count?: number;
  first_preclose_ms?: number;
  last_preclose_ms?: number;
  first_open_ms?: number;
  last_open_ms?: number;
  updated_at?: string;
};

export type ResearchInstrument = {
  id: string;
  symbol: string;
  display_name: string;
  data_source: "binance" | "yahoo" | string;
  supported_intervals: string[];
};

export type MarketDataStatus = {
  instrument: ResearchInstrument;
  instrument_id: string;
  data_source: string;
  symbol: string;
  supported_intervals: string[];
  datasets: DatasetSummary[];
};

export type ImportKLinesInput = {
  instrument_id?: string;
  data_source?: string;
  symbol: string;
  interval: string;
  start_time_ms: number;
  end_time_ms: number;
  include_preclose_snapshots?: boolean;
};

export type ImportKLinesResult = {
  instrument_id: string;
  data_source: string;
  symbol: string;
  interval: string;
  start_time_ms: number;
  end_time_ms: number;
  fetched_bars: number;
  stored_bars: number;
  preclose_snapshot_count?: number;
  first_open_ms?: number;
  last_open_ms?: number;
};

export const marketDataApi = {
  instruments() {
    return apiFetch<{ instruments: ResearchInstrument[]; execution_modes: string[] }>("/market-data/instruments");
  },
  status(instrumentId = "BTCUSDT") {
    return apiFetch<MarketDataStatus>(`/market-data/klines/status?instrument_id=${encodeURIComponent(instrumentId)}`);
  },
  importKLines(input: ImportKLinesInput) {
    return apiFetch<ImportKLinesResult>("/market-data/klines/import", {
      method: "POST",
      body: JSON.stringify(input)
    });
  }
};
