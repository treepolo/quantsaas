import { FormEvent, useEffect, useMemo, useState, type MouseEvent as ReactMouseEvent, type HTMLAttributes } from "react";
import { useSearchParams } from "react-router-dom";
import { useMutation, useQuery } from "@tanstack/react-query";
import { Area, AreaChart, CartesianGrid, Legend, ReferenceLine, ResponsiveContainer, XAxis, YAxis } from "recharts";
import { BarChart3, PlayCircle, RotateCcw, ZoomIn } from "lucide-react";
import { formatMoney, formatPercent } from "../../shared/lib/format";
import { backtestsApi, type BacktestResult } from "../../shared/services/backtests";
import { evolutionApi, type GenomeRecord } from "../../shared/services/evolution";
import { marketDataApi } from "../../shared/services/marketData";
import { Button } from "../../shared/ui/Button";
import { Card, CardDescription, CardHeader, CardTitle } from "../../shared/ui/Card";
import { ChartRangeSlider } from "../../shared/ui/ChartRangeSlider";
import { cn } from "../../shared/lib/cn";

type ScaleMode = "absolute" | "log";
type ValueMode = "nav" | "relative";
type ChartRange = { start: number; end: number };
type ChartPoint = {
  label: string;
  time_ms: number;
  time: string;
  price?: number;
  strategy?: number;
  benchmark?: number;
  strategy_value?: number;
  benchmark_value?: number;
  strategy_change_pct?: number;
  benchmark_change_pct?: number;
  model_target_weight?: number;
  model_target_weight_change?: number;
  empty_reference_target_weight?: number;
  empty_reference_target_weight_change?: number;
  model_target_weight_value?: number;
  model_target_weight_change_value?: number;
  empty_reference_target_weight_value?: number;
  empty_reference_target_weight_change_value?: number;
  [key: string]: string | number | undefined;
};
type ComparisonResult = { genome: GenomeRecord; result: BacktestResult; color: string };
type SeriesDef = { key: string; dataKey: string; name: string; color: string };
type MetricSeriesDef = { dataKey: string; name: string; color: string };

const executionModes = [
  ["close_same_bar", "收盤同根"],
  ["close_next_open", "隔日開盤"],
  ["preclose_10m", "收盤前 10 分鐘"]
] as const;

const intervalLabels: Record<string, string> = {
  "1d": "日 K",
  "1h": "1 小時",
  "15m": "15 分鐘",
  "5m": "5 分鐘",
  "1m": "1 分鐘",
  "1s": "1 秒",
  "1w": "週 K",
  "1M": "月 K"
};

const dayMs = 24 * 60 * 60 * 1000;
const comparisonColors = ["#2dd4bf", "#f59e0b", "#a78bfa", "#38bdf8", "#f472b6", "#84cc16", "#fb7185", "#e2e8f0"];

function roleLabel(role: GenomeRecord["role"]) {
  if (role === "champion") return "已採用";
  if (role === "retired" || role === "archived") return "已封存";
  return "候選";
}

function windowLabel(key: string) {
  const map: Record<string, string> = { "6m": "6 個月", "2y": "2 年", "5y": "5 年", "10y": "完整歷史" };
  return map[key] ?? key;
}

function formatAxisTime(value: string) {
  return new Intl.DateTimeFormat("zh-TW", { year: "2-digit", month: "2-digit", day: "2-digit" }).format(new Date(value));
}

function formatFullAxisTime(value: number | string) {
  return new Intl.DateTimeFormat("zh-TW", { year: "numeric", month: "2-digit", day: "2-digit" }).format(new Date(Number(value)));
}

function formatTick(value: number | string, mode: "year" | "month" | "day") {
  const date = new Date(Number(value));
  if (mode === "year") return new Intl.DateTimeFormat("zh-TW", { year: "numeric" }).format(date);
  if (mode === "month") return new Intl.DateTimeFormat("zh-TW", { year: "2-digit", month: "2-digit" }).format(date);
  return new Intl.DateTimeFormat("zh-TW", { month: "2-digit", day: "2-digit" }).format(date);
}

function genomeLabel(genome: GenomeRecord, instrumentNames: Record<string, string>) {
  const instrument = instrumentNames[genome.instrument_id ?? ""] ?? genome.instrument_id ?? "未知標的";
  return `#${genome.id} - ${roleLabel(genome.role)} - ${instrument} - ${genome.score_total.toFixed(3)}`;
}

function shortGenomeLabel(genome: GenomeRecord) {
  return genome.name?.trim() || `#${genome.id}`;
}

function buildSingleChartData(result: BacktestResult | null): ChartPoint[] {
  return (result?.nav ?? []).map((item) => ({
    label: formatAxisTime(item.time),
    time_ms: new Date(item.time).getTime(),
    time: item.time,
    price: item.price,
    strategy: item.total_assets,
    benchmark: item.benchmark ?? item.total_assets,
    strategy_change_pct: item.strategy_change_pct,
    benchmark_change_pct: item.benchmark_change_pct,
    model_target_weight: item.model_target_weight,
    model_target_weight_change: item.model_target_weight_change,
    empty_reference_target_weight: item.empty_reference_target_weight,
    empty_reference_target_weight_change: item.empty_reference_target_weight_change
  }));
}

