import { apiFetch } from "./client";
import type { ComputePlanPreview, ComputeTask } from "./computeTasks";

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
  available_start_ms?: Record<string, number>;
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

export type GenerateLeveragedInput = {
  source_instrument_id: string;
  source_interval: string;
  start_time_ms: number;
  end_time_ms: number;
  multiplier: number;
  target_instrument_id: string;
  target_symbol: string;
  target_display_name?: string;
};

export type GenerateLeveragedResult = {
  instrument: ResearchInstrument;
  source_instrument_id: string;
  source_data_source: string;
  source_symbol: string;
  interval: string;
  multiplier: number;
  generated_bars: number;
  stored_bars: number;
  first_open_ms?: number;
  last_open_ms?: number;
  used_fallback_baseline?: boolean;
  price_adjustment?: string;
  price_adjustment_label?: string;
};

export type AutoUpdateResult = {
  instrument_id: string;
  data_source: string;
  symbol: string;
  interval: string;
  stored_bars: number;
  skipped?: boolean;
  reason?: string;
  last_open_ms?: number;
  expected_latest_open_ms?: number;
  error?: string;
};

export type AvailableStartResult = {
  instrument_id: string;
  data_source: string;
  symbol: string;
  starts: Record<string, number>;
  errors?: Record<string, string>;
};

export type MaintenanceDatasetResult = {
  interval: string;
  count: number;
  expected_count?: number;
  invalid_open_time_count: number;
  needs_full_reimport: boolean;
  price_adjustment?: string;
  price_adjustment_label?: string;
  reimported_daily?: boolean;
  rebuilt_from_daily?: boolean;
  stored_bars?: number;
  deleted_rows?: number;
  first_open_ms?: number;
  last_open_ms?: number;
  expected_first_open_ms?: number;
  expected_last_open_ms?: number;
  error?: string;
};

export type MaintenanceResult = {
  instrument_id: string;
  data_source: string;
  symbol: string;
  datasets: MaintenanceDatasetResult[];
  has_issues: boolean;
  error?: string;
};

export type MarketVersionBar = {
  ordinal: number;
  open_time: number;
  open: number;
  high: number;
  low: number;
  close: number;
  volume: number;
};

export type RecompositionSource = {
  instrument: ResearchInstrument;
  version_id?: number;
  content_hash?: string;
  artifact_kind: string;
  immutable: boolean;
  integrity_status?: string;
  archived: boolean;
};

export type MarketChartSource = {
  instrument: ResearchInstrument;
  version_id?: number;
  version_number?: number;
  content_hash?: string;
  artifact_kind: string;
  display_name: string;
  series_name?: string;
  interval: string;
  start_time_ms: number;
  end_time_ms: number;
  bar_count: number;
  immutable: boolean;
  integrity_status?: string;
  can_backtest: boolean;
};

export type RecompositionSegmentInput = {
  item_id: string;
  source_instrument_id?: string;
  source_version_id?: number;
  start_time_ms: number;
  end_time_ms: number;
  repeat_count: number;
};

export type RecompositionPreviewInput = {
  segments: RecompositionSegmentInput[];
  interval: string;
  calendar_instrument_id?: string;
  calendar_source_version_id?: number;
  output_start_time_ms: number;
};

export type RecompositionPreviewTask = {
  task: ComputeTask;
  task_preview: ComputePlanPreview;
  total_read_bars: number;
  total_output_bars: number;
  estimated_bytes: number;
};

export type SegmentInstance = {
  instance_id: string;
  segment_item_id: string;
  order: number;
  repeat_ordinal: number;
  source_version_id: number;
  output_start_time_ms: number;
  output_end_time_ms: number;
  scale_multiplier: number;
  source_gap_ratio?: number;
  actual_gap_ratio: number;
  anchor_missing: boolean;
};

export type RecompositionPlan = {
  schema_version: string;
  plan_id: number;
  plan_hash: string;
  content_hash: string;
  interval: string;
  target_market: string;
  target_timezone: string;
  calendar_version_id: number;
  calendar_hash: string;
  output_start_time_ms: number;
  output_end_time_ms: number;
  segment_count: number;
  instance_count: number;
  total_output_bars: number;
  estimated_read_bars: number;
  estimated_write_bars: number;
  estimated_bytes: number;
  anchor_warning_count: number;
  instances: SegmentInstance[];
  segments: Array<{
    item_id: string;
    order: number;
    source_version_id: number;
    source_instrument_id: string;
    source_symbol: string;
    source_display_name: string;
    source_content_hash: string;
    start_time_ms: number;
    end_time_ms: number;
    bar_count: number;
    repeat_count: number;
    previous_close_present: boolean;
    source_gap_ratio?: number;
  }>;
  created_at: string;
};

export type RecompositionGeneration = {
  schema_version: string;
  generation_id: number;
  plan_id: number;
  plan_hash: string;
  series_id: number;
  series_name: string;
  version_id: number;
  version_number: number;
  output_instrument_id?: string;
  content_hash?: string;
  status: string;
  integrity_status: string;
  published: boolean;
  compute_task_id?: number;
  expanded_at?: string;
  calendar_checked_at?: string;
  published_at?: string;
};

