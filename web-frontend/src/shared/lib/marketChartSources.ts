import type { MarketChartSource, ResearchInstrument } from "../services/marketData";

export type MarketSourceCategory = "original" | "reference" | "leverage" | "recomposition" | "perturbation" | "other";

export const marketSourceCategoryOrder: MarketSourceCategory[] = ["original", "reference", "leverage", "recomposition", "perturbation", "other"];

export const marketSourceCategoryLabels: Record<MarketSourceCategory, string> = {
  original: "原始／匯入行情",
  reference: "參考指標",
  leverage: "倍數產生行情",
  recomposition: "片段重組行情",
  perturbation: "局部擾動行情",
  other: "其他研究行情"
};

export function marketSourceCategory(source: MarketChartSource): MarketSourceCategory {
  if (source.artifact_kind === "source_snapshot") return "original";
  if (source.artifact_kind === "reference_indicator") return "reference";
  if (source.artifact_kind === "daily_leverage") return "leverage";
  if (source.artifact_kind === "segment_recomposition") return "recomposition";
  if (source.artifact_kind === "local_perturbation") return "perturbation";
  return "other";
}

export function marketSourceKey(source: MarketChartSource) {
  return source.version_id ? `version:${source.version_id}` : `live:${source.instrument.id}:${source.interval}`;
}

export function marketSourceLabel(source: MarketChartSource) {
  const suffix = source.version_id ? ` · 資料版本 #${source.version_id}` : "";
  return `${source.display_name} · ${source.interval}${suffix}`;
}

export function groupMarketSources(sources: MarketChartSource[]) {
  return marketSourceCategoryOrder.flatMap((category) => {
    const items = sources.filter((source) => marketSourceCategory(source) === category);
    return items.length ? [{ category, label: marketSourceCategoryLabels[category], items }] : [];
  });
}

export function fallbackOriginalMarketSources(instruments: ResearchInstrument[], endTimeMs = Date.now()): MarketChartSource[] {
  return instruments.flatMap((instrument) => {
    if (instrument.data_source === "generated") return [];
    return instrument.supported_intervals.flatMap((interval) => {
      const startTimeMs = instrument.available_start_ms?.[interval] ?? 0;
      if (startTimeMs <= 0) return [];
      const reference = instrument.data_source === "fred";
      return [{
        instrument,
        artifact_kind: reference ? "reference_indicator" : "source_snapshot",
        display_name: instrument.display_name,
        interval,
        start_time_ms: startTimeMs,
        end_time_ms: Math.max(startTimeMs, endTimeMs),
        bar_count: 0,
        immutable: false,
        can_backtest: !reference
      } satisfies MarketChartSource];
    });
  });
}
