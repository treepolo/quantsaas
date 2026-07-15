import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Area, AreaChart, CartesianGrid, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";
import type { PerformanceSummary, UnderwaterChart } from "../../shared/services/performanceReports";
import { performanceReportsApi } from "../../shared/services/performanceReports";
import { StatTile, utcDate } from "./presentation";

export function UnderwaterStats({ reportId, summary, withChart }: { reportId: number; summary: PerformanceSummary; withChart: boolean }) {
  const [open, setOpen] = useState(false);
  const kind = "underwater";
  const query = useQuery({
    queryKey: ["performance-report-chart", reportId, kind],
    queryFn: () => performanceReportsApi.chart<UnderwaterChart>(reportId, kind),
    enabled: withChart && open
  });
  const stats = summary.longest_underwater;
  const rows = (query.data?.data.points ?? []).map((point) => ({ ...point, date: new Date(point.time_ms).toLocaleDateString("zh-TW", { timeZone: "UTC" }) }));
  return (
    <section>
      <h3 className="mb-3 text-sm font-semibold text-slate-300">最長水下期</h3>
      <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
        <StatTile label="最長天數" value={`${stats.longest_days.toLocaleString("zh-TW", { maximumFractionDigits: 2 })} 天`} />
        <StatTile label="觀測點數" value={stats.longest_points.toLocaleString("zh-TW")} />
        <StatTile label="開始日期" value={utcDate(stats.started_at_ms)} />
        <StatTile label="恢復日期" value={stats.recovery_completed ? utcDate(stats.recovered_at_ms) : "尚未恢復"} />
      </div>
      {withChart ? (
        <details className="mt-3 rounded-lg border border-white/[0.06] bg-slate-950/30 p-4" onToggle={(event) => setOpen(event.currentTarget.open)}>
          <summary className="cursor-pointer text-sm font-semibold text-slate-200">展開水下走勢</summary>
          {query.isLoading ? <div className="mt-4 text-sm text-slate-500">載入水下走勢中…</div> : null}
          {query.error ? <div className="mt-4 text-sm text-[#fecaca]">{String(query.error.message)}</div> : null}
          {rows.length > 0 ? (
            <div className="mt-4 h-64 rounded-lg border border-white/[0.04] bg-slate-950/40 p-2">
              <ResponsiveContainer width="100%" height="100%">
                <AreaChart data={rows} margin={{ left: 4, right: 12, top: 8, bottom: 8 }}>
                  <CartesianGrid stroke="rgba(148,163,184,0.08)" vertical={false} />
                  <XAxis dataKey="date" stroke="#64748b" tickLine={false} axisLine={false} fontSize={10} minTickGap={36} />
                  <YAxis stroke="#64748b" tickLine={false} axisLine={false} fontSize={11} tickFormatter={(value) => `${(Number(value) * 100).toFixed(0)}%`} />
                  <Tooltip contentStyle={{ background: "#0f172a", border: "1px solid rgba(148,163,184,.2)", borderRadius: 8 }} formatter={(value) => `${(Number(value) * 100).toFixed(2)}%`} />
                  <Area type="monotone" dataKey="drawdown" name="水下幅度" stroke="#fb7185" fill="#fb7185" fillOpacity={0.15} isAnimationActive={false} />
                </AreaChart>
              </ResponsiveContainer>
            </div>
          ) : null}
        </details>
      ) : null}
    </section>
  );
}
