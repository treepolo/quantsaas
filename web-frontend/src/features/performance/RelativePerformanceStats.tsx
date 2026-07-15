import type { PerformanceSummary } from "../../shared/services/performanceReports";
import { finiteNumber, percent, StatTile } from "./presentation";

export function RelativePerformanceStats({ summary }: { summary: PerformanceSummary }) {
  const relative = summary.relative_performance;
  return (
    <section>
      <h3 className="mb-3 text-sm font-semibold text-slate-300">相對績效</h3>
      <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-3">
        <StatTile label="期末淨值比" value={finiteNumber(relative.final_nav_ratio)} />
        <StatTile label="log 期末淨值比" value={finiteNumber(relative.log_final_nav_ratio)} />
        <StatTile label="策略無現金流年化" value={percent(relative.strategy_no_cash_flow_annualized)} note="由同條件、每月投入為 0 的標準回測計算" />
        <StatTile label="基準無現金流年化" value={percent(relative.benchmark_no_cash_flow_annualized)} note="由同一無現金流回測的基準路徑計算" />
        <StatTile label="無現金流年化差" value={percent(relative.no_cash_flow_annualized_difference)} tone={(relative.no_cash_flow_annualized_difference ?? 0) >= 0 ? "good" : "danger"} />
      </div>
    </section>
  );
}