function buildComparisonChartData(items: ComparisonResult[]): ChartPoint[] {
  const points = new Map<number, ChartPoint>();
  for (const [comparisonIndex, comparison] of items.entries()) {
    const key = `series_${comparison.genome.id}`;
    for (const item of comparison.result.nav ?? []) {
      const timeMs = new Date(item.time).getTime();
      const point =
        points.get(timeMs) ??
        ({
          label: formatAxisTime(item.time),
          time_ms: timeMs,
          time: item.time,
          price: item.price,
          benchmark: item.benchmark ?? item.total_assets
        } satisfies ChartPoint);
      if (point.price === undefined) point.price = item.price;
      point[key] = item.total_assets;
      point[`${key}_model_target_weight`] = item.model_target_weight;
      point[`${key}_model_target_weight_change`] = item.model_target_weight_change;
      point[`${key}_empty_reference_target_weight`] = item.empty_reference_target_weight;
      point[`${key}_empty_reference_target_weight_change`] = item.empty_reference_target_weight_change;
      if (point.benchmark === undefined) point.benchmark = item.benchmark ?? item.total_assets;
      if (comparisonIndex === 0) {
        point.strategy = item.total_assets;
        point.strategy_change_pct = item.strategy_change_pct;
        point.benchmark_change_pct = item.benchmark_change_pct;
        point.model_target_weight = item.model_target_weight;
        point.model_target_weight_change = item.model_target_weight_change;
        point.empty_reference_target_weight = item.empty_reference_target_weight;
        point.empty_reference_target_weight_change = item.empty_reference_target_weight_change;
      }
      points.set(timeMs, point);
    }
  }
  return Array.from(points.values()).sort((a, b) => a.time_ms - b.time_ms);
}

function buildMetricSeries(comparisonResults: ComparisonResult[], metricKey: string, fallback: MetricSeriesDef): MetricSeriesDef[] {
  if (comparisonResults.length === 0) return [fallback];
  return comparisonResults.map((item) => ({
    dataKey: `series_${item.genome.id}_${metricKey}`,
    name: shortGenomeLabel(item.genome),
    color: item.color
  }));
}

function toChartValue(value: number | undefined, mode: ScaleMode) {
  const safe = Math.max(1, value ?? 1);
  return mode === "log" ? Math.log10(safe) : safe;
}

function fromChartValue(value: number | string, mode: ScaleMode) {
  const numeric = Number(value);
  return mode === "log" ? Math.pow(10, numeric) : numeric;
}

function formatPrice(value: number | undefined) {
  if (value === undefined || !Number.isFinite(value)) return "-";
  return value.toLocaleString("zh-TW", { maximumFractionDigits: 4 });
}

function signedPercent(value: number | undefined) {
  if (value === undefined || !Number.isFinite(value)) return "-";
  const prefix = value > 0 ? "+" : "";
  return `${prefix}${formatPercent(value)}`;
}

function limitTicks(values: number[], maxTicks: number) {
  if (values.length <= maxTicks) return values;
  const step = Math.ceil(values.length / maxTicks);
  return values.filter((_, index) => index % step === 0);
}

function firstTickBy(points: ChartPoint[], keyFor: (date: Date) => string) {
  const seen = new Set<string>();
  const out: number[] = [];
  for (const point of points) {
    const key = keyFor(new Date(point.time_ms));
    if (!seen.has(key)) {
      seen.add(key);
      out.push(point.time_ms);
    }
  }
  return out;
}

function spacedTicks(points: ChartPoint[], stepDays: number, maxTicks: number) {
  if (!points.length) return [];
  const out: number[] = [];
  let last = Number.NEGATIVE_INFINITY;
  for (const point of points) {
    if (point.time_ms - last >= stepDays * dayMs) {
      out.push(point.time_ms);
      last = point.time_ms;
    }
  }
  return limitTicks(out, maxTicks);
}

function buildAxisTicks(points: ChartPoint[]) {
  if (points.length === 0) {
    return { ticks: [] as number[], formatter: (value: number | string) => String(value) };
  }
  const first = points[0].time_ms;
  const last = points[points.length - 1].time_ms;
  const spanDays = Math.max(1, (last - first) / dayMs);

  if (spanDays > 900) {
    const ticks = firstTickBy(points, (date) => String(date.getFullYear()));
    return { ticks: limitTicks(ticks, 8), formatter: (value: number | string) => formatTick(value, "year") };
  }
  if (spanDays > 370) {
    const ticks = firstTickBy(points, (date) => `${date.getFullYear()}-${Math.floor(date.getMonth() / 3)}`);
    return { ticks: limitTicks(ticks, 8), formatter: (value: number | string) => formatTick(value, "month") };
  }
  if (spanDays > 120) {
    const ticks = firstTickBy(points, (date) => `${date.getFullYear()}-${date.getMonth()}`);
    return { ticks: limitTicks(ticks, 10), formatter: (value: number | string) => formatTick(value, "month") };
  }
  if (spanDays > 45) {
    return { ticks: spacedTicks(points, 14, 9), formatter: (value: number | string) => formatTick(value, "day") };
  }
  if (spanDays > 18) {
    return { ticks: spacedTicks(points, 7, 10), formatter: (value: number | string) => formatTick(value, "day") };
  }
  if (points.length <= 32) {
    return { ticks: points.map((point) => point.time_ms), formatter: (value: number | string) => formatTick(value, "day") };
  }
  return { ticks: spacedTicks(points, Math.max(1, Math.ceil(spanDays / 14)), 14), formatter: (value: number | string) => formatTick(value, "day") };
}