export type RecompositionGenerationTask = {
  generation: RecompositionGeneration;
  task: ComputeTask;
  task_preview: { stages: ComputePlanPreview[]; requires_confirmation: boolean; estimated_units: number };
};

export type MarketSeries = {
  id: number;
  name: string;
  notes?: string;
  tags: string[];
  archived: boolean;
  created_at: string;
  versions: Array<{
    id: number;
    version_number: number;
    content_hash: string;
    plan_hash: string;
    instrument_id: string;
    interval: string;
    bar_count: number;
    start_time_ms: number;
    end_time_ms: number;
    status: string;
    integrity_status: string;
    published: boolean;
    archived: boolean;
    created_at: string;
  }>;
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
  refreshInstrumentStarts(id: string) {
    return apiFetch<AvailableStartResult>(`/market-data/instruments/${encodeURIComponent(id)}/refresh-starts`, { method: "POST" });
  },
  refreshAllInstrumentStarts() {
    return apiFetch<{ results: AvailableStartResult[] }>("/market-data/instruments/refresh-starts", { method: "POST" });
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
  chartSources() {
    return apiFetch<{ items: MarketChartSource[] }>("/market-data/charts/sources");
  },
  chartBars(input: { instrumentId?: string; versionId?: number; interval: string; startTimeMs: number; endTimeMs: number; limit?: number }) {
    const query = new URLSearchParams({
      interval: input.interval,
      start_time_ms: String(input.startTimeMs),
      end_time_ms: String(input.endTimeMs),
      limit: String(input.limit ?? 5000)
    });
    if (input.instrumentId) query.set("instrument_id", input.instrumentId);
    if (input.versionId) query.set("version_id", String(input.versionId));
    return apiFetch<{ rows: MarketVersionBar[] }>(`/market-data/charts/bars?${query.toString()}`);
  },
  auditMaintenance(id?: string) {
    const suffix = id ? `/${encodeURIComponent(id)}` : "";
    return apiFetch<{ results: MaintenanceResult[] }>(`/market-data/maintenance/audit${suffix}`, { method: "POST" });
  },
  repairMaintenance(id?: string) {
    const suffix = id ? `/${encodeURIComponent(id)}` : "";
    return apiFetch<{ results: MaintenanceResult[] }>(`/market-data/maintenance/repair${suffix}`, { method: "POST" });
  },
  importKLines(input: ImportKLinesInput) {
    return apiFetch<ImportKLinesResult>("/market-data/klines/import", {
      method: "POST",
      body: JSON.stringify(input)
    });
  },
  generateLeveraged(input: GenerateLeveragedInput) {
    return apiFetch<GenerateLeveragedResult>("/market-data/generate/leveraged", {
      method: "POST",
      body: JSON.stringify(input)
    });
  },
  recompositionSources() {
    return apiFetch<{ items: RecompositionSource[] }>("/market-data/recomposition/sources");
  },
  recompositionSourceBars(input: { instrumentId?: string; versionId?: number; interval: string; startTimeMs: number; endTimeMs: number; limit?: number }) {
    const query = new URLSearchParams({
      interval: input.interval,
      start_time_ms: String(input.startTimeMs),
      end_time_ms: String(input.endTimeMs),
      limit: String(input.limit ?? 2000)
    });
    if (input.instrumentId) query.set("instrument_id", input.instrumentId);
    if (input.versionId) query.set("version_id", String(input.versionId));
    return apiFetch<{ rows: MarketVersionBar[] }>(`/market-data/recomposition/source-bars?${query.toString()}`);
  },
  createRecompositionPreview(request: RecompositionPreviewInput, confirmSoftLimit = false) {
    return apiFetch<RecompositionPreviewTask>("/market-data/recomposition/preview-tasks", {
      method: "POST",
      body: JSON.stringify({ request, confirm_soft_limit: confirmSoftLimit })
    });
  },
  recompositionPlan(id: number) {
    return apiFetch<RecompositionPlan>(`/market-data/recomposition/plans/${id}`);
  },
  recompositionPreviewBars(id: number, limit = 2000, offset = 0) {
    return apiFetch<{ rows: MarketVersionBar[]; total: number; limit: number; offset: number }>(
      `/market-data/recomposition/plans/${id}/bars?limit=${limit}&offset=${offset}`
    );
  },
  createRecompositionGeneration(input: { plan_id: number; plan_hash: string; series_name: string; notes?: string; tags?: string[]; idempotency_key?: string }, confirmSoftLimit = false) {
    return apiFetch<RecompositionGenerationTask>("/market-data/recomposition/generations", {
      method: "POST",
      body: JSON.stringify({ request: input, confirm_soft_limit: confirmSoftLimit })
    });
  },
  recompositionGeneration(id: number) {
    return apiFetch<RecompositionGeneration>(`/market-data/recomposition/generations/${id}`);
  },
  marketSeries(includeArchived = false) {
    return apiFetch<{ items: MarketSeries[] }>(`/market-data/series?include_archived=${includeArchived}`);
  },
  archiveMarketSeries(id: number) {
    return apiFetch<{ status: string }>(`/market-data/series/${id}`, { method: "DELETE" });
  },
  archiveMarketVersion(id: number) {
    return apiFetch<{ status: string }>(`/market-data/versions/${id}`, { method: "DELETE" });
  }
};
