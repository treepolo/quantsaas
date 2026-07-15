import type { PerformanceReport } from "../../shared/services/performanceReports";
import { finiteNumber, StatTile } from "./presentation";

export function BetaStats({ report }: { report: PerformanceReport }) {
  if (!report.summary) return null;
  const beta = report.summary.beta;
  const benchmark = report.settings.beta_benchmark;
  return (
    <section>
      <h3 className="mb-3 text-sm font-semibold text-slate-300">自選基準 Beta</h3>
      <div className="grid gap-3 sm:grid-cols-2">
        <StatTile label="Beta" value={finiteNumber(beta.value)} note={beta.unavailable_reason || (benchmark ? `${benchmark.symbol} · ${benchmark.data_source} · ${benchmark.interval}` : "本報告未選擇 Beta 基準")} />
        <StatTile label="對齊日數" value={beta.observation_count.toLocaleString("zh-TW")} note={beta.formula_version} />
      </div>
    </section>
  );
}
