import { useEffect, useMemo, useState, type MouseEvent as ReactMouseEvent } from "react";
import { useQueries, useQuery } from "@tanstack/react-query";
import { CandlestickChart, LineChart } from "lucide-react";
import { groupMarketSources, marketSourceKey, marketSourceLabel } from "../../shared/lib/marketChartSources";
import { marketDataApi, type MarketChartSource, type MarketVersionBar } from "../../shared/services/marketData";
import { Button } from "../../shared/ui/Button";
import { Card, CardDescription, CardHeader, CardTitle } from "../../shared/ui/Card";
import { ChartRangeSlider, type ChartRange } from "../../shared/ui/ChartRangeSlider";

type ChartKind = "line" | "candlestick";
type CompareMode = "price" | "relative";
type ScaleMode = "linear" | "log";

const colors = ["#2dd4bf", "#38bdf8", "#f59e0b", "#a78bfa"];
const inputClass = "min-h-10 w-full rounded-lg border border-white/10 bg-slate-950/70 px-3 text-sm text-slate-200 outline-none focus:border-teal-400/50";

function dateValue(ms: number) { return new Date(ms).toISOString().slice(0, 10); }
function dayStart(value: string) { return new Date(`${value}T00:00:00.000Z`).getTime(); }
function dayEnd(value: string) { return new Date(`${value}T23:59:59.999Z`).getTime(); }
function dateLabel(ms: number) { return new Date(ms).toLocaleDateString("zh-TW", { timeZone: "UTC" }); }
function valueLabel(value: number, relative: boolean) { return relative ? `${value.toFixed(2)}` : value >= 1000 ? value.toLocaleString("zh-TW", { maximumFractionDigits: 0 }) : value.toLocaleString("zh-TW", { maximumFractionDigits: 4 }); }

function downsample<T>(rows: T[], maximum: number) {
  if (rows.length <= maximum) return rows;
  const step = rows.length / maximum;
  return Array.from({ length: maximum }, (_, index) => rows[Math.min(rows.length - 1, Math.floor(index * step))]);
}

