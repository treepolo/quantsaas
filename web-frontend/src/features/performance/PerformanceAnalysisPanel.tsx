import { useEffect, useMemo, useState, type FormEvent } from "react";
import { Link } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { BarChart3, ShieldCheck } from "lucide-react";
import type { ResearchInstrument } from "../../shared/services/marketData";
import type { PerformanceReport } from "../../shared/services/performanceReports";
import { performanceReportsApi } from "../../shared/services/performanceReports";
import { Button } from "../../shared/ui/Button";
import { Card, CardDescription, CardHeader, CardTitle } from "../../shared/ui/Card";
import { cn } from "../../shared/lib/cn";
import { BetaStats } from "./BetaStats";
import { ExposureStats } from "./ExposureStats";
import { RelativePerformanceStats } from "./RelativePerformanceStats";
import { ReturnAccumulationChart } from "./ReturnAccumulationChart";
import { ReturnDistributionChart } from "./ReturnDistributionChart";
import { UnderwaterStats } from "./UnderwaterStats";
import { finiteNumber, percent, reportDate, StatTile, utcDate } from "./presentation";

type Mode = "full" | "summary";

type Props = {
  mode?: Mode;
  backtestResultId?: number;
  report?: PerformanceReport;
  betaBenchmarks?: ResearchInstrument[];
};

const statusLabels: Record<PerformanceReport["status"], string> = {
  pending: "等待中",
  running: "計算中",
  completed: "已完成",
  failed: "失敗",
  cancelled: "已取消",
  invalidated: "版本或內容已失效",
  archived: "已封存"
};

function sourceValue(report: PerformanceReport, key: string) {
  const value = report.source_result.spec[key];
  if (typeof value === "number" || typeof value === "string") return String(value);
  return "-";
}

function shortHash(value?: string) {
  if (!value) return "-";
  return value.length > 18 ? `${value.slice(0, 10)}…${value.slice(-6)}` : value;
}