function dateStartMs(value: string) {
  if (!value) return undefined;
  const ms = new Date(`${value}T00:00:00`).getTime();
  return Number.isFinite(ms) ? ms : undefined;
}

function dateEndMs(value: string) {
  if (!value) return undefined;
  const ms = new Date(`${value}T23:59:59.999`).getTime();
  return Number.isFinite(ms) ? ms : undefined;
}

export function BacktestingPage() {
  const [params] = useSearchParams();
  const initialGenome = Number(params.get("genome")) || 0;
  const instrumentsQuery = useQuery({ queryKey: ["market-data-instruments"], queryFn: () => marketDataApi.instruments() });
  const instruments = instrumentsQuery.data?.instruments ?? [];
  const instrumentNames = useMemo(() => Object.fromEntries(instruments.map((item) => [item.id, item.display_name])), [instruments]);
  const [instrumentId, setInstrumentId] = useState("BTCUSDT");
  const selectedInstrument = instruments.find((item) => item.id === instrumentId);
  const [interval, setInterval] = useState("1d");
  const [executionMode, setExecutionMode] = useState("close_same_bar");
  const [source, setSource] = useState<"champion" | "candidate" | "custom">(initialGenome ? "candidate" : "champion");
  const [candidateId, setCandidateId] = useState(initialGenome);
  const [selectedGenomeIds, setSelectedGenomeIds] = useState<number[]>(initialGenome ? [initialGenome] : []);
  const [customJson, setCustomJson] = useState("{\n  \n}");
  const [backtestStart, setBacktestStart] = useState("");
  const [backtestEnd, setBacktestEnd] = useState("");
  const [result, setResult] = useState<BacktestResult | null>(null);
  const [comparisonResults, setComparisonResults] = useState<ComparisonResult[]>([]);
  const [range, setRange] = useState<ChartRange | null>(null);
  const [scaleMode, setScaleMode] = useState<ScaleMode>("absolute");
  const [valueMode, setValueMode] = useState<ValueMode>("nav");
  const [hoverIndex, setHoverIndex] = useState<number | null>(null);
  const [initialCapital, setInitialCapital] = useState(10000);
  const [monthlyDCA, setMonthlyDCA] = useState(1000);
  const { data: genomes = [] } = useQuery({ queryKey: ["genomes"], queryFn: () => evolutionApi.listGenomes() });
  const selectableGenomes = genomes.filter((genome) => ["candidate", "challenger", "champion", "retired", "archived"].includes(genome.role));
  const selectedGenome = selectableGenomes.find((genome) => genome.id === candidateId) ?? selectableGenomes.find((genome) => selectedGenomeIds.includes(genome.id)) ?? selectableGenomes[0];
  const selectedGenomes = selectableGenomes.filter((genome) => selectedGenomeIds.includes(genome.id));
  const crossInstrumentCount = selectedGenomes.filter((genome) => genome.instrument_id && genome.instrument_id !== instrumentId).length;

  const startMutation = useMutation({
    mutationFn: async () => {
      const basePayload = {
        instrument_id: instrumentId,
        data_source: selectedInstrument?.data_source,
        symbol: selectedInstrument?.symbol ?? instrumentId,
        interval,
        execution_mode: executionMode,
        start_time_ms: dateStartMs(backtestStart),
        end_time_ms: dateEndMs(backtestEnd),
        initial_capital: initialCapital,
        monthly_dca: monthlyDCA,
        source
      };
      if (source === "candidate") {
        const targets = selectedGenomes.length > 0 ? selectedGenomes : selectedGenome ? [selectedGenome] : [];
        const responses = await Promise.all(
          targets.map((genome, index) =>
            backtestsApi
              .create({ ...basePayload, candidate_id: genome.id })
              .then((result) => ({ genome, result, color: comparisonColors[index % comparisonColors.length] }))
          )
        );
        return { primary: responses[0]?.result ?? null, comparisons: responses };
      }
      const primary = await backtestsApi.create({
        ...basePayload,
        custom_params: source === "custom" ? JSON.parse(customJson || "{}") : undefined
      });
      return { primary, comparisons: [] as ComparisonResult[] };
    },
    onSuccess: ({ primary, comparisons }) => {
      setResult(primary);
      setComparisonResults(comparisons);
    }
  });

  const seriesDefs = useMemo<SeriesDef[]>(
    () =>
      comparisonResults.length > 0
        ? comparisonResults.map((item) => ({
            key: `series_${item.genome.id}`,
            dataKey: `value_series_${item.genome.id}`,
            name: shortGenomeLabel(item.genome),
            color: item.color
          }))
        : [{ key: "strategy", dataKey: "strategy_value", name: "策略結果", color: "#2dd4bf" }],
    [comparisonResults]
  );
  const modelWeightSeries = useMemo(
    () => buildMetricSeries(comparisonResults, "model_target_weight", { dataKey: "model_target_weight", name: "基準模型", color: "#38bdf8" }),
    [comparisonResults]
  );
  const modelWeightChangeSeries = useMemo(
    () => buildMetricSeries(comparisonResults, "model_target_weight_change", { dataKey: "model_target_weight_change", name: "基準模型變化", color: "#f59e0b" }),
    [comparisonResults]
  );
  const emptyReferenceWeightSeries = useMemo(
    () => buildMetricSeries(comparisonResults, "empty_reference_target_weight", { dataKey: "empty_reference_target_weight", name: "空倉參考", color: "#a78bfa" }),
    [comparisonResults]
  );
  const emptyReferenceWeightChangeSeries = useMemo(
    () => buildMetricSeries(comparisonResults, "empty_reference_target_weight_change", { dataKey: "empty_reference_target_weight_change", name: "空倉參考變化", color: "#f472b6" }),
    [comparisonResults]
  );
  const chartData = useMemo(
    () => (comparisonResults.length > 0 ? buildComparisonChartData(comparisonResults) : buildSingleChartData(result)),
    [comparisonResults, result]
  );

  useEffect(() => {
    setRange(chartData.length ? { start: 0, end: chartData.length - 1 } : null);
  }, [chartData.length]);

  const visibleRawChartData = useMemo(() => {
    if (!range) return chartData;
    return chartData.slice(range.start, range.end + 1);
  }, [chartData, range]);
  const visibleChartData = useMemo(() => {
    if (visibleRawChartData.length === 0) return [];
    const bases = Object.fromEntries(seriesDefs.map((series) => [series.key, Math.max(1, Number(visibleRawChartData[0][series.key]) || 1)]));
    const baseBenchmark = Math.max(1, Number(visibleRawChartData[0].benchmark) || 1);
    return visibleRawChartData.map((item) => {
      const next: ChartPoint = { ...item };
      for (const series of seriesDefs) {
        const rawValue = Number(item[series.key]) || 0;
        const seriesRaw = valueMode === "relative" ? (rawValue / bases[series.key]) * 100 : rawValue;
        next[series.dataKey] = toChartValue(seriesRaw, scaleMode);
      }
      const benchmarkRawValue = Number(item.benchmark) || 0;
      const benchmarkRaw = valueMode === "relative" ? (benchmarkRawValue / baseBenchmark) * 100 : benchmarkRawValue;
      next.benchmark_value = toChartValue(benchmarkRaw, scaleMode);
      next.model_target_weight_value = Number(item.model_target_weight) || 0;
      next.model_target_weight_change_value = Number(item.model_target_weight_change) || 0;
      next.empty_reference_target_weight_value = Number(item.empty_reference_target_weight) || 0;
      next.empty_reference_target_weight_change_value = Number(item.empty_reference_target_weight_change) || 0;
      return next;
    });
  }, [visibleRawChartData, scaleMode, valueMode, seriesDefs]);
  const axisTicks = useMemo(() => buildAxisTicks(visibleChartData), [visibleChartData]);
  const hoveredPoint = hoverIndex !== null ? visibleChartData[hoverIndex] : null;

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    startMutation.mutate();
  }

  function changeInstrument(nextId: string) {
    const next = instruments.find((item) => item.id === nextId);
    setInstrumentId(nextId);
    setInterval(next?.supported_intervals[0] ?? "1d");
  }

  function resetRange() {
    setRange(chartData.length ? { start: 0, end: chartData.length - 1 } : null);
  }

  function updateHoverFromMouse(event: ReactMouseEvent<HTMLDivElement>) {
    if (visibleChartData.length === 0) {
      setHoverIndex(null);
      return;
    }
    const rect = event.currentTarget.getBoundingClientRect();
    const ratio = Math.min(1, Math.max(0, (event.clientX - rect.left) / Math.max(1, rect.width)));
    setHoverIndex(Math.round((visibleChartData.length - 1) * ratio));
  }

  function chartLayerProps(): HTMLAttributes<HTMLDivElement> {
    return {
      onMouseMove: updateHoverFromMouse,
      onMouseLeave: () => setHoverIndex(null)
    };
  }

  const axisFormatter = (value: number | string) => {
    const display = fromChartValue(value, scaleMode);
    if (valueMode === "relative") {
      return display >= 100 ? display.toFixed(0) : display.toFixed(1);
    }
    if (display >= 1_000_000) return `${(display / 1_000_000).toFixed(1)}M`;
    if (display >= 1_000) return `${Math.round(display / 1_000)}k`;
    return Math.round(display).toString();
  };

  return (
    <section className="space-y-4">
      <div>
        <h1 className="text-2xl font-bold text-slate-100">回測</h1>
        <p className="mt-1 text-sm text-slate-400">選擇標的、參數來源與執行設定，檢查同一組參數在不同市場中的表現。</p>
      </div>

      <Card>
        <CardHeader>
          <div>
            <CardTitle>{selectedInstrument?.display_name ?? "研究標的"}</CardTitle>
            <CardDescription>可使用本標的採用參數，也可指定候選參數做跨商品回測。</CardDescription>
          </div>
        </CardHeader>
        <form className="grid gap-4 md:grid-cols-2" onSubmit={submit}>
          <Select label="回測商品" value={instrumentId} onChange={changeInstrument} options={instruments.map((item) => [item.id, item.display_name])} />
          <Select label="資料週期" value={interval} onChange={setInterval} options={(selectedInstrument?.supported_intervals ?? ["1d"]).map((item) => [item, intervalLabels[item] ?? item])} />
          <Select label="執行假設" value={executionMode} onChange={setExecutionMode} options={executionModes} />
          <Select
            label="參數來源"
            value={source}
            onChange={(value) => setSource(value as typeof source)}
            options={[
              ["champion", "使用此商品採用參數"],
              ["candidate", "指定任一參數包"],
              ["custom", "自訂 JSON"]
            ]}
          />
          <DateInput label="回測開始日" value={backtestStart} onChange={setBacktestStart} />
          <DateInput label="回測結束日" value={backtestEnd} onChange={setBacktestEnd} />
          <NumberInput label="初始資金" value={initialCapital} min={1} onChange={setInitialCapital} />
          <NumberInput label="每月投入 / 定投金額" value={monthlyDCA} min={0} onChange={setMonthlyDCA} />

          {source === "candidate" ? (
            <div className="md:col-span-2">
              <div className="mb-2 text-sm text-slate-300">參數包</div>
              <div className="max-h-72 space-y-2 overflow-auto rounded-lg border border-slate-700 bg-slate-900/60 p-3">
                {selectableGenomes.map((genome) => {
                  const checked = selectedGenomeIds.includes(genome.id);
                  return (
                    <label key={genome.id} className={cn("flex cursor-pointer items-start gap-3 rounded-lg border px-3 py-2 text-sm transition", checked ? "border-[#2dd4bf]/40 bg-[#2dd4bf]/10 text-slate-100" : "border-white/[0.04] text-slate-400 hover:text-slate-200")}>
                      <input
                        className="mt-1"
                        type="checkbox"
                        checked={checked}
                        onChange={(event) => {
                          setCandidateId(genome.id);
                          setSelectedGenomeIds((current) => (event.target.checked ? Array.from(new Set([...current, genome.id])) : current.filter((id) => id !== genome.id)));
                        }}
                      />
                      <span>
                        <span className="block font-mono">{genomeLabel(genome, instrumentNames)}</span>
                        {genome.name ? <span className="mt-1 block text-xs text-slate-500">{genome.name}</span> : null}
                      </span>
                    </label>
                  );
                })}
              </div>
              {crossInstrumentCount > 0 ? <div className="mt-2 text-xs text-[#fde68a]">跨商品回測：已選 {crossInstrumentCount.toLocaleString("zh-TW")} 個非本商品來源參數，目前套用到 {selectedInstrument?.display_name ?? instrumentId}。</div> : null}
            </div>
          ) : null}

          {source === "custom" ? (
            <textarea className="h-40 w-full rounded-lg border border-slate-700 bg-slate-950/80 p-3 font-mono text-sm text-slate-100 outline-none focus:border-[#2dd4bf] md:col-span-2" value={customJson} onChange={(event) => setCustomJson(event.target.value)} />
          ) : null}

          {executionMode === "preclose_10m" ? <div className="text-xs text-[#fde68a] md:col-span-2">此模式需要已匯入收盤前 10 分鐘快照。若資料不足，請先到資料頁匯入包含快照的日 K 資料。</div> : null}

          <div className="md:col-span-2">
            <Button icon={PlayCircle} loading={startMutation.isPending} type="submit" disabled={source === "candidate" && selectedGenomeIds.length === 0}>
              開始回測
            </Button>
            {source === "candidate" && selectableGenomes.length === 0 ? <div className="mt-2 text-sm text-slate-500">尚無可回測的參數。</div> : null}
            {source === "candidate" && selectableGenomes.length > 0 && selectedGenomeIds.length === 0 ? <div className="mt-2 text-sm text-slate-500">請至少勾選一個參數。</div> : null}
            {startMutation.error ? <div className="mt-2 text-sm text-[#fecaca]">{String(startMutation.error.message)}</div> : null}
          </div>
        </form>
      </Card>

      {result ? (
        <>
          <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-4">
            {[
              ["總報酬", formatPercent(result.total_return), "text-[#bbf7d0]"],
              ["超額報酬", formatPercent(result.alpha), "text-[#99f6e4]"],
              ["最大回撤", formatPercent(result.max_drawdown), "text-[#fecaca]"],
              ["期末權益", formatMoney(result.final_equity), "text-slate-100"],
              ["定投總報酬", formatPercent(result.benchmark_return ?? 0), "text-slate-100"],
              ["定投最大回撤", formatPercent(result.benchmark_max_drawdown ?? 0), "text-[#fecaca]"],
              ["定投期末權益", formatMoney(result.benchmark_final_equity ?? result.benchmark), "text-slate-100"]
            ].map(([label, value, color]) => (
              <Card key={label} className="p-4">
                <div className="text-sm text-slate-500">{label}</div>
                <div className={cn("mt-2 font-mono text-2xl font-semibold", color)}>{value}</div>
              </Card>
            ))}
          </div>

          {comparisonResults.length > 1 ? (
            <Card className="p-4">
              <div className="mb-3 text-sm font-semibold text-slate-300">多參數比較</div>
              <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-3">
                {comparisonResults.map((item) => (
                  <div key={item.genome.id} className="rounded-lg border border-white/[0.04] bg-white/[0.02] p-3">
                    <div className="flex items-center gap-2">
                      <span className="h-3 w-3 rounded-full" style={{ backgroundColor: item.color }} />
                      <span className="font-mono text-sm text-slate-200">{shortGenomeLabel(item.genome)}</span>
                    </div>
                    <div className="mt-3 grid grid-cols-3 gap-2 text-xs">
                      <div><div className="text-slate-500">總報酬</div><div className="font-mono text-slate-100">{formatPercent(item.result.total_return)}</div></div>
                      <div><div className="text-slate-500">超額</div><div className="font-mono text-slate-100">{formatPercent(item.result.alpha)}</div></div>
                      <div><div className="text-slate-500">回撤</div><div className="font-mono text-[#fecaca]">{formatPercent(item.result.max_drawdown)}</div></div>
                      <div><div className="text-slate-500">定投報酬</div><div className="font-mono text-slate-100">{formatPercent(item.result.benchmark_return ?? 0)}</div></div>
                      <div><div className="text-slate-500">定投回撤</div><div className="font-mono text-[#fecaca]">{formatPercent(item.result.benchmark_max_drawdown ?? 0)}</div></div>
                      <div><div className="text-slate-500">定投權益</div><div className="font-mono text-slate-100">{formatMoney(item.result.benchmark_final_equity ?? item.result.benchmark)}</div></div>
                    </div>
                  </div>
                ))}
              </div>
            </Card>
          ) : null}

          <Card>
            <CardHeader className="items-center">
              <div>
                <CardTitle>淨值曲線</CardTitle>
                <CardDescription>使用下方時間滑塊調整顯示區間，游標移入圖表可同步讀值。</CardDescription>
              </div>
              <div className="flex flex-wrap items-center justify-end gap-2">
                <div className="inline-flex rounded-lg border border-white/10 bg-white/[0.03] p-1" aria-label="顯示模式">
                  <button type="button" className={cn("rounded-md px-3 py-1.5 text-sm transition", valueMode === "nav" ? "bg-[#2dd4bf] text-slate-950" : "text-slate-300 hover:bg-white/[0.06]")} onClick={() => setValueMode("nav")}>
                    實際淨值
                  </button>
                  <button type="button" className={cn("rounded-md px-3 py-1.5 text-sm transition", valueMode === "relative" ? "bg-[#2dd4bf] text-slate-950" : "text-slate-300 hover:bg-white/[0.06]")} onClick={() => setValueMode("relative")}>
                    區間相對
                  </button>
                </div>
                <div className="inline-flex rounded-lg border border-white/10 bg-white/[0.03] p-1" aria-label="刻度模式">
                  <button type="button" className={cn("rounded-md px-3 py-1.5 text-sm transition", scaleMode === "absolute" ? "bg-[#2dd4bf] text-slate-950" : "text-slate-300 hover:bg-white/[0.06]")} onClick={() => setScaleMode("absolute")}>
                    絕對值
                  </button>
                  <button type="button" className={cn("rounded-md px-3 py-1.5 text-sm transition", scaleMode === "log" ? "bg-[#2dd4bf] text-slate-950" : "text-slate-300 hover:bg-white/[0.06]")} onClick={() => setScaleMode("log")}>
                    對數
                  </button>
                </div>
                <Button icon={RotateCcw} variant="secondary" onClick={resetRange}>
                  重設
                </Button>
              </div>
            </CardHeader>
            <div className="mb-2 flex flex-wrap items-center gap-3 text-xs text-slate-500">
              <span className="inline-flex items-center gap-2" data-testid="backtest-visible-count">
                <ZoomIn className="h-4 w-4" />
                顯示 {visibleChartData.length.toLocaleString("zh-TW")} / {chartData.length.toLocaleString("zh-TW")} 筆
              </span>
              <span className="inline-flex items-center gap-2">
                <BarChart3 className="h-4 w-4" />
                {valueMode === "relative" ? "左側起點 = 100" : scaleMode === "log" ? "對數刻度" : "絕對值刻度"}
              </span>
            </div>
            <div className="relative h-96 overflow-hidden rounded-lg border border-white/[0.04] bg-slate-950/30 p-2">
              <ResponsiveContainer width="100%" height="100%">
                <AreaChart data={visibleChartData} margin={{ left: 0, right: 10, top: 10, bottom: 30 }}>
                  <defs>
                    {seriesDefs.map((series) => (
                      <linearGradient key={series.dataKey} id={`${series.dataKey}Fill`} x1="0" x2="0" y1="0" y2="1">
                        <stop offset="5%" stopColor={series.color} stopOpacity={0.3} />
                        <stop offset="95%" stopColor={series.color} stopOpacity={0.02} />
                      </linearGradient>
                    ))}
                  </defs>
                  <CartesianGrid stroke="rgba(148,163,184,0.08)" vertical={false} />
                  <XAxis dataKey="time_ms" ticks={axisTicks.ticks} tickFormatter={axisTicks.formatter} stroke="#64748b" tickLine={false} axisLine={false} fontSize={11} interval={0} minTickGap={24} />
                  <YAxis stroke="#64748b" tickLine={false} axisLine={false} fontSize={12} tickFormatter={axisFormatter} domain={["auto", "auto"]} />
                  {hoveredPoint ? <ReferenceLine x={hoveredPoint.time_ms} stroke="#f8fafc" strokeOpacity={0.35} strokeWidth={1} /> : null}
                  <Legend />
                  {seriesDefs.map((series) => (
                    <Area key={series.dataKey} name={series.name} type="monotone" dataKey={series.dataKey} stroke={series.color} strokeWidth={2} fill={seriesDefs.length === 1 ? `url(#${series.dataKey}Fill)` : "transparent"} isAnimationActive={false} connectNulls />
                  ))}
                  <Area name="基準" type="monotone" dataKey="benchmark_value" stroke="#64748b" strokeDasharray="5 5" fill="transparent" isAnimationActive={false} />
                </AreaChart>
              </ResponsiveContainer>
              <div
                aria-label="圖表互動區"
                data-testid="backtest-chart-layer"
                className="absolute inset-x-2 bottom-14 top-2 z-10 select-none rounded-md"
                {...chartLayerProps()}
              />
            </div>
            {hoveredPoint ? (
              <ChartReadout
                point={hoveredPoint}
                rows={[
                  ["日期", formatFullAxisTime(hoveredPoint.time_ms)],
                  ["價位 / 點數", formatPrice(hoveredPoint.price)],
                  ["策略淨值", formatMoney(Number(hoveredPoint.strategy ?? 0))],
                  ["策略日變化", signedPercent(Number(hoveredPoint.strategy_change_pct ?? 0))],
                  ["定投淨值", formatMoney(Number(hoveredPoint.benchmark ?? 0))],
                  ["定投日變化", signedPercent(Number(hoveredPoint.benchmark_change_pct ?? 0))],
                  ["基準模型目標權重", formatPercent(Number(hoveredPoint.model_target_weight ?? 0))],
                  ["基準模型權重變化", signedPercent(Number(hoveredPoint.model_target_weight_change ?? 0))],
                  ["空倉參考目標權重", formatPercent(Number(hoveredPoint.empty_reference_target_weight ?? 0))],
                  ["空倉參考權重變化", signedPercent(Number(hoveredPoint.empty_reference_target_weight_change ?? 0))]
                ]}
              />
            ) : null}
            <ChartRangeSlider range={range} total={chartData.length} startLabel={formatFullAxisTime(chartData[range?.start ?? 0]?.time_ms ?? 0)} endLabel={formatFullAxisTime(chartData[range?.end ?? 0]?.time_ms ?? 0)} onChange={setRange} onReset={resetRange} />
          </Card>

          <MetricChartCard
            title="基準模型目標權重每日值"
            description="從回測起點空倉開始，依模型路徑逐日產生的目標水準。"
            data={visibleChartData}
            axisTicks={axisTicks}
            hoveredPoint={hoveredPoint}
            lines={modelWeightSeries}
            formatter={(value) => formatPercent(Number(value))}
            layerProps={chartLayerProps()}
          />
          <ChartRangeSlider range={range} total={chartData.length} startLabel={formatFullAxisTime(chartData[range?.start ?? 0]?.time_ms ?? 0)} endLabel={formatFullAxisTime(chartData[range?.end ?? 0]?.time_ms ?? 0)} onChange={setRange} onReset={resetRange} />
          <MetricChartCard
            title="基準模型目標權重每日變化"
            description="今日基準模型目標權重減昨日基準模型目標權重。"
            data={visibleChartData}
            axisTicks={axisTicks}
            hoveredPoint={hoveredPoint}
            lines={modelWeightChangeSeries}
            formatter={(value) => signedPercent(Number(value))}
            layerProps={chartLayerProps()}
          />
          <ChartRangeSlider range={range} total={chartData.length} startLabel={formatFullAxisTime(chartData[range?.start ?? 0]?.time_ms ?? 0)} endLabel={formatFullAxisTime(chartData[range?.end ?? 0]?.time_ms ?? 0)} onChange={setRange} onReset={resetRange} />
          <MetricChartCard
            title="空倉參考目標權重每日值"
            description="每天獨立假設昨日空倉後，依該日資料得到的參考目標水準。"
            data={visibleChartData}
            axisTicks={axisTicks}
            hoveredPoint={hoveredPoint}
            lines={emptyReferenceWeightSeries}
            formatter={(value) => formatPercent(Number(value))}
            layerProps={chartLayerProps()}
          />
          <ChartRangeSlider range={range} total={chartData.length} startLabel={formatFullAxisTime(chartData[range?.start ?? 0]?.time_ms ?? 0)} endLabel={formatFullAxisTime(chartData[range?.end ?? 0]?.time_ms ?? 0)} onChange={setRange} onReset={resetRange} />
          <MetricChartCard
            title="空倉參考目標權重每日變化"
            description="今日空倉參考目標權重減昨日空倉參考目標權重。"
            data={visibleChartData}
            axisTicks={axisTicks}
            hoveredPoint={hoveredPoint}
            lines={emptyReferenceWeightChangeSeries}
            formatter={(value) => signedPercent(Number(value))}
            layerProps={chartLayerProps()}
          />
          <ChartRangeSlider range={range} total={chartData.length} startLabel={formatFullAxisTime(chartData[range?.start ?? 0]?.time_ms ?? 0)} endLabel={formatFullAxisTime(chartData[range?.end ?? 0]?.time_ms ?? 0)} onChange={setRange} onReset={resetRange} />

          <Card>
            <CardHeader>
              <div>
                <CardTitle>窗口評分</CardTitle>
                <CardDescription>同一參數在不同歷史視窗中的表現。</CardDescription>
              </div>
            </CardHeader>
            <div className="grid gap-3 md:grid-cols-4">
              {Object.entries(result.windows).map(([label, value]) => (
                <div key={label} className="rounded-lg border border-white/[0.04] bg-white/[0.02] p-4">
                  <div className="text-sm text-slate-500">{windowLabel(label)}</div>
                  <div className="mt-2 font-mono text-xl text-slate-100">{value.toFixed(2)}</div>
                </div>
              ))}
            </div>
          </Card>
        </>
      ) : (
        <Card className="p-4 text-sm text-slate-500">尚無回測結果。</Card>
      )}
    </section>
  );
}

