import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Area, AreaChart, CartesianGrid, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";
import type { ExposureChart, PerformanceSummary } from "../../shared/services/performanceReports";
import { performanceReportsApi } from "../../shared/services/performanceReports";
import { percent, StatTile } from "./presentation";

export function ExposureStats({ reportId, summary, withChart }: { reportId: number; summary: PerformanceSummary; withChart: boolean }) {
  const [open, setOpen] = useState(false);
  const kind = "exposure";
  const query = useQuery({
    queryKey: ["performance-report-chart", reportId, kind],
    queryFn: () => performanceReportsApi.chart<ExposureChart>(reportId, kind),
    enabled: withChart && open
  });
  const stats = summary.exposure;
  const rows = (query.data?.data.points ?? []).map((point) => ({ ...point, date: new Date(point.time_ms).toLocaleDateString("zh-TW", { timeZone: "UTC" }) }));
  return (
    <section>
      <h3 className="mb-3 text-sm font-semibold text-slate-300">實際曝險</h3>
      <div className="grid gap-3 sm:grid-cols-3">
        <StatTile label="持倉天數比例" value={percent(stats.exposure_days_ratio)} />
        <StatTile label="平均實際倉位" value={percent(stats.average_actual_exposure)} />
        <StatTile label="曝險調整報酬" value={stats.exposure_adjusted_readable ? percent(stats.exposure_adjusted_return) : "不可解讀"} note={stats.exposure_adjusted_readable ? undefined : "平均實際倉位為 0"} />
      </div>
      {withChart ? (
        <details className="mt-3 rounded-lg border border-white/[0.06] bg-slate-950/30 p-4" onToggle={(event) => setOpen(event.currentTarget.open)}>
          <summary className="cursor-pointer text-sm font-semibold text-slate-200">展開實際倉位走勢</summary>
          {query.isLoading ? <div className="mt-4 text-sm text-slate-500">載入倉位走勢中…</div> : null}
          {query.error ? <div className="mt-4 text-sm text-[#fecaca]">{String(query.error.message)}</div> : null}
          {rows.length > 0 ? (
            <div className="mt-4 h-64 rounded-lg border border-white/[0.04] bg-slate-950/40 p-2">
              <ResponsiveContainer width="100%" height="100%">
                <AreaChart data={rows} margin={{ left: 4, right: 12, top: 8, bottom: 8 }}>
                  <CartesianGrid stroke="rgba(148,163,184,0.08)" vertical={false} />
                  <XAxis dataKey="date" stroke="#64748b" tickLine={false} axisLine={false} fontSize={10} minTickGap={36} />
                  <YAxis domain={[0, "auto"]} stroke="#64748b" tickLine={false} axisLine={false} fontSize={11} tickFormatter={(value) => `${(Number(value) * 100).toFixed(0)}%`} />
                  <Tooltip contentStyle={{ background: "#0f172a", border: "1px solid rgba(148,163,184,.2)", borderRadius: 8 }} formatter={(value) => `${(Number(value) * 100).toFixed(2)}%`} />
                  <Area type="stepAfter" dataKey="actual_exposure_weight" name="實際倉位" stroke="#38bdf8" fill="#38bdf8" fillOpacity={0.16} isAnimationActive={false} />
                </AreaChart>
              </ResponsiveContainer>
            </div>
          ) : null}
        </details>
      ) : null}
    </section>
  );
}
