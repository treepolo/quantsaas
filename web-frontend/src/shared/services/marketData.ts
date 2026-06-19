import { apiFetch } from "./client";

export type DatasetSummary = {
  instrument_id: string;
  data_source: string;
  symbol: string;
  market?: string;
  interval: string;
  count: number;
  preclose_snapshot_count?: number;
  first_preclose_ms?: number;
  last_preclose_ms?: number;
  first_open_ms?: number;
  last_open_ms?: number;
  expected_latest_open_ms?: number;
  is_fresh?: boolean;
  updated_at?: string;
  price_adjustment?: string;
  price_adjustment_label?: string;
  price_adjustment_note?: string;
  needs_full_reimport?: boolean;
  price_metadata_updated_at?: string;
};

export type ResearchInstrument = {
  id: string;
  symbol: string;
  display_name: string;
  data_source: "binance" | "yahoo" | string;
  supported_intervals: string[];
  market?: "tw" | "us" | "crypto" | string;
  sort_order?: number;
  enabled?: boolean;
  last_auto_update_at?: string;
  last_auto_update_error?: string;
};

export type MarketDataStatus = {
  instrument: ResearchInstrument;
  instrument_id: string;
  data_source: string;
  symbol: string;
  supported_intervals: string[];
  datasets: DatasetSummary[];
};

export type InstrumentSummary = {
  instrument: ResearchInstrument;
  datasets: DatasetSummary[];
};

export type UpsertInstrumentInput = {
  id?: string;
  symbol: string;
  display_name?: string;
  data_source?: string;
  supported_intervals?: string[];
  market?: string;
  sort_order?: number;
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
  price_adjustment?: string;
  price_adjustment_label?: string;
};

export type AutoUpdateResult = {
  instrument_id: string;
  data_source: string;
  symbol: string;
  interval: string;
  stored_bars: number;
  error?: string;
};

export const marketDataApi = {
  instruments() {
    return apiFetch<{ instruments: ResearchInstrument[]; execution_modes: string[] }>("/market-data/instruments");
  },
  upsertInstrument(input: UpsertInstrumentInput) {
    return apiFetch<ResearchInstrument>("/market-data/instruments", {
      method: "POST",
      body: JSON.stringify(input)
    });
  },
  deleteInstrument(id: string) {
    return apiFetch<{ status: string }>(`/market-data/instruments/${encodeURIComponent(id)}`, { method: "DELETE" });
  },
  reorderInstruments(ids: string[]) {
    return apiFetch<{ status: string }>("/market-data/instruments/order", {
      method: "PATCH",
      body: JSON.stringify({ ids })
    });
  },
  status(instrumentId = "BTCUSDT") {
    return apiFetch<MarketDataStatus>(`/market-data/klines/status?instrument_id=${encodeURIComponent(instrumentId)}`);
  },
  overview() {
    return apiFetch<{ items: InstrumentSummary[] }>("/market-data/klines/overview");
  },
  updateLatest() {
    return apiFetch<{ results: AutoUpdateResult[] }>("/market-data/klines/update-latest", { method: "POST" });
  },
  importKLines(input: ImportKLinesInput) {
    return apiFetch<ImportKLinesResult>("/market-data/klines/import", {
      method: "POST",
      body: JSON.stringify(input)
    });
  }
};