export function MarketPriceExplorer() {
  const sourcesQuery = useQuery({ queryKey: ["market-chart-sources"], queryFn: marketDataApi.chartSources });
  const sources = sourcesQuery.data?.items ?? [];
  const grouped = useMemo(() => groupMarketSources(sources), [sources]);
  const [primaryKey, setPrimaryKey] = useState("");
  const [selectedKeys, setSelectedKeys] = useState<string[]>([]);
  const [startDate, setStartDate] = useState("");
  const [endDate, setEndDate] = useState("");
  const [chartKind, setChartKind] = useState<ChartKind>("line");
  const [compareMode, setCompareMode] = useState<CompareMode>("price");
  const [scaleMode, setScaleMode] = useState<ScaleMode>("linear");
  const [range, setRange] = useState<ChartRange | null>(null);
  const [hoverIndex, setHoverIndex] = useState<number | null>(null);

  const primary = sources.find((source) => marketSourceKey(source) === primaryKey) ?? sources.find((source) => source.artifact_kind === "source_snapshot") ?? sources[0];
  useEffect(() => {
    if (!primary) return;
    const key = marketSourceKey(primary);
    if (!primaryKey) setPrimaryKey(key);
    if (selectedKeys.length === 0) setSelectedKeys([key]);
  }, [primary, primaryKey, selectedKeys.length]);
  useEffect(() => {
    if (!primary) return;
    setStartDate(dateValue(primary.start_time_ms));
    setEndDate(dateValue(primary.end_time_ms));
    setRange(null);
  }, [primaryKey, primary?.start_time_ms, primary?.end_time_ms]);

  const selectedSources = selectedKeys.map((key) => sources.find((source) => marketSourceKey(source) === key)).filter((source): source is MarketChartSource => Boolean(source));
  const requests = useQueries({ queries: selectedSources.map((source) => ({
    queryKey: ["market-chart-bars", marketSourceKey(source), startDate, endDate],
    queryFn: () => marketDataApi.chartBars({ instrumentId: source.version_id ? undefined : source.instrument.id, versionId: source.version_id, interval: source.interval, startTimeMs: dayStart(startDate), endTimeMs: dayEnd(endDate), limit: 5000 }),
    enabled: Boolean(startDate && endDate && dayEnd(endDate) >= dayStart(startDate))
  })) });
  const series = selectedSources.map((source, index) => ({ source, color: colors[index % colors.length], bars: requests[index]?.data?.rows ?? [] }));
  const primarySeriesIndex = primary ? Math.max(0, selectedSources.findIndex((source) => marketSourceKey(source) === marketSourceKey(primary))) : 0;
  const primaryBars = series[primarySeriesIndex]?.bars ?? [];
  useEffect(() => { setRange(primaryBars.length ? { start: 0, end: primaryBars.length - 1 } : null); }, [primaryKey, primaryBars.length, startDate, endDate]);

  const visiblePrimary = range ? primaryBars.slice(range.start, range.end + 1) : primaryBars;
  const visibleStart = visiblePrimary[0]?.open_time ?? 0;
  const visibleEnd = visiblePrimary[visiblePrimary.length - 1]?.open_time ?? 0;
  const visibleSeries = series.map((item) => {
    const bars = item.bars.filter((bar) => (!visibleStart || bar.open_time >= visibleStart) && (!visibleEnd || bar.open_time <= visibleEnd));
    const base = bars[0]?.close || 1;
    const factor = compareMode === "relative" ? 100 / base : 1;
    return { ...item, bars: bars.map((bar) => ({ ...bar, open: bar.open * factor, high: bar.high * factor, low: bar.low * factor, close: bar.close * factor })) };
  });
  const allValues = visibleSeries.flatMap((item) => item.bars.flatMap((bar) => [bar.low, bar.high])).filter((value) => Number.isFinite(value) && (scaleMode !== "log" || value > 0));
  const rawMin = allValues.length ? Math.min(...allValues) : 0;
  const rawMax = allValues.length ? Math.max(...allValues) : 1;
  const padded = Math.max((rawMax - rawMin) * 0.06, Math.abs(rawMax) * 0.002, 1e-9);
  const domainMin = scaleMode === "log" ? Math.log(Math.max(rawMin, 1e-12)) : rawMin - padded;
  const domainMax = scaleMode === "log" ? Math.log(Math.max(rawMax, 1e-12)) : rawMax + padded;
  const timeMin = visibleStart || Math.min(...visibleSeries.flatMap((item) => item.bars.map((bar) => bar.open_time)), Date.now());
  const timeMax = visibleEnd || Math.max(...visibleSeries.flatMap((item) => item.bars.map((bar) => bar.open_time)), timeMin + 1);
  const width = 920, height = 430, left = 72, right = 18, top = 20, bottom = 62;
  const plotWidth = width - left - right, plotHeight = height - top - bottom;
  const x = (time: number) => left + ((time - timeMin) / Math.max(1, timeMax - timeMin)) * plotWidth;
  const y = (value: number) => {
    const scaled = scaleMode === "log" ? Math.log(Math.max(value, 1e-12)) : value;
    return top + ((domainMax - scaled) / Math.max(1e-12, domainMax - domainMin)) * plotHeight;
  };
  const yTicks = Array.from({ length: 6 }, (_, index) => {
    const scaled = domainMax - (index / 5) * (domainMax - domainMin);
    return { y: top + (index / 5) * plotHeight, value: scaleMode === "log" ? Math.exp(scaled) : scaled };
  });
  const xTicks = Array.from({ length: 6 }, (_, index) => timeMin + (index / 5) * (timeMax - timeMin));
  const hoverBar = hoverIndex === null ? undefined : visiblePrimary[hoverIndex];

  function choosePrimary(key: string) {
    setPrimaryKey(key);
    setSelectedKeys((current) => current.includes(key) ? current : [key, ...current].slice(0, 4));
  }
  function toggleSource(key: string) {
    setSelectedKeys((current) => {
      if (current.includes(key)) return current.length === 1 ? current : current.filter((item) => item !== key);
      return current.length >= 4 ? current : [...current, key];
    });
  }
  function updateHover(event: ReactMouseEvent<SVGSVGElement>) {
    if (!visiblePrimary.length) return setHoverIndex(null);
    const rect = event.currentTarget.getBoundingClientRect();
    const ratio = Math.max(0, Math.min(1, (event.clientX - rect.left) / Math.max(1, rect.width)));
    setHoverIndex(Math.round(ratio * (visiblePrimary.length - 1)));
  }

  return <Card>
    <CardHeader><div><CardTitle>查看與比較行情走勢</CardTitle><CardDescription>原始行情、參考指標與各類研究行情分開列出；最多可疊合四組資料。</CardDescription></div></CardHeader>
    <div className="grid gap-3 lg:grid-cols-4">
      <label className="text-xs text-slate-400 lg:col-span-2">主要行情<select className={`${inputClass} mt-1`} value={primary ? marketSourceKey(primary) : ""} onChange={(event) => choosePrimary(event.target.value)}>{grouped.map((group) => <optgroup key={group.category} label={group.label}>{group.items.map((source) => <option key={marketSourceKey(source)} value={marketSourceKey(source)}>{marketSourceLabel(source)}</option>)}</optgroup>)}</select></label>
      <label className="text-xs text-slate-400">開始日期<input className={`${inputClass} mt-1`} type="date" value={startDate} onChange={(event) => setStartDate(event.target.value)} /></label>
      <label className="text-xs text-slate-400">結束日期<input className={`${inputClass} mt-1`} type="date" value={endDate} onChange={(event) => setEndDate(event.target.value)} /></label>
    </div>
    <div className="mt-4 grid gap-3 xl:grid-cols-[minmax(260px,.42fr)_minmax(0,1.58fr)]">
      <div className="max-h-[430px] space-y-4 overflow-auto rounded-lg border border-white/[0.06] bg-white/[0.02] p-3">
        <div className="text-xs leading-5 text-slate-500">勾選要疊合的資料，最多四組。主要行情決定 K 線與下方區間滑桿。</div>
        {grouped.map((group) => <div key={group.category}><div className="mb-2 text-xs font-semibold text-slate-300">{group.label}</div><div className="space-y-1">{group.items.map((source) => { const key = marketSourceKey(source); const checked = selectedKeys.includes(key), isPrimary = primary ? key === marketSourceKey(primary) : false; return <label key={key} className="flex cursor-pointer items-start gap-2 rounded px-2 py-1.5 text-xs hover:bg-white/[0.04]"><input className="mt-0.5" type="checkbox" checked={checked} disabled={isPrimary} onChange={() => toggleSource(key)} /><span><span className="block text-slate-300">{source.display_name}{isPrimary ? "（主要）" : ""}</span><span className="text-slate-600">{source.interval} · {source.bar_count.toLocaleString("zh-TW")} 根{source.version_id ? ` · 版本 #${source.version_id}` : ""}</span></span></label>})}</div></div>)}
      </div>
      <div>
        <div className="mb-3 flex flex-wrap gap-2">
          <Button size="sm" variant={chartKind === "line" ? "primary" : "secondary"} icon={LineChart} onClick={() => setChartKind("line")}>收盤價走勢</Button>
          <Button size="sm" variant={chartKind === "candlestick" ? "primary" : "secondary"} icon={CandlestickChart} onClick={() => setChartKind("candlestick")}>K 線</Button>
          <Button size="sm" variant={compareMode === "price" ? "primary" : "secondary"} onClick={() => setCompareMode("price")}>實際價格</Button>
          <Button size="sm" variant={compareMode === "relative" ? "primary" : "secondary"} onClick={() => setCompareMode("relative")}>起點對齊 100</Button>
          <Button size="sm" variant={scaleMode === "linear" ? "primary" : "secondary"} onClick={() => setScaleMode("linear")}>一般刻度</Button>
          <Button size="sm" variant={scaleMode === "log" ? "primary" : "secondary"} disabled={rawMin <= 0} onClick={() => setScaleMode("log")}>對數刻度</Button>
        </div>
        {requests.some((query) => query.isLoading) ? <div className="flex h-[430px] items-center justify-center rounded-lg border border-white/[0.06] text-sm text-slate-500">正在載入行情…</div> : visiblePrimary.length ? <>
          <div className="relative overflow-hidden rounded-lg border border-white/[0.06] bg-slate-950/60">
            <svg viewBox={`0 0 ${width} ${height}`} className="w-full" onMouseMove={updateHover} onMouseLeave={() => setHoverIndex(null)}>
              {yTicks.map((tick, index) => <g key={`y-${index}`}><line x1={left} x2={width-right} y1={tick.y} y2={tick.y} stroke="rgba(148,163,184,.09)"/><text x={left-8} y={tick.y+4} textAnchor="end" fill="#64748b" fontSize="11">{valueLabel(tick.value, compareMode === "relative")}</text></g>)}
              {xTicks.map((tick, index) => <g key={`x-${index}`}><line x1={x(tick)} x2={x(tick)} y1={top} y2={height-bottom} stroke="rgba(148,163,184,.04)"/><text x={x(tick)} y={height-30} textAnchor="middle" fill="#64748b" fontSize="11">{dateLabel(tick)}</text></g>)}
              {chartKind === "candlestick" && visibleSeries[primarySeriesIndex] ? downsample(visibleSeries[primarySeriesIndex].bars, 650).map((bar) => { const up = bar.close >= bar.open; const color = up ? "#22c55e" : "#ef4444"; const candleWidth = Math.max(1.2, Math.min(10, plotWidth / Math.max(1, visibleSeries[primarySeriesIndex].bars.length) * .72)); return <g key={bar.open_time}><line x1={x(bar.open_time)} x2={x(bar.open_time)} y1={y(bar.high)} y2={y(bar.low)} stroke={color} strokeWidth="1"/><rect x={x(bar.open_time)-candleWidth/2} y={Math.min(y(bar.open),y(bar.close))} width={candleWidth} height={Math.max(1,Math.abs(y(bar.open)-y(bar.close)))} fill={color}/></g> }) : null}
              {chartKind === "candlestick" ? visibleSeries.map((item, index) => { if (index === primarySeriesIndex) return null; const rows = downsample(item.bars, 1200); const points = rows.map((bar) => `${x(bar.open_time)},${y(bar.close)}`).join(" "); return <polyline key={marketSourceKey(item.source)} points={points} fill="none" stroke={item.color} strokeWidth="2" opacity=".9"/> }) : null}
              {chartKind === "line" && visibleSeries.map((item) => { const rows = downsample(item.bars, 1200); return <polyline key={`line-${marketSourceKey(item.source)}`} points={rows.map((bar) => `${x(bar.open_time)},${y(bar.close)}`).join(" ")} fill="none" stroke={item.color} strokeWidth="2"/> })}
              {hoverBar ? <line x1={x(hoverBar.open_time)} x2={x(hoverBar.open_time)} y1={top} y2={height-bottom} stroke="#e2e8f0" strokeDasharray="4 4" opacity=".55"/> : null}
            </svg>
            {hoverBar ? <div className="pointer-events-none absolute left-3 top-3 max-w-[88%] rounded-lg border border-white/10 bg-slate-950/95 px-3 py-2 text-xs shadow-xl"><div className="mb-1 font-semibold text-slate-200">{dateLabel(hoverBar.open_time)}</div>{visibleSeries.map((item) => { const bar = item.bars.find((row) => row.open_time === hoverBar.open_time); return <div key={marketSourceKey(item.source)} className="flex flex-wrap gap-x-2" style={{color:item.color}}><span>{item.source.display_name}</span>{bar ? <span>開 {valueLabel(bar.open, compareMode === "relative")}　高 {valueLabel(bar.high, compareMode === "relative")}　低 {valueLabel(bar.low, compareMode === "relative")}　收 {valueLabel(bar.close, compareMode === "relative")}</span> : <span>此日無資料</span>}</div>})}</div> : null}
          </div>
          <ChartRangeSlider range={range} total={primaryBars.length} startLabel={visiblePrimary[0] ? dateLabel(visiblePrimary[0].open_time) : ""} endLabel={visiblePrimary.at(-1) ? dateLabel(visiblePrimary.at(-1)!.open_time) : ""} onChange={setRange} onReset={() => setRange(primaryBars.length ? {start:0,end:primaryBars.length-1} : null)} />
          <div className="mt-3 flex flex-wrap gap-x-4 gap-y-1 text-xs">{visibleSeries.map((item) => <span key={marketSourceKey(item.source)} style={{color:item.color}}>● {item.source.display_name}</span>)}</div>
        </> : <div className="flex h-[430px] items-center justify-center rounded-lg border border-dashed border-white/10 text-sm text-slate-500">這個日期區間沒有行情資料。</div>}
        {requests.find((query) => query.error)?.error ? <div className="mt-3 text-sm text-rose-300">{String(requests.find((query) => query.error)?.error?.message)}</div> : null}
        <div className="mt-2 text-xs leading-5 text-slate-600">單次最多讀取 5,000 根 K 線；綠色代表收盤高於或等於開盤，紅色代表收盤低於開盤。</div>
      </div>
    </div>
  </Card>;
}
