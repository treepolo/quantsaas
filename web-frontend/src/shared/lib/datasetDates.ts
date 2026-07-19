import type { ResearchInstrument } from "../services/marketData";

export function dateInputFromMs(ms: number) {
  return ms > 0 ? new Date(ms).toISOString().slice(0, 10) : "";
}

export function datasetStartDate(instrument?: ResearchInstrument, interval = "1d", fallback = "") {
  const starts = instrument?.available_start_ms;
  const exact = starts?.[interval];
  if (exact && exact > 0) return dateInputFromMs(exact);
  const earliest = Object.values(starts ?? {}).filter((value) => Number.isFinite(value) && value > 0);
  return earliest.length ? dateInputFromMs(Math.min(...earliest)) : fallback;
}