function AnalysisSummary({ report, mode }: { report: PerformanceReport; mode: Mode }) {
  if (!report.summary) {
    return <div className="rounded-lg border border-white/[0.05] bg-slate-950/30 p-4 text-sm text-slate-500">這份報告尚未產生可讀摘要。</div>;
  }
  const summary = report.summary;
  return (
    <div className="space-y-6">
      <RelativePerformanceStats summary={summary} />
      <section>
        <h3 className="mb-3 text-sm font-semibold text-slate-300">風險調整</h3>
        <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
          <StatTile label="Sortino" value={finiteNumber(summary.sortino.value)} note={summary.sortino.unavailable_reason || `${summary.sortino.observation_count.toLocaleString("zh-TW")} 個日觀測`} />
          <StatTile label="無風險年利率" value={percent(summary.sortino.risk_free_annual_rate)} />
          <StatTile label="觀測年化日數" value={finiteNumber(summary.sortino.periods_per_year, 2)} />
          <StatTile label="日報酬樣本" value={summary.distributions.daily.count.toLocaleString("zh-TW")} />
        </div>
      </section>
      <UnderwaterStats reportId={report.id} summary={summary} withChart={mode === "full"} />
      <ExposureStats reportId={report.id} summary={summary} withChart={mode === "full"} />
      <BetaStats report={report} />
      {mode === "full" ? <ReturnDistributionChart reportId={report.id} summary={summary} /> : null}
      {mode === "full" ? <ReturnAccumulationChart reportId={report.id} /> : null}
    </div>
  );
}
function ReportDetails({ report, mode }: { report: PerformanceReport; mode: Mode }) {
  const runID = report.source_result.backtest_run_ids[0];
  const startMs = Number(sourceValue(report, "start_time_ms"));
  const endMs = Number(sourceValue(report, "end_time_ms"));
  return (
    <div className="space-y-5">
      {report.status === "invalidated" ? <div className="rounded-lg border border-amber-300/20 bg-amber-300/5 p-3 text-sm text-[#fde68a]">這份報告的版本或內容驗證已失效；保留舊資料供追溯，但不應和有效報告混用。</div> : null}
      {report.error ? <div className="rounded-lg border border-red-300/20 bg-red-300/5 p-3 text-sm text-[#fecaca]">{report.error}</div> : null}
      <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-4">
        <StatTile label="來源標準結果" value={`#${report.backtest_result_id}`} note={runID ? <Link className="text-[#5eead4] hover:underline" to={`/backtesting?run=${runID}`}>回到原始回測 #{runID}</Link> : "目前帳號沒有可跳轉的執行紀錄"} />
        <StatTile label="研究條件" value={`${sourceValue(report, "instrument_id")} · ${sourceValue(report, "interval")}`} note={`${utcDate(startMs)}～${utcDate(endMs)}`} />
        <StatTile label="分析設定" value={`無風險利率 ${percent(report.settings.risk_free_annual_rate)}`} note={report.settings.beta_benchmark ? `Beta：${report.settings.beta_benchmark.symbol}` : "未選擇 Beta 基準"} />
        <StatTile label="完成時間" value={reportDate(report.completed_at)} note={report.reused ? "本次沿用相同設定的既有報告" : undefined} />
      </div>
      <AnalysisSummary report={report} mode={mode} />
      <details className="rounded-lg border border-white/[0.05] bg-slate-950/30 p-3">
        <summary className="cursor-pointer text-xs font-semibold text-slate-400">版本與驗證資訊</summary>
        <div className="mt-3 grid gap-2 text-xs text-slate-500 md:grid-cols-2">
          <div>報告 #{report.id} · {statusLabels[report.status]}</div>
          <div>分析版本：<span className="font-mono text-slate-300">{report.analysis_version}</span></div>
          <div>報告 schema：<span className="font-mono text-slate-300">{report.schema_version}</span></div>
          <div>來源結果版本：<span className="font-mono text-slate-300">{report.source_result.result_version}</span></div>
          <div title={report.content_hash}>內容 hash：<span className="font-mono text-slate-300">{shortHash(report.content_hash)}</span></div>
          <div title={report.settings_hash}>設定 hash：<span className="font-mono text-slate-300">{shortHash(report.settings_hash)}</span></div>
        </div>
      </details>
    </div>
  );
}

export function PerformanceAnalysisPanel({ mode = "full", backtestResultId, report: fixedReport, betaBenchmarks = [] }: Props) {
  const queryClient = useQueryClient();
  const [selectedReportId, setSelectedReportId] = useState<number>();
  const [riskFreePercent, setRiskFreePercent] = useState(0);
  const [histogramBins, setHistogramBins] = useState(20);
  const [betaBenchmark, setBetaBenchmark] = useState("");
  const reportsQuery = useQuery({
    queryKey: ["performance-reports", backtestResultId],
    queryFn: () => performanceReportsApi.list(backtestResultId!),
    enabled: mode === "full" && Boolean(backtestResultId) && !fixedReport
  });
  const reports = reportsQuery.data ?? [];
  useEffect(() => {
    if (!fixedReport && reports.length > 0 && !reports.some((item) => item.id === selectedReportId)) setSelectedReportId(reports[0].id);
  }, [fixedReport, reports, selectedReportId]);
  const selectedReport = fixedReport ?? reports.find((item) => item.id === selectedReportId) ?? reports[0];
  const dailyBenchmarks = useMemo(() => betaBenchmarks.filter((item) => item.enabled !== false && item.supported_intervals.includes("1d")), [betaBenchmarks]);
  const createMutation = useMutation({
    mutationFn: () => performanceReportsApi.create(backtestResultId!, {
      risk_free_annual_rate: riskFreePercent / 100,
      histogram_bins: histogramBins,
      beta_benchmark_instrument_id: betaBenchmark || undefined
    }),
    onSuccess: (created) => {
      setSelectedReportId(created.id);
      queryClient.invalidateQueries({ queryKey: ["performance-reports", backtestResultId] });
    }
  });

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (backtestResultId) createMutation.mutate();
  }

  if (mode === "summary") {
    return fixedReport ? <ReportDetails report={fixedReport} mode="summary" /> : <div className="text-sm text-slate-500">未計算報酬分析。</div>;
  }

  return (
    <Card data-testid="performance-analysis-panel">
      <CardHeader>
        <div>
          <CardTitle>報酬分析</CardTitle>
          <CardDescription>按需建立版本化報告；開啟頁面只讀既有摘要，圖表資料在展開時才載入。</CardDescription>
        </div>
        <BarChart3 className="h-5 w-5 text-[#2dd4bf]" />
      </CardHeader>
      <div className="space-y-5">
        <form className="rounded-lg border border-white/[0.06] bg-white/[0.02] p-4" onSubmit={submit}>
          <div className="mb-3 flex items-center gap-2 text-sm font-semibold text-slate-300"><ShieldCheck className="h-4 w-4" />建立新報告</div>
          <div className="grid gap-3 md:grid-cols-3">
            <label>
              <span className="mb-2 block text-xs text-slate-500">無風險年利率（%）</span>
              <input type="number" step="0.01" min="-99.99" className="h-10 w-full rounded-lg border border-slate-700 bg-slate-900/80 px-3 text-sm text-slate-100 outline-none focus:border-[#2dd4bf]" value={riskFreePercent} onChange={(event) => setRiskFreePercent(Number(event.target.value))} />
            </label>
            <label>
              <span className="mb-2 block text-xs text-slate-500">直方圖區間數</span>
              <input type="number" min="1" max="100" className="h-10 w-full rounded-lg border border-slate-700 bg-slate-900/80 px-3 text-sm text-slate-100 outline-none focus:border-[#2dd4bf]" value={histogramBins} onChange={(event) => setHistogramBins(Number(event.target.value))} />
            </label>
            <label>
              <span className="mb-2 block text-xs text-slate-500">Beta 基準（選填）</span>
              <select className="h-10 w-full rounded-lg border border-slate-700 bg-slate-900/80 px-3 text-sm text-slate-100 outline-none focus:border-[#2dd4bf]" value={betaBenchmark} onChange={(event) => setBetaBenchmark(event.target.value)}>
                <option value="">不計算 Beta</option>
                {dailyBenchmarks.map((item) => <option key={item.id} value={item.id}>{item.display_name}（{item.symbol}）</option>)}
              </select>
            </label>
          </div>
          <div className="mt-3 flex flex-wrap items-center gap-3">
            <Button type="submit" loading={createMutation.isPending} disabled={!backtestResultId}>建立報酬分析</Button>
            <span className="text-xs text-slate-500">相同標準結果與相同設定會沿用既有報告。</span>
          </div>
          {createMutation.error ? <div className="mt-3 text-sm text-[#fecaca]">{String(createMutation.error.message)}</div> : null}
        </form>

        {reportsQuery.isLoading ? <div className="text-sm text-slate-500">讀取既有報告中…</div> : null}
        {reportsQuery.error ? <div className="text-sm text-[#fecaca]">{String(reportsQuery.error.message)}</div> : null}
        {reports.length > 0 ? (
          <div>
            <div className="mb-2 text-xs text-slate-500">已保存報告</div>
            <div className="flex flex-wrap gap-2">
              {reports.map((item) => (
                <button key={item.id} type="button" className={cn("rounded-lg border px-3 py-2 text-left text-sm transition", selectedReport?.id === item.id ? "border-[#2dd4bf]/40 bg-[#2dd4bf]/10 text-[#99f6e4]" : "border-white/[0.06] text-slate-400 hover:text-slate-200")} onClick={() => setSelectedReportId(item.id)}>
                  <span className="font-mono">#{item.id}</span> · {statusLabels[item.status]} · {percent(item.settings.risk_free_annual_rate)}
                </button>
              ))}
            </div>
          </div>
        ) : !reportsQuery.isLoading ? <div className="rounded-lg border border-dashed border-white/[0.08] p-4 text-sm text-slate-500">尚未計算報酬分析。上方設定只會在按下「建立報酬分析」後執行。</div> : null}

        {selectedReport ? <ReportDetails report={selectedReport} mode="full" /> : null}
      </div>
    </Card>
  );
}

