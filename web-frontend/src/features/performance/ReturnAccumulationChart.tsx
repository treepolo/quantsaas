import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { CartesianGrid, Line, LineChart, ReferenceLine, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";
import type { AccumulationChart } from "../../shared/services/performanceReports";
import { performanceReportsApi } from "../../shared/services/performanceReports";

export function ReturnAccumulationChart({ reportId }: { reportId: number }) {
  const [open, setOpen] = useState(false);
  const kind = "return_accumulation";
  const query = useQuery({
    queryKey: ["performance-report-chart", reportId, kind],
    queryFn: () => performanceReportsApi.chart<AccumulationChart>(reportId, kind),
    enabled: open
  });
  const rows = (query.data?.data.points ?? []).map((point) => ({ ...point, date: new Date(point.time_ms).toLocaleDateString("zh-TW", { timeZone: "UTC" }) }));
  return (
    <details className="rounded-lg border border-white/[0.06] bg-slate-950/30 p-4" onToggle={(event) => setOpen(event.currentTarget.open)}>
      <summary className="cursor-pointer text-sm font-semibold text-slate-200">日報酬累加走勢</summary>
      <div className="mt-2 text-xs text-slate-500">把每天的報酬率逐日相加，用來觀察每日漲跌的累積方向；這不是淨值或複利走勢。</div>
      {query.isLoading ? <div className="mt-4 text-sm text-slate-500">載入走勢資料中…</div> : null}
      {query.error ? <div className="mt-4 text-sm text-[#fecaca]">{String(query.error.message)}</div> : null}
      {rows.length > 0 ? (
        <div className="mt-4 h-72 rounded-lg border border-white/[0.04] bg-slate-950/40 p-2">
          <ResponsiveContainer width="100%" height="100%">
            <LineChart data={rows} margin={{ left: 4, right: 12, top: 8, bottom: 8 }}>
              <CartesianGrid stroke="rgba(148,163,184,0.08)" vertical={false} />
              <XAxis dataKey="date" stroke="#64748b" tickLine={false} axisLine={false} fontSize={10} minTickGap={36} />
              <YAxis stroke="#64748b" tickLine={false} axisLine={false} fontSize={11} tickFormatter={(value) => `${(Number(value) * 100).toFixed(0)}%`} />
              <Tooltip contentStyle={{ background: "#0f172a", border: "1px solid rgba(148,163,184,.2)", borderRadius: 8 }} formatter={(value) => `${(Number(value) * 100).toFixed(2)}%`} />
              <ReferenceLine y={0} stroke="rgba(148,163,184,.35)" strokeDasharray="4 4" />
              <Line type="linear" dataKey="arithmetic_sum" name="每日報酬逐日相加" stroke="#f59e0b" dot={false} isAnimationActive={false} />
            </LineChart>
          </ResponsiveContainer>
        </div>
      ) : null}
    </details>
  );
}
