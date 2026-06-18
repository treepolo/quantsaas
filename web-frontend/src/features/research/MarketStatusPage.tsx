import { useEffect, useMemo, useRef, useState, type HTMLAttributes, type MouseEvent as ReactMouseEvent, type ReactNode, type RefCallback } from "react";
import { useQuery } from "@tanstack/react-query";
import { Activity, BarChart3, Gauge, Home, RotateCcw } from "lucide-react";
import { Area, AreaChart, CartesianGrid, Legend, ReferenceLine, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";
import { formatMoney, formatPercent, shortDateTime } from "../../shared/lib/format";
import { researchApi, type ResearchModelPoint, type ResearchStatusItem } from "../../shared/services/research";
import { marketDataApi } from "../../shared/services/marketData";
import { Card, CardDescription, CardHeader, CardTitle } from "../../shared/ui/Card";
import { Button } from "../../shared/ui/Button";
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
};

type ScaleMode = "absolute" | "log";
type ValueMode = "nav" | "relative";
type ChartRange = { start: number; end: number };
type PanDrag = { startX: number; range: ChartRange; width: number };
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
    monthlyDCA: 1000
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

function clampRangeBySize(start: number, size: number, length: number): ChartRange {
  const clampedSize = Math.max(1, Math.min(length, size));
  const nextStart = Math.max(0, Math.min(start, length - clampedSize));
  return { start: nextStart, end: nextStart + clampedSize - 1 };
}

function toChartValue(value: number | undefined, mode: ScaleMode) {
  const safe = Math.max(1, value ?? 1);
  return mode === "log" ? Math.log10(safe) : safe;
}

function fromChartValue(value: number | string, mode: ScaleMode) {
  const numeric = Number(value);
  return mode === "log" ? Math.pow(10, numeric) : numeric;
}

