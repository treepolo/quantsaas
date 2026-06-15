import { apiFetch } from "./client";

export type DatasetSummary = {
  symbol: string;
  interval: string;
  count: number;
  first_open_ms?: number;
  last_open_ms?: number;
  updated_at?: string;
};

export type MarketDataStatus = {
  symbol: string;
  supported_intervals: string[];
  datasets: DatasetSummary[];
};

export type ImportKLinesInput = {
  symbol: string;
  interval: string;
  start_time_ms: number;
  end_time_ms: number;
};

export type ImportKLinesResult = {
  symbol: string;
  interval: string;
  start_time_ms: number;
  end_time_ms: number;
  fetched_bars: number;
  stored_bars: number;
  first_open_ms?: number;
  last_open_ms?: number;
};

export const marketDataApi = {
  status(symbol = "BTCUSDT") {
    return apiFetch<MarketDataStatus>(`/market-data/klines/status?symbol=${encodeURIComponent(symbol)}`);
  },
  importKLines(input: ImportKLinesInput) {
    return apiFetch<ImportKLinesResult>("/market-data/klines/import", {
      method: "POST",
      body: JSON.stringify(input)
    });
  }
};
