import { useEffect, useMemo, useState, type HTMLAttributes, type MouseEvent as ReactMouseEvent, type ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { Activity, BarChart3, Gauge, Home, RotateCcw } from "lucide-react";
import { Area, AreaChart, CartesianGrid, Legend, ReferenceLine, ResponsiveContainer, XAxis, YAxis } from "recharts";
import { formatMoney, formatPercent, shortDateTime } from "../../shared/lib/format";
import { researchApi, type ResearchModelPoint, type ResearchStatusItem } from "../../shared/services/research";
import { marketDataApi } from "../../shared/services/marketData";
import { Card, CardDescription, CardHeader, CardTitle } from "../../shared/ui/Card";
import { Button } from "../../shared/ui/Button";
import { ChartRangeSlider } from "../../shared/ui/ChartRangeSlider";
import { cn } from "../../shared/lib/cn";

const settingsStorageKey = "quantsaas.marketStatus.positionSimulation";
const homeStorageKey = "quantsaas.marketStatus.homeInstrument";

const diagLabels: Record<string, string> = {
  total_equity: "估算總資產",
  reserve_floor: "保留現金",
  spendable_usdt: "可配置資金",
  current_weight: "空倉參考目前權重",
  target_weight: "空倉參考目標權重",
  delta_weight: "空倉參考權重差",
  signal: "綜合訊號",
  volatility_ratio: "波動比",
  market_beta: "市場 Beta 倍率",
  market_trend_slope: "趨勢斜率",
  market_drawdown: "回撤比例",
  macro_regime_multiplier: "定投狀態倍率"
};

const stateLabels: Record<string, string> = {
  BULL_TREND: "牛市趨勢",
  BEAR_TREND: "熊市趨勢",
  QUIET: "平靜",
  SHOCK: "震盪"
};

type SimulationSettings = {
  startDate: string;
  initialCapital: number;
  monthlyDCA: number;
  feeRate: number;
  spreadRate: number;
};

type ScaleMode = "absolute" | "log";
type ValueMode = "nav" | "relative";
type ChartRange = { start: number; end: number };
type ChartPoint = ResearchModelPoint & {
  label: string;
  model_nav_value?: number;
  benchmark_value?: number;
};

const dayMs = 24 * 60 * 60 * 1000;

function defaultSettings(): SimulationSettings {
  const date = new Date();
  date.setUTCFullYear(date.getUTCFullYear() - 5);
  return {
    startDate: date.toISOString().slice(0, 10),
    initialCapital: 10000,
    monthlyDCA: 1000,
    feeRate: 0.001,
    spreadRate: 0.0005
  };
}

function loadSettings() {
  try {
    const raw = localStorage.getItem(settingsStorageKey);
    if (!raw) return defaultSettings();
    return { ...defaultSettings(), ...JSON.parse(raw) } as SimulationSettings;
  } catch {
    return defaultSettings();
  }
}

function loadHomeInstrument() {
  return localStorage.getItem(homeStorageKey) || "TWII";
}

function dayStartMs(value: string) {
  return new Date(`${value}T00:00:00.000Z`).getTime();
}

function formatNumber(value: unknown) {
  if (typeof value !== "number") return String(value ?? "-");
  if (Math.abs(value) <= 1) return value.toFixed(4);
  return value.toLocaleString("zh-TW", { maximumFractionDigits: 4 });
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

function stateLabel(value?: string) {
  return stateLabels[value ?? ""] ?? value ?? "尚無判斷";
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

function toChartValue(value: number | undefined, mode: ScaleMode) {
  const safe = Math.max(1, value ?? 1);
  return mode === "log" ? Math.log10(safe) : safe;
}

function fromChartValue(value: number | string, mode: ScaleMode) {
  const numeric = Number(value);
  return mode === "log" ? Math.pow(10, numeric) : numeric;
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
  if (spanDays > 900) return { ticks: limitTicks(firstTickBy(points, (date) => String(date.getFullYear())), 8), formatter: (value: number | string) => formatTick(value, "year") };
  if (spanDays > 370) return { ticks: limitTicks(firstTickBy(points, (date) => `${date.getFullYear()}-${Math.floor(date.getMonth() / 3)}`), 8), formatter: (value: number | string) => formatTick(value, "month") };
  if (spanDays > 120) return { ticks: limitTicks(firstTickBy(points, (date) => `${date.getFullYear()}-${date.getMonth()}`), 10), formatter: (value: number | string) => formatTick(value, "month") };
  if (spanDays > 45) return { ticks: spacedTicks(points, 14, 9), formatter: (value: number | string) => formatTick(value, "day") };
  if (spanDays > 18) return { ticks: spacedTicks(points, 7, 10), formatter: (value: number | string) => formatTick(value, "day") };
  if (points.length <= 32) return { ticks: points.map((point) => point.time_ms), formatter: (value: number | string) => formatTick(value, "day") };
  return { ticks: spacedTicks(points, Math.max(1, Math.ceil(spanDays / 14)), 14), formatter: (value: number | string) => formatTick(value, "day") };
}

function axisMoney(value: number | string) {
  const display = Number(value);
  if (display >= 1_000_000) return `${(display / 1_000_000).toFixed(1)}M`;
  if (display >= 1_000) return `${Math.round(display / 1_000)}k`;
  return Math.round(display).toString();
}

export function MarketStatusPage() {
  const [settings, setSettings] = useState<SimulationSettings>(() => loadSettings());
  const [homeInstrument, setHomeInstrument] = useState(() => loadHomeInstrument());
  const [instrumentId, setInstrumentId] = useState(() => loadHomeInstrument());
  const [range, setRange] = useState<ChartRange | null>(null);
  const [scaleMode, setScaleMode] = useState<ScaleMode>("absolute");
  const [valueMode, setValueMode] = useState<ValueMode>("nav");
  const [statusInterval, setStatusInterval] = useState("1d");
  const [hoverIndex, setHoverIndex] = useState<number | null>(null);

  useEffect(() => {
    localStorage.setItem(settingsStorageKey, JSON.stringify(settings));
  }, [settings]);

  useEffect(() => {
    localStorage.setItem(homeStorageKey, homeInstrument);
  }, [homeInstrument]);

  const instrumentsQuery = useQuery({ queryKey: ["market-data-instruments"], queryFn: () => marketDataApi.instruments() });
  const instruments = instrumentsQuery.data?.instruments ?? [];
  const selectedInstrument = instruments.find((item) => item.id === instrumentId);
  const query = useQuery({
    queryKey: ["research-status", instrumentId, settings],
    queryFn: () =>
      researchApi.status({
        instrument_id: instrumentId,
        simulation_start_ms: dayStartMs(settings.startDate),
        simulation_initial_capital: settings.initialCapital,
        simulation_monthly_dca: settings.monthlyDCA,
        simulation_fee_rate: settings.feeRate,
        simulation_spread_rate: settings.spreadRate
      }),
    enabled: Boolean(instrumentId),
    refetchInterval: 60_000
  });
  const item = query.data?.items?.[0] as ResearchStatusItem | undefined;
  const intervalStates = item?.interval_states ?? [];
  const activeState = intervalStates.find((state) => state.interval === statusInterval) ?? item;
  const ready = activeState?.status === "ready";
  const chartData = useMemo<ChartPoint[]>(
    () =>
      (activeState?.model_simulation?.chart_points ?? []).map((point) => ({
        ...point,
        label: formatFullAxisTime(point.time_ms),
        model_nav_value: point.model_nav,
        benchmark_value: point.benchmark
      })),
    [activeState]
  );

  useEffect(() => {
    setRange(chartData.length ? { start: 0, end: chartData.length - 1 } : null);
  }, [chartData.length]);

  const visibleRawChartData = useMemo(() => (range ? chartData.slice(range.start, range.end + 1) : chartData), [chartData, range]);
  const visibleChartData = useMemo(() => {
    if (visibleRawChartData.length === 0) return [];
    const baseModel = Math.max(1, visibleRawChartData[0].model_nav ?? visibleRawChartData[0].model_nav_value ?? 1);
    const baseBenchmark = Math.max(1, visibleRawChartData[0].benchmark ?? visibleRawChartData[0].benchmark_value ?? 1);
    return visibleRawChartData.map((point) => {
      const modelRaw = Number(point.model_nav ?? point.model_nav_value ?? 0);
      const benchmarkRaw = Number(point.benchmark ?? point.benchmark_value ?? 0);
      const modelDisplay = valueMode === "relative" ? (modelRaw / baseModel) * 100 : modelRaw;
      const benchmarkDisplay = valueMode === "relative" ? (benchmarkRaw / baseBenchmark) * 100 : benchmarkRaw;
      return {
        ...point,
        model_nav_value: toChartValue(modelDisplay, scaleMode),
        benchmark_value: toChartValue(benchmarkDisplay, scaleMode)
      };
    });
  }, [visibleRawChartData, scaleMode, valueMode]);
  const axisTicks = useMemo(() => buildAxisTicks(visibleChartData), [visibleChartData]);
  const hoveredPoint = hoverIndex !== null ? visibleChartData[hoverIndex] : null;
  const simulation = item?.position_simulation;
  const model = activeState?.model_simulation;

  function updateHoverFromMouse(event: ReactMouseEvent<HTMLDivElement>) {
    if (visibleChartData.length === 0) {
      setHoverIndex(null);
      return;
    }
    const rect = event.currentTarget.getBoundingClientRect();
    const ratio = Math.min(1, Math.max(0, (event.clientX - rect.left) / Math.max(1, rect.width)));
    setHoverIndex(Math.round((visibleChartData.length - 1) * ratio));
  }

  function chartLayerProps() {
    return {
      onMouseMove: updateHoverFromMouse,
      onMouseLeave: () => {
        setHoverIndex(null);
      }
    };
  }

  function resetRange() {
    setRange(chartData.length ? { start: 0, end: chartData.length - 1 } : null);
  }

  const navAxisFormatter = (value: number | string) => {
    const display = fromChartValue(value, scaleMode);
    if (valueMode === "relative") {
      return display >= 100 ? display.toFixed(0) : display.toFixed(1);
    }
    return axisMoney(display);
  };

  const navChartControls = (
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
  );

  const navChartSummary = (
    <div className="mb-2 flex flex-wrap items-center gap-3 text-xs text-slate-500">
      <span className="inline-flex items-center gap-2">
        <BarChart3 className="h-4 w-4" />
        {valueMode === "relative" ? "左側起點 = 100" : scaleMode === "log" ? "對數刻度" : "絕對值刻度"}
      </span>
    </div>
  );

  return (
    <section className="space-y-4">
      <div>
        <h1 className="text-2xl font-bold text-slate-100">市場狀態</h1>
        <p className="mt-1 text-sm text-slate-400">套用目前已採用參數，觀察單一標的的基準模型、空倉參考與收盤後狀態。</p>
      </div>

      <Card className="p-4">
        <div className="grid gap-3 md:grid-cols-[1fr_auto]">
          <label>
            <span className="mb-2 block text-sm text-slate-300">顯示標的</span>
            <select className="h-11 w-full rounded-lg border border-slate-700 bg-slate-900/80 px-3 text-sm text-slate-100 outline-none focus:border-[#2dd4bf]" value={instrumentId} onChange={(event) => setInstrumentId(event.target.value)}>
              {instruments.map((instrument) => (
                <option key={instrument.id} value={instrument.id}>{instrument.display_name}</option>
              ))}
            </select>
          </label>
          <div className="flex items-end">
            <Button icon={Home} variant={homeInstrument === instrumentId ? "secondary" : "primary"} onClick={() => setHomeInstrument(instrumentId)}>
              {homeInstrument === instrumentId ? "首頁標的" : "設為首頁"}
            </Button>
          </div>
        </div>
        <div className="mt-4 inline-flex rounded-lg border border-white/10 bg-white/[0.03] p-1" aria-label="參數週期">
          {["1d", "1w"].map((interval) => {
            const state = intervalStates.find((item) => item.interval === interval);
            return (
              <button
                key={interval}
                type="button"
                className={cn("rounded-md px-3 py-1.5 text-sm transition", statusInterval === interval ? "bg-[#2dd4bf] text-slate-950" : "text-slate-300 hover:bg-white/[0.06]")}
                onClick={() => setStatusInterval(interval)}
              >
                {interval === "1d" ? "日 K 參數" : "週 K 參數"}{state?.status === "ready" ? "" : "（缺）"}
              </button>
            );
          })}
        </div>
      </Card>

      {query.isLoading ? <Card className="p-4 text-sm text-slate-500">載入中...</Card> : null}
      {query.error ? <Card className="p-4 text-sm text-[#fecaca]">{String(query.error.message)}</Card> : null}

      {!ready ? (
        <Card className="p-4 text-sm text-slate-500">
          {activeState?.status === "missing_champion" ? `尚未有這個標的的${statusInterval === "1d" ? "日 K" : "週 K"}已採用參數。` : `尚未有足夠的${statusInterval === "1d" ? "日 K" : "週 K"}資料。`}
        </Card>
      ) : (
        <>
          <Card className="border-[#2dd4bf]/20">
            <CardHeader>
              <div>
                <CardTitle>{selectedInstrument?.display_name ?? item?.instrument.display_name}</CardTitle>
                <CardDescription>{item?.symbol} · {item?.data_source} · {activeState?.interval} · {activeState?.execution_mode}</CardDescription>
              </div>
              <Gauge className="h-5 w-5 text-[#99f6e4]" />
            </CardHeader>
            <div className="grid gap-3 md:grid-cols-3 xl:grid-cols-4">
              <Metric label="市場狀態" value={stateLabel(activeState?.market_state)} />
              <Metric label={statusInterval === "1d" ? "最新完成日 K" : "最新完成週 K"} value={activeState?.latest_bar ? `${shortDateTime(activeState.latest_bar.time)} · ${formatNumber(activeState.latest_bar.close)}` : "-"} />
              <Metric label="實務模型淨值" value={model ? formatMoney(model.latest_nav, "USD") : "-"} highlight />
              <Metric label="實務模型淨值日變化" value={signedPercent(model?.nav_change_pct)} danger={(model?.nav_change_pct ?? 0) < 0} />
              <Metric label="定投淨值" value={model ? formatMoney(model.latest_benchmark, "USD") : "-"} />
              <Metric label="定投淨值日變化" value={signedPercent(model?.benchmark_change_pct)} danger={(model?.benchmark_change_pct ?? 0) < 0} />
              <Metric label="實務模型目標權重" value={model ? formatPercent(model.latest_practical_target_weight) : "-"} highlight />
              <Metric label="實務模型權重變化" value={signedPercent(model?.latest_practical_target_weight_change)} />
              <Metric label="基準模型目標權重" value={model ? formatPercent(model.latest_model_target_weight) : "-"} highlight />
              <Metric label="基準模型權重變化" value={signedPercent(model?.latest_model_target_weight_change)} />
              <Metric label="空倉參考目標權重" value={model ? formatPercent(model.latest_empty_reference_target_weight) : "-"} />
              <Metric label="空倉參考權重變化" value={signedPercent(model?.latest_empty_reference_target_weight_change)} />
              <Metric label="調倉門檻" value={model ? formatPercent(model.rebalance_threshold ?? 0) : "-"} />
              <Metric label="手續費率" value={model ? formatPercent(model.fee_rate ?? 0) : "-"} />
              <Metric label="價差 / 滑價率" value={model ? formatPercent(model.spread_rate ?? 0) : "-"} />
            </div>
          </Card>

          <ChartCard title="實務模型淨值走勢" description="實務模型與定投基準使用相同本金與定期入金設定。" actions={navChartControls} summary={navChartSummary} data={visibleChartData} axisTicks={axisTicks} hoveredPoint={hoveredPoint} layerProps={chartLayerProps()} yFormatter={navAxisFormatter} lines={[["model_nav_value", "實務模型", "#2dd4bf"], ["benchmark_value", "定投基準", "#64748b"]]} />
          <ChartRangeSlider range={range} total={chartData.length} startLabel={formatFullAxisTime(chartData[range?.start ?? 0]?.time_ms ?? 0)} endLabel={formatFullAxisTime(chartData[range?.end ?? 0]?.time_ms ?? 0)} onChange={setRange} onReset={resetRange} />
          <ChartCard title="實務模型目標權重每日值" description="套用調倉門檻與執行假設後，實務模型實際採用的總倉位目標。" data={visibleChartData} axisTicks={axisTicks} hoveredPoint={hoveredPoint} layerProps={chartLayerProps()} yFormatter={(value) => formatPercent(Number(value))} lines={[["practical_target_weight", "實務模型", "#2dd4bf"]]} />
          <ChartRangeSlider range={range} total={chartData.length} startLabel={formatFullAxisTime(chartData[range?.start ?? 0]?.time_ms ?? 0)} endLabel={formatFullAxisTime(chartData[range?.end ?? 0]?.time_ms ?? 0)} onChange={setRange} onReset={resetRange} />
          <ChartCard title="實務模型目標權重每日變化" description="今日實務模型目標權重減昨日實務模型目標權重。" data={visibleChartData} axisTicks={axisTicks} hoveredPoint={hoveredPoint} layerProps={chartLayerProps()} yFormatter={(value) => signedPercent(Number(value))} lines={[["practical_target_weight_change", "實務模型變化", "#fb7185"]]} />
          <ChartRangeSlider range={range} total={chartData.length} startLabel={formatFullAxisTime(chartData[range?.start ?? 0]?.time_ms ?? 0)} endLabel={formatFullAxisTime(chartData[range?.end ?? 0]?.time_ms ?? 0)} onChange={setRange} onReset={resetRange} />
          <ChartCard title="基準模型目標權重每日值" description="基準模型路徑逐日產生的理論目標水準。" data={visibleChartData} axisTicks={axisTicks} hoveredPoint={hoveredPoint} layerProps={chartLayerProps()} yFormatter={(value) => formatPercent(Number(value))} lines={[["model_target_weight", "基準模型", "#38bdf8"]]} />
          <ChartRangeSlider range={range} total={chartData.length} startLabel={formatFullAxisTime(chartData[range?.start ?? 0]?.time_ms ?? 0)} endLabel={formatFullAxisTime(chartData[range?.end ?? 0]?.time_ms ?? 0)} onChange={setRange} onReset={resetRange} />
          <ChartCard title="基準模型目標權重每日變化" description="今日基準模型目標權重減昨日基準模型目標權重。" data={visibleChartData} axisTicks={axisTicks} hoveredPoint={hoveredPoint} layerProps={chartLayerProps()} yFormatter={(value) => signedPercent(Number(value))} lines={[["model_target_weight_change", "基準模型變化", "#f59e0b"]]} />
          <ChartRangeSlider range={range} total={chartData.length} startLabel={formatFullAxisTime(chartData[range?.start ?? 0]?.time_ms ?? 0)} endLabel={formatFullAxisTime(chartData[range?.end ?? 0]?.time_ms ?? 0)} onChange={setRange} onReset={resetRange} />
          <ChartCard title="空倉參考目標權重每日值" description="每天獨立假設昨日空倉後得到的參考目標水準。" data={visibleChartData} axisTicks={axisTicks} hoveredPoint={hoveredPoint} layerProps={chartLayerProps()} yFormatter={(value) => formatPercent(Number(value))} lines={[["empty_reference_target_weight", "空倉參考", "#a78bfa"]]} />
          <ChartRangeSlider range={range} total={chartData.length} startLabel={formatFullAxisTime(chartData[range?.start ?? 0]?.time_ms ?? 0)} endLabel={formatFullAxisTime(chartData[range?.end ?? 0]?.time_ms ?? 0)} onChange={setRange} onReset={resetRange} />
          <ChartCard title="空倉參考目標權重每日變化" description="今日空倉參考目標權重減昨日空倉參考目標權重。" data={visibleChartData} axisTicks={axisTicks} hoveredPoint={hoveredPoint} layerProps={chartLayerProps()} yFormatter={(value) => signedPercent(Number(value))} lines={[["empty_reference_target_weight_change", "空倉參考變化", "#f472b6"]]} />
          <ChartRangeSlider range={range} total={chartData.length} startLabel={formatFullAxisTime(chartData[range?.start ?? 0]?.time_ms ?? 0)} endLabel={formatFullAxisTime(chartData[range?.end ?? 0]?.time_ms ?? 0)} onChange={setRange} onReset={resetRange} />

          <details className="rounded-lg border border-white/[0.04] bg-white/[0.02] p-4">
            <summary className="cursor-pointer text-sm font-semibold text-slate-300">倉位模擬系統</summary>
            <div className="mt-4 space-y-4">
              <div className="grid gap-3 md:grid-cols-3">
                <label>
                  <span className="mb-2 block text-sm text-slate-300">模擬起始日</span>
                  <input className="h-11 w-full rounded-lg border border-slate-700 bg-slate-900/80 px-3 text-sm text-slate-100 outline-none focus:border-[#2dd4bf]" type="date" value={settings.startDate} onChange={(event) => setSettings((prev) => ({ ...prev, startDate: event.target.value }))} />
                </label>
                <NumberInput label="初始資金" min={1} value={settings.initialCapital} onChange={(value) => setSettings((prev) => ({ ...prev, initialCapital: value }))} />
                <NumberInput label="每月定投金額" min={0} value={settings.monthlyDCA} onChange={(value) => setSettings((prev) => ({ ...prev, monthlyDCA: value }))} />
                <NumberInput label="手續費率" min={0} step={0.0001} value={settings.feeRate} onChange={(value) => setSettings((prev) => ({ ...prev, feeRate: value }))} />
                <NumberInput label="價差 / 滑價率" min={0} step={0.0001} value={settings.spreadRate} onChange={(value) => setSettings((prev) => ({ ...prev, spreadRate: value }))} />
              </div>
              {simulation ? (
                <div className="grid gap-3 md:grid-cols-4">
                  <Metric label="模擬倉淨值" value={formatMoney(simulation.latest_nav, "USD")} highlight />
                  <Metric label="淨值日變化" value={signedPercent(simulation.nav_change_pct)} danger={(simulation.nav_change_pct ?? 0) < 0} />
                  <Metric label="現金" value={formatMoney(simulation.cash_balance, "USD")} />
                  <Metric label="投入本金" value={formatMoney(simulation.invested_capital, "USD")} />
                  <Metric label="昨日實際持倉權重" value={formatPercent(simulation.previous_actual_weight)} />
                  <Metric label="今日實際持倉權重" value={formatPercent(simulation.latest_actual_weight)} />
                  <Metric label="昨日目標權重" value={formatPercent(simulation.previous_target_weight)} />
                  <Metric label="今日目標權重" value={formatPercent(simulation.latest_target_weight)} />
                  <Metric label="目標權重變化" value={signedPercent(simulation.target_weight_delta)} />
                  <Metric label="當日入金" value={formatMoney(simulation.latest_contribution, "USD")} />
                </div>
              ) : (
                <div className="rounded-lg border border-white/[0.04] bg-white/[0.02] p-3 text-sm text-slate-500">模擬倉尚無結果，請確認起始日期落在已匯入資料範圍內，且初始資金大於 0。</div>
              )}
            </div>
          </details>

          <details className="rounded-lg border border-white/[0.04] bg-slate-950/40 p-3">
            <summary className="cursor-pointer text-sm font-semibold text-slate-300">診斷資訊與採用參數</summary>
            <div className="mt-4 grid gap-2 md:grid-cols-2 xl:grid-cols-3">
              {Object.entries(activeState?.diagnostics ?? {}).map(([key, value]) => (
                <div key={key} className="rounded-lg border border-white/[0.04] bg-white/[0.02] p-3">
                  <div className="text-xs text-slate-500">{diagLabels[key] ?? key}</div>
                  <div className="mt-1 font-mono text-sm text-slate-100">{formatNumber(value)}</div>
                </div>
              ))}
            </div>
            <pre className="mt-4 max-h-72 overflow-auto text-xs leading-relaxed text-slate-300">{JSON.stringify(activeState?.parameter_values ?? {}, null, 2)}</pre>
          </details>
        </>
      )}

      <div className="flex items-center gap-2 text-xs text-slate-500">
        <Activity className="h-4 w-4" />
        此頁僅供研究判讀，不會送出任何交易指令。
      </div>
    </section>
  );
}

function Metric({ label, value, highlight = false, danger = false }: { label: string; value: string; highlight?: boolean; danger?: boolean }) {
  return (
    <div className="rounded-lg border border-white/[0.04] bg-white/[0.02] p-3">
      <div className="text-xs text-slate-500">{label}</div>
      <div className={cn("mt-1 font-mono text-sm", danger ? "text-[#fecaca]" : highlight ? "text-[#99f6e4]" : "text-slate-100")}>{value}</div>
    </div>
  );
}

function NumberInput({ label, value, min, step = 1, onChange }: { label: string; value: number; min: number; step?: number; onChange: (value: number) => void }) {
  return (
    <label>
      <span className="mb-2 block text-sm text-slate-300">{label}</span>
      <input className="h-11 w-full rounded-lg border border-slate-700 bg-slate-900/80 px-3 text-sm text-slate-100 outline-none focus:border-[#2dd4bf]" type="number" min={min} step={step} value={value} onChange={(event) => onChange(Number(event.target.value))} />
    </label>
  );
}

function ChartCard({
  title,
  description,
  actions,
  summary,
  data,
  axisTicks,
  hoveredPoint,
  layerProps,
  yFormatter,
  lines
}: {
  title: string;
  description: string;
  actions?: ReactNode;
  summary?: ReactNode;
  data: ChartPoint[];
  axisTicks: { ticks: number[]; formatter: (value: number | string) => string };
  hoveredPoint: ChartPoint | null;
  layerProps: HTMLAttributes<HTMLDivElement>;
  yFormatter: (value: number | string) => string;
  lines: Array<[string, string, string]>;
}) {
  return (
    <Card>
      <CardHeader>
        <div>
          <CardTitle>{title}</CardTitle>
          <CardDescription>{description}</CardDescription>
        </div>
        {actions}
      </CardHeader>
      {summary}
      <div className="relative h-72 overflow-hidden rounded-lg border border-white/[0.04] bg-slate-950/30 p-2">
        <ResponsiveContainer width="100%" height="100%">
          <AreaChart data={data} margin={{ left: 0, right: 10, top: 10, bottom: 30 }}>
            <CartesianGrid stroke="rgba(148,163,184,0.08)" vertical={false} />
            <XAxis dataKey="time_ms" ticks={axisTicks.ticks} tickFormatter={axisTicks.formatter} stroke="#64748b" tickLine={false} axisLine={false} fontSize={11} interval={0} minTickGap={24} />
            <YAxis stroke="#64748b" tickLine={false} axisLine={false} fontSize={12} tickFormatter={yFormatter} domain={["auto", "auto"]} />
            {hoveredPoint ? <ReferenceLine x={hoveredPoint.time_ms} stroke="#f8fafc" strokeOpacity={0.35} strokeWidth={1} /> : null}
            <Legend />
            {lines.map(([key, name, color]) => <Area key={key} name={name} type="monotone" dataKey={key} stroke={color} strokeWidth={2} fill="transparent" isAnimationActive={false} connectNulls />)}
          </AreaChart>
        </ResponsiveContainer>
        <div className="absolute inset-x-2 bottom-14 top-2 z-10 select-none rounded-md" {...layerProps} />
      </div>
      {hoveredPoint ? (
        <div className="mt-3 grid gap-2 rounded-lg border border-white/[0.04] bg-slate-950/50 p-3 text-xs md:grid-cols-3 xl:grid-cols-5">
          <Readout label="日期" value={formatFullAxisTime(hoveredPoint.time_ms)} />
          <Readout label="價位 / 點數" value={formatPrice(hoveredPoint.price)} />
          <Readout label="實務模型淨值" value={formatMoney(hoveredPoint.model_nav, "USD")} />
          <Readout label="實務模型淨值日變化" value={signedPercent(hoveredPoint.model_nav_change_pct)} />
          <Readout label="定投淨值" value={formatMoney(hoveredPoint.benchmark, "USD")} />
          <Readout label="定投淨值日變化" value={signedPercent(hoveredPoint.benchmark_change_pct)} />
          <Readout label="實務模型目標權重" value={formatPercent(hoveredPoint.practical_target_weight)} />
          <Readout label="實務模型權重變化" value={signedPercent(hoveredPoint.practical_target_weight_change)} />
          <Readout label="基準模型目標權重" value={formatPercent(hoveredPoint.model_target_weight)} />
          <Readout label="基準模型權重變化" value={signedPercent(hoveredPoint.model_target_weight_change)} />
          <Readout label="空倉參考目標權重" value={formatPercent(hoveredPoint.empty_reference_target_weight)} />
          <Readout label="空倉參考權重變化" value={signedPercent(hoveredPoint.empty_reference_target_weight_change)} />
        </div>
      ) : null}
    </Card>
  );
}

function Readout({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <div className="text-slate-500">{label}</div>
      <div className="mt-1 font-mono text-slate-100">{value}</div>
    </div>
  );
}