function formatRelativeIndex(value: number) {
  return value.toLocaleString("zh-TW", { minimumFractionDigits: 2, maximumFractionDigits: 2 });
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
  const [hoverIndex, setHoverIndex] = useState<number | null>(null);
  const [isPanning, setIsPanning] = useState(false);
  const rangeRef = useRef<ChartRange | null>(null);
  const chartLengthRef = useRef(0);
  const panDragRef = useRef<PanDrag | null>(null);
  const chartLayerRefs = useRef<Array<HTMLDivElement | null>>([]);

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
        simulation_monthly_dca: settings.monthlyDCA
      }),
    enabled: Boolean(instrumentId),
    refetchInterval: 60_000
  });
  const item = query.data?.items?.[0] as ResearchStatusItem | undefined;
  const ready = item?.status === "ready";
  const chartData = useMemo<ChartPoint[]>(
    () =>
      (item?.model_simulation?.chart_points ?? []).map((point) => ({
        ...point,
        label: formatFullAxisTime(point.time_ms),
        model_nav_value: point.model_nav,
        benchmark_value: point.benchmark
      })),
    [item]
  );

  useEffect(() => {
    setRange(chartData.length ? { start: 0, end: chartData.length - 1 } : null);
  }, [chartData.length]);

  useEffect(() => {
    rangeRef.current = range;
  }, [range]);

  useEffect(() => {
    chartLengthRef.current = chartData.length;
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
  const model = item?.model_simulation;

  function updateHoverFromMouse(event: ReactMouseEvent<HTMLDivElement>) {
    if (visibleChartData.length === 0) {
      setHoverIndex(null);
      return;
    }
    const rect = event.currentTarget.getBoundingClientRect();
    const ratio = Math.min(1, Math.max(0, (event.clientX - rect.left) / Math.max(1, rect.width)));
    setHoverIndex(Math.round((visibleChartData.length - 1) * ratio));
  }

  function zoomRangeFromWheel(deltaY: number, clientX: number, rect: DOMRect) {
    if (chartLengthRef.current < 3) return;
    setRange((currentRange) => {
      const length = chartLengthRef.current;
      if (!currentRange || length < 3) return currentRange;
      const currentSize = currentRange.end - currentRange.start + 1;
      const nextSize = Math.round(currentSize * (deltaY < 0 ? 0.75 : 1.35));
      const clampedSize = Math.max(Math.min(10, length), Math.min(length, nextSize));
      const ratio = Math.min(1, Math.max(0, (clientX - rect.left) / Math.max(1, rect.width)));
      const center = currentRange.start + Math.round((currentSize - 1) * ratio);
      const nextStart = center - Math.round((clampedSize - 1) * ratio);
      return clampRangeBySize(nextStart, clampedSize, length);
    });
  }

  useEffect(() => {
    const layers = chartLayerRefs.current.filter((element): element is HTMLDivElement => Boolean(element));
    const cleanups = layers.map((element) => {
      const handleWheel = (event: WheelEvent) => {
        event.preventDefault();
        event.stopPropagation();
        zoomRangeFromWheel(event.deltaY, event.clientX, element.getBoundingClientRect());
      };
      element.addEventListener("wheel", handleWheel, { passive: false });
      return () => element.removeEventListener("wheel", handleWheel);
    });
    return () => cleanups.forEach((cleanup) => cleanup());
  }, [chartData.length]);

  function setChartLayerRef(index: number): RefCallback<HTMLDivElement> {
    return (node) => {
      chartLayerRefs.current[index] = node;
    };
  }

  function beginPan(event: ReactMouseEvent<HTMLDivElement>) {
    if (![0, 1].includes(event.button) || !rangeRef.current || chartLengthRef.current < 2) return;
    updateHoverFromMouse(event);
    event.preventDefault();
    event.stopPropagation();
    panDragRef.current = {
      startX: event.clientX,
      range: rangeRef.current,
      width: Math.max(1, event.currentTarget.clientWidth)
    };
    setIsPanning(true);
  }

  function movePan(event: ReactMouseEvent<HTMLDivElement>) {
    updateHoverFromMouse(event);
    const drag = panDragRef.current;
    if (!drag) return;
    event.preventDefault();
    event.stopPropagation();
    const size = drag.range.end - drag.range.start + 1;
    const barsPerPixel = size / drag.width;
    const shift = Math.round((drag.startX - event.clientX) * barsPerPixel);
    setRange(clampRangeBySize(drag.range.start + shift, size, chartLengthRef.current));
  }

  function endPan(event: ReactMouseEvent<HTMLDivElement>) {
    if (panDragRef.current) {
      event.preventDefault();
      event.stopPropagation();
      panDragRef.current = null;
      setIsPanning(false);
    }
  }

  function chartLayerProps() {
    return {
      onMouseDown: beginPan,
      onMouseMove: movePan,
      onMouseUp: endPan,
      onMouseLeave: (event: ReactMouseEvent<HTMLDivElement>) => {
        endPan(event);
        setHoverIndex(null);
      }
    };
  }

  function resetRange() {
    setRange(chartData.length ? { start: 0, end: chartData.length - 1 } : null);
  }

  function updateRangeStart(value: number) {
    if (!range) return;
    setRange({ start: Math.min(value, range.end), end: range.end });
  }

  function updateRangeEnd(value: number) {
    if (!range) return;
    setRange({ start: range.start, end: Math.max(value, range.start) });
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
      </Card>

      {query.isLoading ? <Card className="p-4 text-sm text-slate-500">載入中...</Card> : null}
      {query.error ? <Card className="p-4 text-sm text-[#fecaca]">{String(query.error.message)}</Card> : null}

      {!ready ? (
        <Card className="p-4 text-sm text-slate-500">
          {item?.status === "missing_champion" ? "尚未有這個標的的已採用參數。" : "尚未有足夠的完成日 K 資料。"}
        </Card>
      ) : (
        <>
          <Card className="border-[#2dd4bf]/20">
            <CardHeader>
              <div>
                <CardTitle>{selectedInstrument?.display_name ?? item?.instrument.display_name}</CardTitle>
                <CardDescription>{item?.symbol} · {item?.data_source} · {item?.interval} · {item?.execution_mode}</CardDescription>
              </div>
              <Gauge className="h-5 w-5 text-[#99f6e4]" />
            </CardHeader>
            <div className="grid gap-3 md:grid-cols-3 xl:grid-cols-4">
              <Metric label="市場狀態" value={stateLabel(item?.market_state)} />
              <Metric label="最新完成日 K" value={item?.latest_bar ? `${shortDateTime(item.latest_bar.time)} · ${formatNumber(item.latest_bar.close)}` : "-"} />
              <Metric label="基準模型淨值" value={model ? formatMoney(model.latest_nav, "USD") : "-"} highlight />
              <Metric label="基準模型淨值日變化" value={signedPercent(model?.nav_change_pct)} danger={(model?.nav_change_pct ?? 0) < 0} />
              <Metric label="定投淨值" value={model ? formatMoney(model.latest_benchmark, "USD") : "-"} />
              <Metric label="定投淨值日變化" value={signedPercent(model?.benchmark_change_pct)} danger={(model?.benchmark_change_pct ?? 0) < 0} />
              <Metric label="基準模型目標權重" value={model ? formatPercent(model.latest_model_target_weight) : "-"} highlight />
              <Metric label="基準模型權重變化" value={signedPercent(model?.latest_model_target_weight_change)} />
              <Metric label="空倉參考目標權重" value={model ? formatPercent(model.latest_empty_reference_target_weight) : "-"} />
              <Metric label="空倉參考權重變化" value={signedPercent(model?.latest_empty_reference_target_weight_change)} />
            </div>
          </Card>

          <ChartCard title="基準模型淨值走勢" description="基準模型與定投基準使用相同本金與定期入金設定。" actions={navChartControls} summary={navChartSummary} data={visibleChartData} axisTicks={axisTicks} hoveredPoint={hoveredPoint} isPanning={isPanning} layerProps={chartLayerProps()} layerRef={setChartLayerRef(0)} yFormatter={navAxisFormatter} lines={[["model_nav_value", "基準模型", "#2dd4bf"], ["benchmark_value", "定投基準", "#64748b"]]} />
          <RangeControls range={range} total={chartData.length} chartData={chartData} onStart={updateRangeStart} onEnd={updateRangeEnd} onReset={resetRange} />
          <ChartCard title="空倉參考目標權重每日值" description="每天獨立假設昨日空倉後得到的參考目標水準。" data={visibleChartData} axisTicks={axisTicks} hoveredPoint={hoveredPoint} isPanning={isPanning} layerProps={chartLayerProps()} layerRef={setChartLayerRef(1)} yFormatter={(value) => formatPercent(Number(value))} lines={[["empty_reference_target_weight", "空倉參考", "#a78bfa"]]} />
          <RangeControls range={range} total={chartData.length} chartData={chartData} onStart={updateRangeStart} onEnd={updateRangeEnd} onReset={resetRange} />
          <ChartCard title="空倉參考目標權重每日變化" description="今日空倉參考目標權重減昨日空倉參考目標權重。" data={visibleChartData} axisTicks={axisTicks} hoveredPoint={hoveredPoint} isPanning={isPanning} layerProps={chartLayerProps()} layerRef={setChartLayerRef(2)} yFormatter={(value) => signedPercent(Number(value))} lines={[["empty_reference_target_weight_change", "空倉參考變化", "#f472b6"]]} />
          <RangeControls range={range} total={chartData.length} chartData={chartData} onStart={updateRangeStart} onEnd={updateRangeEnd} onReset={resetRange} />
          <ChartCard title="基準模型目標權重每日值" description="基準模型路徑逐日產生的目標水準。" data={visibleChartData} axisTicks={axisTicks} hoveredPoint={hoveredPoint} isPanning={isPanning} layerProps={chartLayerProps()} layerRef={setChartLayerRef(3)} yFormatter={(value) => formatPercent(Number(value))} lines={[["model_target_weight", "基準模型", "#38bdf8"]]} />
          <RangeControls range={range} total={chartData.length} chartData={chartData} onStart={updateRangeStart} onEnd={updateRangeEnd} onReset={resetRange} />
          <ChartCard title="基準模型目標權重每日變化" description="今日基準模型目標權重減昨日基準模型目標權重。" data={visibleChartData} axisTicks={axisTicks} hoveredPoint={hoveredPoint} isPanning={isPanning} layerProps={chartLayerProps()} layerRef={setChartLayerRef(4)} yFormatter={(value) => signedPercent(Number(value))} lines={[["model_target_weight_change", "基準模型變化", "#f59e0b"]]} />
          <RangeControls range={range} total={chartData.length} chartData={chartData} onStart={updateRangeStart} onEnd={updateRangeEnd} onReset={resetRange} />

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
              {Object.entries(item?.diagnostics ?? {}).map(([key, value]) => (
                <div key={key} className="rounded-lg border border-white/[0.04] bg-white/[0.02] p-3">
                  <div className="text-xs text-slate-500">{diagLabels[key] ?? key}</div>
                  <div className="mt-1 font-mono text-sm text-slate-100">{formatNumber(value)}</div>
                </div>
              ))}
            </div>
            <pre className="mt-4 max-h-72 overflow-auto text-xs leading-relaxed text-slate-300">{JSON.stringify(item?.parameter_values ?? {}, null, 2)}</pre>
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

function NumberInput({ label, value, min, onChange }: { label: string; value: number; min: number; onChange: (value: number) => void }) {
  return (
    <label>
      <span className="mb-2 block text-sm text-slate-300">{label}</span>
      <input className="h-11 w-full rounded-lg border border-slate-700 bg-slate-900/80 px-3 text-sm text-slate-100 outline-none focus:border-[#2dd4bf]" type="number" min={min} step="100" value={value} onChange={(event) => onChange(Number(event.target.value))} />
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
  isPanning,
  layerProps,
  layerRef,
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
  isPanning: boolean;
  layerProps: HTMLAttributes<HTMLDivElement>;
  layerRef: RefCallback<HTMLDivElement>;
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
            <Tooltip contentStyle={{ background: "#020617", border: "1px solid rgba(255,255,255,0.08)", borderRadius: 8 }} formatter={(value, name) => [yFormatter(value as number), name]} labelFormatter={(value) => formatFullAxisTime(value)} />
            <Legend />
            {lines.map(([key, name, color]) => <Area key={key} name={name} type="monotone" dataKey={key} stroke={color} strokeWidth={2} fill="transparent" isAnimationActive={false} connectNulls />)}
          </AreaChart>
        </ResponsiveContainer>
        <div ref={layerRef} className={cn("absolute inset-x-2 bottom-14 top-2 z-10 cursor-grab select-none touch-none overscroll-contain rounded-md", isPanning && "cursor-grabbing")} {...layerProps} />
      </div>
      {hoveredPoint ? (
        <div className="mt-3 grid gap-2 rounded-lg border border-white/[0.04] bg-slate-950/50 p-3 text-xs md:grid-cols-3 xl:grid-cols-5">
          <Readout label="日期" value={formatFullAxisTime(hoveredPoint.time_ms)} />
          <Readout label="基準模型淨值" value={formatMoney(hoveredPoint.model_nav, "USD")} />
          <Readout label="基準模型淨值日變化" value={signedPercent(hoveredPoint.model_nav_change_pct)} />
          <Readout label="定投淨值" value={formatMoney(hoveredPoint.benchmark, "USD")} />
          <Readout label="定投淨值日變化" value={signedPercent(hoveredPoint.benchmark_change_pct)} />
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

function RangeControls({
  range,
  total,
  chartData,
  onStart,
  onEnd,
  onReset
}: {
  range: ChartRange | null;
  total: number;
  chartData: ChartPoint[];
  onStart: (value: number) => void;
  onEnd: (value: number) => void;
  onReset: () => void;
}) {
  if (!range || total <= 1) return null;
  return (
    <Card className="p-4">
      <div className="mb-3 flex items-center justify-between gap-3 text-xs text-slate-500">
        <span>{formatFullAxisTime(chartData[range.start]?.time_ms ?? 0)}</span>
        <Button icon={RotateCcw} variant="secondary" onClick={onReset}>重設</Button>
        <span>{formatFullAxisTime(chartData[range.end]?.time_ms ?? 0)}</span>
      </div>
      <div className="grid gap-2 md:grid-cols-2">
        <input type="range" min={0} max={total - 1} value={range.start} onChange={(event) => onStart(Number(event.target.value))} />
        <input type="range" min={0} max={total - 1} value={range.end} onChange={(event) => onEnd(Number(event.target.value))} />
      </div>
    </Card>
  );
}