export function GenomePerformanceSummaryPanel({ genomeId }: { genomeId: number }) {
  const [open, setOpen] = useState(false);
  const query = useQuery({
    queryKey: ["genome-performance-report", genomeId],
    queryFn: () => performanceReportsApi.latestForGenome(genomeId),
    enabled: open
  });
  return (
    <details className="rounded-lg border border-white/[0.04] bg-slate-950/40 p-3" onToggle={(event) => setOpen(event.currentTarget.open)}>
      <summary className="cursor-pointer text-sm font-semibold text-slate-300">報酬分析摘要</summary>
      {query.isLoading ? <div className="mt-3 text-sm text-slate-500">讀取既有分析中…</div> : null}
      {query.error ? <div className="mt-3 text-sm text-[#fecaca]">{String(query.error.message)}</div> : null}
      {query.data?.report ? <div className="mt-4"><PerformanceAnalysisPanel mode="summary" report={query.data.report} /></div> : null}
      {query.data && !query.data.report ? (
        <div className="mt-3 rounded-lg border border-dashed border-white/[0.08] p-3 text-sm text-slate-500">
          未計算完整報酬分析，不影響既有參數評分。
          <div className="mt-2"><Link className="text-[#5eead4] hover:underline" to={query.data.backtest_run_id ? `/backtesting?run=${query.data.backtest_run_id}` : `/backtesting?genome=${genomeId}`}>前往來源回測建立報告</Link></div>
        </div>
      ) : null}
    </details>
  );
}
