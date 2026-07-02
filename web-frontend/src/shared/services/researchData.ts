import { apiFetch } from "./client";

export type MissingPolicy = "empty" | "forward_fill" | "linear";

export type IndicatorSelectionInput = {
  instrument_id: string;
  interval: string;
};

export type ResearchDatasetInput = {
  name?: string;
  notes?: string;
  primary_instrument_id: string;
  primary_interval: string;
  indicators: IndicatorSelectionInput[];
  start_time_ms: number;
  end_time_ms: number;
  missing_policy: MissingPolicy;
  indicator_algorithm?: string;
};

export type ResearchDatasetPreviewInput = ResearchDatasetInput;

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

export type ResearchDatasetSeries = {
  instrument_id: string;
  symbol: string;
  display_name: string;
  data_source: string;
  interval: string;
  sort_order: number;
};

export type ResearchDataset = {
  id: number;
  name: string;
  notes?: string;
  primary: ResearchDatasetSeries;
  indicators: ResearchDatasetSeries[];
  start_time_ms: number;
  end_time_ms: number;
  missing_policy: MissingPolicy;
  indicator_algorithm?: string;
  can_search: boolean;
  search_blocked_reason?: string;
  warnings?: string[];
  preview?: ResearchDatasetPreview;
  created_at: string;
  updated_at: string;
};

export const researchDataApi = {
  list() {
    return apiFetch<{ datasets: ResearchDataset[] }>("/research-datasets");
  },
  get(id: number, preview = false) {
    return apiFetch<ResearchDataset>(`/research-datasets/${id}${preview ? "?preview=1" : ""}`);
  },
  create(input: ResearchDatasetInput) {
    return apiFetch<ResearchDataset>("/research-datasets", {
      method: "POST",
      body: JSON.stringify(input)
    });
  },
  update(id: number, input: ResearchDatasetInput) {
    return apiFetch<ResearchDataset>(`/research-datasets/${id}`, {
      method: "PATCH",
      body: JSON.stringify(input)
    });
  },
  delete(id: number) {
    return apiFetch<{ status: string; id: number }>(`/research-datasets/${id}`, { method: "DELETE" });
  },
  preview(input: ResearchDatasetPreviewInput) {
    return apiFetch<ResearchDatasetPreview>("/research-datasets/preview", {
      method: "POST",
      body: JSON.stringify(input)
    });
  }
};