function DateInput({ label, value, onChange }: { label: string; value: string; onChange: (value: string) => void }) {
  return (
    <label>
      <span className="mb-2 block text-sm text-slate-300">{label}</span>
      <input
        className="h-11 w-full rounded-lg border border-slate-700 bg-slate-900/80 px-3 text-sm text-slate-100 outline-none focus:border-[#2dd4bf]"
        type="date"
        value={value}
        onChange={(event) => onChange(event.target.value)}
      />
    </label>
  );
}

function NumberInput({ label, value, min, onChange }: { label: string; value: number; min: number; onChange: (value: number) => void }) {
  return (
    <label>
      <span className="mb-2 block text-sm text-slate-300">{label}</span>
      <input
        className="h-11 w-full rounded-lg border border-slate-700 bg-slate-900/80 px-3 text-sm text-slate-100 outline-none focus:border-[#2dd4bf]"
        type="number"
        min={min}
        step="100"
        value={value}
        onChange={(event) => onChange(Number(event.target.value))}
      />
    </label>
  );
}

function ChartReadout({ rows }: { point: ChartPoint; rows: Array<[string, string]> }) {
  return (
    <div className="mt-3 grid gap-2 rounded-lg border border-white/[0.04] bg-slate-950/50 p-3 text-xs md:grid-cols-3 xl:grid-cols-5">
      {rows.map(([label, value]) => (
        <div key={label}>
          <div className="text-slate-500">{label}</div>
          <div className="mt-1 font-mono text-slate-100">{value}</div>
        </div>
      ))}
    </div>
  );
}

