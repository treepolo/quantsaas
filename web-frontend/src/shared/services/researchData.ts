import { apiFetch } from "./client";

export type MissingPolicy = "empty" | "forward_fill" | "linear";

export type IndicatorSelectionInput = {
  instrument_id: string;
  interval: string;
};

export type ResearchDatasetPreviewInput = {
  primary_instrument_id: string;
  primary_interval: string;
  indicators: IndicatorSelectionInput[];
  start_time_ms: number;
  end_time_ms: number;
  missing_policy: MissingPolicy;
  indicator_algorithm?: string;
};

export type SeriesPreview = {
  instrument_id: string;
  symbol: string;
  display_name: string;
  data_source: string;
  interval: string;
  raw_rows: number;
  aligned_rows: number;
  missing_rows: number;
  filled_rows: number;
  first_data_time_ms?: number;
  last_data_time_ms?: number;
  first_aligned_time_ms?: number;
  last_aligned_time_ms?: number;
  error?: string;
};

export type ResearchDatasetPreview = {
  primary: SeriesPreview;
  indicators: SeriesPreview[];
  missing_policy: MissingPolicy;
  start_time_ms: number;
  end_time_ms: number;
  aligned_rows: number;
  reference_count: number;
  can_search: boolean;
  search_blocked_reason?: string;
  warnings?: string[];
};

export const researchDataApi = {
  preview(input: ResearchDatasetPreviewInput) {
    return apiFetch<ResearchDatasetPreview>("/research-datasets/preview", {
      method: "POST",
      body: JSON.stringify(input)
    });
  }
};

