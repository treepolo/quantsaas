import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Bar, BarChart, CartesianGrid, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";
import type { HistogramChart, PerformanceSummary } from "../../shared/services/performanceReports";
import { performanceReportsApi } from "../../shared/services/performanceReports";
import { cn } from "../../shared/lib/cn";
import { finiteNumber, percent, StatTile } from "./presentation";

type Period = "daily" | "weekly" | "monthly";

const periods: Array<{ value: Period; label: string; kind: string }> = [
  { value: "daily", label: "日", kind: "return_distribution_daily" },
  { value: "weekly", label: "週", kind: "return_distribution_weekly" },
  { value: "monthly", label: "月", kind: "return_distribution_monthly" }
];

export function ReturnDistributionChart({ reportId, summary }: { reportId: number; summary: PerformanceSummary }) {
  const [open, setOpen] = useState(false);
  const [period, setPeriod] = useState<Period>("daily");
  const selected = periods.find((item) => item.value === period)!;
  const query = useQuery({
    queryKey: ["performance-report-chart", reportId, selected.kind],
    queryFn: () => performanceReportsApi.chart<HistogramChart>(reportId, selected.kind),
    enabled: open
  });
  const stats = summary.distributions[period];
  const rows = (query.data?.data.bins ?? []).map((bin) => ({
    range: `${percent(bin.lower, 1)}～${percent(bin.upper, 1)}`,
    count: bin.count
  }));

  return (
    <details className="rounded-lg border border-white/[0.06] bg-slate-950/30 p-4" onToggle={(event) => setOpen(event.currentTarget.open)}>
      <summary className="cursor-pointer text-sm font-semibold text-slate-200">日／週／月報酬分佈</summary>
      <div className="mt-4 space-y-4">
        <div className="flex w-fit rounded-lg border border-white/[0.06] bg-white/[0.03] p-1">
          {periods.map((item) => (
            <button key={item.value} type="button" className={cn("rounded-md px-3 py-1.5 text-sm", period === item.value ? "bg-[#2dd4bf]/15 text-[#99f6e4]" : "text-slate-500")} onClick={() => setPeriod(item.value)}>
              {item.label}報酬
            </button>
          ))}
        </div>
        <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
          <StatTile label="樣本數" value={stats.count.toLocaleString("zh-TW")} />
          <StatTile label="平均值" value={percent(stats.mean)} />
          <StatTile label="中位數" value={percent(stats.median)} />
          <StatTile label="標準差" value={percent(stats.std_dev)} />
          <StatTile label="偏態" value={finiteNumber(stats.skewness)} />
          <StatTile label="超額峰度" value={finiteNumber(stats.excess_kurtosis)} />
          <StatTile label="最小／最大" value={`${percent(stats.minimum)} / ${percent(stats.maximum)}`} />
          <StatTile label="P05／P95" value={`${percent(stats.quantiles.p05)} / ${percent(stats.quantiles.p95)}`} />
        </div>
        {query.isLoading ? <div className="text-sm text-slate-500">載入直方圖資料中…</div> : null}
        {query.error ? <div className="text-sm text-[#fecaca]">{String(query.error.message)}</div> : null}
        {rows.length > 0 ? (
          <div className="h-72 rounded-lg border border-white/[0.04] bg-slate-950/40 p-2">
            <ResponsiveContainer width="100%" height="100%">
              <BarChart data={rows} margin={{ left: 4, right: 8, top: 8, bottom: 24 }}>
                <CartesianGrid stroke="rgba(148,163,184,0.08)" vertical={false} />
                <XAxis dataKey="range" stroke="#64748b" tickLine={false} axisLine={false} fontSize={10} interval="preserveStartEnd" />
                <YAxis allowDecimals={false} stroke="#64748b" tickLine={false} axisLine={false} fontSize={11} />
                <Tooltip contentStyle={{ background: "#0f172a", border: "1px solid rgba(148,163,184,.2)", borderRadius: 8 }} />
                <Bar dataKey="count" name="筆數" fill="#2dd4bf" radius={[3, 3, 0, 0]} isAnimationActive={false} />
              </BarChart>
            </ResponsiveContainer>
          </div>
        ) : null}
      </div>
    </details>
  );
}