function MetricChartCard({
  title,
  description,
  data,
  axisTicks,
  hoveredPoint,
  lines,
  formatter,
  layerProps
}: {
  title: string;
  description: string;
  data: ChartPoint[];
  axisTicks: { ticks: number[]; formatter: (value: number | string) => string };
  hoveredPoint: ChartPoint | null;
  lines: MetricSeriesDef[];
  formatter: (value: number | string) => string;
  layerProps: HTMLAttributes<HTMLDivElement>;
}) {
  return (
    <Card>
      <CardHeader>
        <div>
          <CardTitle>{title}</CardTitle>
          <CardDescription>{description}</CardDescription>
        </div>
      </CardHeader>
      <div className="relative h-72 overflow-hidden rounded-lg border border-white/[0.04] bg-slate-950/30 p-2">
        <ResponsiveContainer width="100%" height="100%">
          <AreaChart data={data} margin={{ left: 0, right: 10, top: 10, bottom: 30 }}>
            <CartesianGrid stroke="rgba(148,163,184,0.08)" vertical={false} />
            <XAxis dataKey="time_ms" ticks={axisTicks.ticks} tickFormatter={axisTicks.formatter} stroke="#64748b" tickLine={false} axisLine={false} fontSize={11} interval={0} minTickGap={24} />
            <YAxis stroke="#64748b" tickLine={false} axisLine={false} fontSize={12} tickFormatter={formatter} domain={["auto", "auto"]} />
            {hoveredPoint ? <ReferenceLine x={hoveredPoint.time_ms} stroke="#f8fafc" strokeOpacity={0.35} strokeWidth={1} /> : null}
            <Legend />
            {lines.map((line) => (
              <Area key={line.dataKey} name={line.name} type="monotone" dataKey={line.dataKey} stroke={line.color} strokeWidth={2} fill="transparent" isAnimationActive={false} connectNulls />
            ))}
          </AreaChart>
        </ResponsiveContainer>
        <div
          className="absolute inset-x-2 bottom-14 top-2 z-10 select-none rounded-md"
          {...layerProps}
        />
      </div>
      {hoveredPoint ? (
        <div className="mt-3 rounded-lg border border-white/[0.04] bg-slate-950/50 p-3 text-xs">
          <div className="text-slate-500">{formatFullAxisTime(hoveredPoint.time_ms)}</div>
          <div className="mt-2 grid gap-2 md:grid-cols-2 xl:grid-cols-4">
            <div>
              <div className="text-slate-500">價位 / 點數</div>
              <div className="mt-1 font-mono text-slate-100">{formatPrice(hoveredPoint.price)}</div>
            </div>
            {lines.map((line) => (
              <div key={line.dataKey}>
                <div className="flex items-center gap-2 text-slate-500">
                  <span className="h-2.5 w-2.5 rounded-full" style={{ backgroundColor: line.color }} />
                  <span>{line.name}</span>
                </div>
                <div className="mt-1 font-mono text-slate-100">{formatter(Number(hoveredPoint[line.dataKey] ?? 0))}</div>
              </div>
            ))}
          </div>
        </div>
      ) : null}
    </Card>
  );
}

function Select({
  label,
  value,
  onChange,
  options
}: {
  label: string;
  value: string;
  onChange: (value: string) => void;
  options: ReadonlyArray<readonly [string, string]>;
}) {
  return (
    <label>
      <span className="mb-2 block text-sm text-slate-300">{label}</span>
      <select className="h-11 w-full rounded-lg border border-slate-700 bg-slate-900/80 px-3 text-sm text-slate-100 outline-none focus:border-[#2dd4bf]" value={value} onChange={(event) => onChange(event.target.value)}>
        {options.map(([optionValue, optionLabel]) => (
          <option key={optionValue} value={optionValue}>
            {optionLabel}
          </option>
        ))}
      </select>
    </label>
  );
}
