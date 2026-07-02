import { FormEvent, useEffect, useMemo, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Activity, AlertTriangle, CheckCircle2, FlaskConical, Plus, Save, Square, TerminalSquare, Trash2, X } from "lucide-react";
import { formatMoney, formatPercent, relativeTime, shortDateTime } from "../../shared/lib/format";
import { evolutionApi, type CreateTaskInput, type EvolutionTask, type GeneObservation, type GeneObservationAxis, type GeneObservationQuery, type GenomeRecord, type TraceMode } from "../../shared/services/evolution";
import { marketDataApi } from "../../shared/services/marketData";
import { Button } from "../../shared/ui/Button";
import { Card, CardDescription, CardHeader, CardTitle } from "../../shared/ui/Card";
import { StatusBadge } from "../../shared/ui/StatusBadge";
import { cn } from "../../shared/lib/cn";

const intervalLabels: Record<string, string> = { "1d": "日 K", "1h": "1 小時", "15m": "15 分鐘", "5m": "5 分鐘", "1m": "1 分鐘", "1s": "1 秒", "1w": "週 K", "1M": "月 K" };
const executionModes = [
  ["close_next_open", "隔日開盤", "用收盤訊號，假設下一根開盤才調整"],
  ["close_same_bar", "收盤同根", "用當根收盤價作為研究判斷基準"],
  ["preclose_10m", "收盤前 10 分鐘", "需要額外快照資料，缺資料時不會假裝可用"]
] as const;
const traceModeOptions: Array<[TraceMode, string, string]> = [
  ["off", "關閉", "不產生原始追蹤"],
  ["summary", "摘要", "只顯示任務與世代摘要"],
  ["detailed", "詳細", "顯示資料視窗、個體評估與世代資訊"],
  ["full", "逐筆", "顯示策略步進事件，會拖慢運算"]
];

const SEARCH_INITIAL_CAPITAL = 1_000_000;

type ReferenceIndicatorDraft = {
  instrument_id: string;
  interval: string;
};

function dateInputValue(date: Date) {
  return date.toISOString().slice(0, 10);
}

function dayStartMs(value: string) {
  return new Date(`${value}T00:00:00.000Z`).getTime();
}

function dayEndMs(value: string) {
  return new Date(`${value}T23:59:59.999Z`).getTime();
}

function msToDateInput(value?: number) {
  return value ? new Date(value).toISOString().slice(0, 10) : "";
}

function monitorValue(value: string | number | undefined) {
  if (value === undefined || value === "") return "等待回報";
  return value;
}

function formatUnits(value?: number) {
  if (value === undefined || !Number.isFinite(value)) return "-";
  return Math.max(0, Math.round(value)).toLocaleString("zh-TW");
}

function formatUnitRate(value?: number) {
  if (value === undefined || !Number.isFinite(value) || value <= 0) return "-";
  return `${Math.round(value).toLocaleString("zh-TW")} 筆/秒`;
}

function formatDurationSeconds(value?: number) {
  if (value === undefined || !Number.isFinite(value) || value <= 0) return "-";
  const total = Math.round(value);
  const hours = Math.floor(total / 3600);
  const minutes = Math.floor((total % 3600) / 60);
  const seconds = total % 60;
  if (hours > 0) return `${hours} 小時 ${minutes} 分`;
  if (minutes > 0) return `${minutes} 分 ${seconds} 秒`;
  return `${seconds} 秒`;
}

function useAnimatedNumber(target: number, durationMs = 500) {
  const [display, setDisplay] = useState(target);
  const currentRef = useRef(target);
  useEffect(() => {
    const start = currentRef.current;
    const diff = target - start;
    if (Math.abs(diff) < 1) {
      currentRef.current = target;
      setDisplay(target);
      return;
    }
    let frame = 0;
    const startedAt = performance.now();
    const tick = (now: number) => {
      const progress = Math.min(1, (now - startedAt) / durationMs);
      const eased = 1 - Math.pow(1 - progress, 3);
      const next = start + diff * eased;
      currentRef.current = next;
      setDisplay(next);
      if (progress < 1) frame = requestAnimationFrame(tick);
    };
    frame = requestAnimationFrame(tick);
    return () => cancelAnimationFrame(frame);
  }, [durationMs, target]);
  return display;
}

function formatTraceValue(value: unknown): string {
  if (value === null || value === undefined) return "null";
  if (typeof value === "number") return Number.isInteger(value) ? value.toLocaleString("zh-TW") : value.toFixed(6);
  if (typeof value === "string" || typeof value === "boolean") return String(value);
  return JSON.stringify(value);
}

function labelForInstrument(id?: string, names?: Record<string, string>) {
  return names?.[id ?? ""] ?? id ?? "未指定";
}

function JsonPreview({ value }: { value?: Record<string, unknown> | null }) {
  if (!value) return <div className="text-sm text-slate-500">尚未產生參數</div>;
  return (
    <pre className="max-h-72 overflow-auto rounded-lg border border-white/[0.04] bg-slate-950/70 p-3 text-xs leading-relaxed text-slate-300">
      {JSON.stringify(value, null, 2)}
    </pre>
  );
}

function TraceConsole({ task }: { task: EvolutionTask }) {
  const initialMode = task.trace_mode ?? "off";
  const [open, setOpen] = useState(initialMode !== "off");
  const [mode, setMode] = useState<TraceMode>(initialMode);
  const queryClient = useQueryClient();
  const traceQuery = useQuery({
    queryKey: ["evolution-trace", task.id, open, mode],
    queryFn: () => evolutionApi.trace(task.id, mode === "full" ? 1200 : 600),
    enabled: open && mode !== "off",
    refetchInterval: open && mode !== "off" ? 1000 : false
  });
  const modeMutation = useMutation({
    mutationFn: (nextMode: TraceMode) => evolutionApi.setTraceMode(task.id, nextMode),
    onSuccess: (result) => {
      setMode(result.mode);
      if (result.mode === "off") setOpen(false);
      queryClient.invalidateQueries({ queryKey: ["evolution-trace", task.id] });
    }
  });
  const visibleEvents = (traceQuery.data?.events ?? []).slice(-500);
  return (
    <Card>
      <CardHeader>
        <div>
          <CardTitle>原始追蹤</CardTitle>
          <CardDescription>泛用 trace viewer；關閉視窗時停止前端輪詢，逐筆模式會增加後端追蹤成本。</CardDescription>
        </div>
        <Button icon={TerminalSquare} variant="secondary" onClick={() => setOpen((value) => !value)}>
          {open ? "收合" : "展開"}
        </Button>
      </CardHeader>
      <div className="space-y-3">
        <div className="flex flex-wrap gap-2">
          {traceModeOptions.map(([value, label, description]) => (
            <button key={value} type="button" title={description} className={cn("rounded-lg border px-3 py-2 text-sm transition", mode === value ? "border-[#2dd4bf]/40 bg-[#2dd4bf]/10 text-[#99f6e4]" : "border-white/[0.04] text-slate-400 hover:text-slate-200")} onClick={() => modeMutation.mutate(value)}>
              {label}
            </button>
          ))}
        </div>
        {mode === "off" ? <div className="text-xs text-slate-500">追蹤已關閉，不產生後端 trace，也不輪詢事件。</div> : null}
        {mode === "full" ? <div className="text-xs text-[#fde68a]">逐筆追蹤會拖慢優化，只建議短時間觀察。</div> : null}
        {open && mode !== "off" ? (
          <div className="h-[28rem] overflow-auto rounded-lg border border-white/[0.06] bg-slate-950 p-3 font-mono text-xs leading-relaxed text-slate-300">
            {traceQuery.isLoading ? <div className="text-slate-500">載入追蹤資料...</div> : null}
            {!traceQuery.isLoading && visibleEvents.length === 0 ? <div className="text-slate-500">尚無追蹤事件。</div> : null}
            {visibleEvents.map((event) => (
              <div key={event.id} className="border-b border-white/[0.03] py-1">
                <span className="text-slate-500">#{event.id}</span> <span className="text-[#99f6e4]">{shortDateTime(event.time)}</span> <span className="text-[#fde68a]">{event.source}</span> <span className="text-[#c4b5fd]">{event.scope}</span> <span>{event.message}</span>
                {event.fields ? (
                  <div className="mt-1 break-words pl-4 text-slate-400">
                    {Object.entries(event.fields).map(([key, value]) => (
                      <span key={key} className="mr-3 inline-block">
                        <span className="text-slate-500">{key}=</span>{formatTraceValue(value)}
                      </span>
                    ))}
                  </div>
                ) : null}
              </div>
            ))}
          </div>
        ) : null}
      </div>
    </Card>
  );
}

function CurrentBestCard({ task }: { task: EvolutionTask }) {
  return (
    <Card>
      <CardHeader>
        <div>
          <CardTitle>本次任務目前最佳</CardTitle>
          <CardDescription>任務尚未完成前，這裡顯示運算中暫時領先的參數包。</CardDescription>
        </div>
      </CardHeader>
      <div className="space-y-4">
        <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-4">
          <Metric label="最佳評分" value={(task.best_score ?? 0).toFixed(4)} />
          <Metric label="最大回撤" value={formatPercent(task.max_drawdown ?? 0)} danger />
          {Object.entries(task.window_score ?? {}).map(([key, value]) => <Metric key={key} label={key} value={value.toFixed(4)} />)}
        </div>
        <JsonPreview value={task.best_param_pack} />
      </div>
    </Card>
  );
}

function Metric({ label, value, danger = false }: { label: string; value: string; danger?: boolean }) {
  return (
    <div className="rounded-lg border border-white/[0.04] bg-white/[0.02] p-3">
      <div className="text-xs text-slate-500">{label}</div>
      <div className={cn("mt-1 font-mono text-lg", danger ? "text-[#fecaca]" : "text-slate-100")}>{value}</div>
    </div>
  );
}

function ParameterLandscape({ query, live = false }: { query: GeneObservationQuery; live?: boolean }) {
  const observationsQuery = useQuery({
    queryKey: ["gene-observations", query],
    queryFn: () => evolutionApi.listGeneObservations(query),
    refetchInterval: live ? 2_000 : false,
    enabled: Boolean(query.instrument_id && query.interval && query.execution_mode)
  });
  const schema = observationsQuery.data?.schema ?? [];
  const observations = observationsQuery.data?.observations ?? [];
  return (
    <Card className="md:col-span-2">
      <CardHeader>
        <div>
          <CardTitle>參數分佈地圖</CardTitle>
          <CardDescription>依目前搜尋條件顯示曾經誕生過的參數；任務運行時會持續更新。</CardDescription>
        </div>
        <div className="text-right text-xs text-slate-500">
          {observationsQuery.isFetching ? "更新中" : "已同步"}<br />
          {observations.length.toLocaleString("zh-TW")} 筆
        </div>
      </CardHeader>
      {observationsQuery.isLoading ? <div className="text-sm text-slate-500">載入參數分佈...</div> : null}
      {!observationsQuery.isLoading && observations.length === 0 ? (
        <div className="rounded-lg border border-white/[0.04] bg-white/[0.02] p-4 text-sm text-slate-500">目前搜尋條件還沒有參數誕生紀錄。開始搜尋後，這裡會逐代更新。</div>
      ) : null}
      {observations.length > 0 ? (
        <div className="grid gap-4 xl:grid-cols-2">
          <PcaScatterPlot schema={schema} observations={observations} />
          <ParallelCoordinatePlot schema={schema} observations={observations} />
        </div>
      ) : null}
    </Card>
  );
}

function PcaScatterPlot({ schema, observations }: { schema: GeneObservationAxis[]; observations: GeneObservation[] }) {
  const points = useMemo(() => pcaProjection(schema, observations), [schema, observations]);
  const [hovered, setHovered] = useState<(typeof points)[number] | null>(null);
  const width = 620;
  const height = 360;
  const padding = 36;
  const xs = points.map((item) => item.x);
  const ys = points.map((item) => item.y);
  const minX = Math.min(...xs, -1);
  const maxX = Math.max(...xs, 1);
  const minY = Math.min(...ys, -1);
  const maxY = Math.max(...ys, 1);
  const sx = (x: number) => padding + ((x - minX) / Math.max(0.000001, maxX - minX)) * (width - padding * 2);
  const sy = (y: number) => height - padding - ((y - minY) / Math.max(0.000001, maxY - minY)) * (height - padding * 2);
  return (
    <div className="rounded-lg border border-white/[0.04] bg-slate-950/40 p-3">
      <div className="mb-2 flex items-center justify-between">
        <div className="text-sm font-semibold text-slate-200">降維散點圖</div>
        <div className="text-xs text-slate-500">越靠近代表 15 維形狀越相似</div>
      </div>
      <div className="relative overflow-hidden rounded-lg border border-white/[0.04] bg-slate-950">
        <svg viewBox={`0 0 ${width} ${height}`} className="h-80 w-full" role="img" aria-label="參數降維散點圖">
          <line x1={padding} x2={width - padding} y1={height / 2} y2={height / 2} stroke="rgba(148,163,184,0.18)" />
          <line x1={width / 2} x2={width / 2} y1={padding} y2={height - padding} stroke="rgba(148,163,184,0.18)" />
          {points.map((point) => (
            <circle
              key={point.observation.id}
              cx={sx(point.x)}
              cy={sy(point.y)}
              r={point.observation.fatal ? 2.4 : 3.2}
              fill={scoreColor(point.observation.score_total, observations)}
              opacity={point.observation.fatal ? 0.22 : 0.72}
              onMouseEnter={() => setHovered(point)}
              onMouseLeave={() => setHovered(null)}
            />
          ))}
        </svg>
        {hovered ? <ObservationTooltip observation={hovered.observation} className="left-3 top-3" /> : null}
      </div>
    </div>
  );
}

function ParallelCoordinatePlot({ schema, observations }: { schema: GeneObservationAxis[]; observations: GeneObservation[] }) {
  const [hovered, setHovered] = useState<GeneObservation | null>(null);
  const width = 720;
  const height = 360;
  const paddingX = 34;
  const paddingY = 26;
  const x = (index: number) => paddingX + (index / Math.max(1, schema.length - 1)) * (width - paddingX * 2);
  const y = (axis: GeneObservationAxis, value: number) => {
    const normalized = (value - axis.min) / Math.max(0.000001, axis.max - axis.min);
    return height - paddingY - Math.min(1, Math.max(0, normalized)) * (height - paddingY * 2);
  };
  const visible = observations.slice(-3000);
  return (
    <div className="rounded-lg border border-white/[0.04] bg-slate-950/40 p-3">
      <div className="mb-2 flex items-center justify-between">
        <div className="text-sm font-semibold text-slate-200">平行座標圖</div>
        <div className="text-xs text-slate-500">每一條線是一組參數</div>
      </div>
      <div className="relative overflow-hidden rounded-lg border border-white/[0.04] bg-slate-950">
        <svg viewBox={`0 0 ${width} ${height}`} className="h-80 w-full" role="img" aria-label="參數平行座標圖">
          {schema.map((axis, index) => (
            <g key={axis.key}>
              <line x1={x(index)} x2={x(index)} y1={paddingY} y2={height - paddingY} stroke="rgba(148,163,184,0.24)" />
              <text x={x(index)} y={height - 6} textAnchor="middle" fontSize="10" fill="rgb(148,163,184)">
                {axis.label.slice(0, 5)}
              </text>
            </g>
          ))}
          {visible.map((item) => {
            const d = schema.map((axis, index) => `${index === 0 ? "M" : "L"} ${x(index).toFixed(2)} ${y(axis, Number(item.param_values[axis.key] ?? axis.min)).toFixed(2)}`).join(" ");
            return (
              <path
                key={item.id}
                d={d}
                fill="none"
                stroke={scoreColor(item.score_total, observations)}
                strokeWidth={hovered?.id === item.id ? 2.2 : 0.85}
                opacity={hovered?.id === item.id ? 0.95 : item.fatal ? 0.08 : 0.22}
                onMouseEnter={() => setHovered(item)}
                onMouseLeave={() => setHovered(null)}
              />
            );
          })}
        </svg>
        {hovered ? <ObservationTooltip observation={hovered} className="left-3 top-3" /> : null}
      </div>
      {observations.length > visible.length ? <div className="mt-2 text-xs text-slate-500">為了維持流暢，平行座標顯示最近 {visible.length.toLocaleString("zh-TW")} 筆。</div> : null}
    </div>
  );
}

function ObservationTooltip({ observation, className }: { observation: GeneObservation; className?: string }) {
  return (
    <div className={cn("pointer-events-none absolute z-10 rounded-lg border border-white/10 bg-slate-900/95 p-3 text-xs text-slate-200 shadow-xl", className)}>
      <div className="font-semibold">#{observation.id} · 任務 #{observation.task_id}</div>
      <div className="mt-1 text-slate-400">世代 {observation.generation} / 個體 {observation.individual}</div>
      <div className="mt-1">評分 {observation.score_total.toFixed(4)}</div>
      <div>最大回撤 {formatPercent(observation.max_drawdown)}</div>
      <div className={cn("mt-1", observation.fatal ? "text-[#fecaca]" : "text-[#99f6e4]")}>{observation.fatal ? "淘汰" : "有效"}</div>
    </div>
  );
}

function scoreColor(score: number, observations: GeneObservation[]) {
  const values = observations.filter((item) => Number.isFinite(item.score_total)).map((item) => item.score_total);
  const min = Math.min(...values, score);
  const max = Math.max(...values, score);
  const ratio = (score - min) / Math.max(0.000001, max - min);
  if (ratio > 0.75) return "rgb(45,212,191)";
  if (ratio > 0.45) return "rgb(250,204,21)";
  return "rgb(248,113,113)";
}

function pcaProjection(schema: GeneObservationAxis[], observations: GeneObservation[]) {
  const vectors = observations.map((item) => schema.map((axis) => {
    const value = Number(item.param_values[axis.key] ?? axis.min);
    return (value - axis.min) / Math.max(0.000001, axis.max - axis.min);
  }));
  if (vectors.length === 0 || schema.length === 0) return [];
  const dims = schema.length;
  const means = Array.from({ length: dims }, (_, dim) => vectors.reduce((sum, vector) => sum + vector[dim], 0) / vectors.length);
  const centered = vectors.map((vector) => vector.map((value, dim) => value - means[dim]));
  const covariance = Array.from({ length: dims }, (_, row) =>
    Array.from({ length: dims }, (_, col) => centered.reduce((sum, vector) => sum + vector[row] * vector[col], 0) / Math.max(1, centered.length - 1))
  );
  const pc1 = powerIteration(covariance);
  const lambda1 = dot(pc1, multiply(covariance, pc1));
  const deflated = covariance.map((row, i) => row.map((value, j) => value - lambda1 * pc1[i] * pc1[j]));
  const pc2 = powerIteration(deflated);
  return observations.map((observation, index) => ({
    observation,
    x: dot(centered[index], pc1),
    y: dot(centered[index], pc2)
  }));
}

function powerIteration(matrix: number[][]) {
  const dims = matrix.length;
  let vector = Array.from({ length: dims }, (_, index) => (index === 0 ? 1 : 1 / Math.max(1, dims)));
  for (let step = 0; step < 32; step++) {
    const next = multiply(matrix, vector);
    const norm = Math.sqrt(dot(next, next)) || 1;
    vector = next.map((value) => value / norm);
  }
  return vector;
}

function multiply(matrix: number[][], vector: number[]) {
  return matrix.map((row) => row.reduce((sum, value, index) => sum + value * vector[index], 0));
}

function dot(a: number[], b: number[]) {
  return a.reduce((sum, value, index) => sum + value * b[index], 0);
}

function EvolutionPanel({ instrumentNames }: { instrumentNames: Record<string, string> }) {
  const queryClient = useQueryClient();
  const instrumentsQuery = useQuery({ queryKey: ["market-data-instruments"], queryFn: () => marketDataApi.instruments() });
  const instruments = instrumentsQuery.data?.instruments ?? [];
  const [expanded, setExpanded] = useState(false);
  const [instrumentId, setInstrumentId] = useState("BTCUSDT");
  const selected = instruments.find((item) => item.id === instrumentId);
  const [interval, setInterval] = useState("1d");
  const [executionMode, setExecutionMode] = useState("close_next_open");
  const [startDate, setStartDate] = useState(dateInputValue(new Date(Date.now() - 365 * 24 * 60 * 60 * 1000)));
  const [endDate, setEndDate] = useState(dateInputValue(new Date()));
  const [population, setPopulation] = useState(300);
  const [generations, setGenerations] = useState(25);
  const [monthlyDCA, setMonthlyDCA] = useState(0);
  const [evolveRebalanceThreshold, setEvolveRebalanceThreshold] = useState(true);
  const [evolveForceFullThreshold, setEvolveForceFullThreshold] = useState(true);
  const [evolveForceEmptyThreshold, setEvolveForceEmptyThreshold] = useState(true);
  const [evolveGamma, setEvolveGamma] = useState(false);
  const [enableWMean, setEnableWMean] = useState(true);
  const [enableWMomentum, setEnableWMomentum] = useState(true);
  const [enableWBreakout, setEnableWBreakout] = useState(true);
  const [positionStructure, setPositionStructure] = useState<"dual_layer" | "floating_only">("floating_only");
  const [tradePenalty, setTradePenalty] = useState(0);
  const [feeRate, setFeeRate] = useState(0);
  const [spreadRate, setSpreadRate] = useState(0);
  const [spawnMode, setSpawnMode] = useState<"inherit" | "random_once" | "manual">("inherit");
  const [traceMode, setTraceMode] = useState<TraceMode>("off");
  const [computeMonitorEnabled, setComputeMonitorEnabled] = useState(false);
  const [showLandscape, setShowLandscape] = useState(false);
  const [referenceIndicators, setReferenceIndicators] = useState<ReferenceIndicatorDraft[]>([]);
  const [continuousMode, setContinuousMode] = useState<"" | "standardized_best" | "random">("");
  const [continuousIterations, setContinuousIterations] = useState(3);
  const [continuousUnlimited, setContinuousUnlimited] = useState(false);
  const [standardStartDate, setStandardStartDate] = useState(startDate);
  const [standardEndDate, setStandardEndDate] = useState(endDate);
  const datasetQuery = useQuery({
    queryKey: ["market-data", selected?.id ?? instrumentId],
    queryFn: () => marketDataApi.status(selected?.id ?? instrumentId),
    enabled: Boolean(selected?.id ?? instrumentId)
  });
  const selectedDataset = useMemo(
    () => datasetQuery.data?.datasets.find((item) => item.interval === interval),
    [datasetQuery.data, interval]
  );

  useEffect(() => {
    if (!selectedDataset?.first_open_ms || !selectedDataset?.last_open_ms) return;
    const nextStart = msToDateInput(selectedDataset.first_open_ms);
    const nextEnd = msToDateInput(selectedDataset.last_open_ms);
    if (!nextStart || !nextEnd) return;
    setStartDate(nextStart);
    setEndDate(nextEnd);
    setStandardStartDate(nextStart);
    setStandardEndDate(nextEnd);
  }, [instrumentId, interval, selectedDataset?.first_open_ms, selectedDataset?.last_open_ms]);
  const overviewQuery = useQuery({ queryKey: ["evolution-tasks"], queryFn: () => evolutionApi.listTasks(), refetchInterval: 1_000 });
  const running = overviewQuery.data?.current_task ?? overviewQuery.data?.tasks.find((task) => task.status === "running");
  const animatedComputedUnits = useAnimatedNumber(running?.computed_units ?? 0);
  const referenceIndicatorOptions = useMemo(() => instruments.filter((item) => item.id !== instrumentId), [instruments, instrumentId]);
  const referenceIndicatorSearchBlocked = referenceIndicators.length > 0;
  const taskInput = useMemo<CreateTaskInput>(() => ({
    strategy_id: "sigmoid-dca-btc",
    pair: selected?.symbol ?? instrumentId,
    instrument_id: instrumentId,
    data_source: selected?.data_source,
    interval,
    execution_mode: executionMode,
    train_start_ms: dayStartMs(startDate),
    train_end_ms: dayEndMs(endDate),
    monthly_dca: monthlyDCA,
    evolve_rebalance_threshold: evolveRebalanceThreshold,
    evolve_force_full_threshold: evolveForceFullThreshold,
    evolve_force_empty_threshold: evolveForceEmptyThreshold,
    evolve_gamma: evolveGamma,
    enable_w_mean: enableWMean,
    enable_w_momentum: enableWMomentum,
    enable_w_breakout: enableWBreakout,
    position_structure: positionStructure,
    trade_penalty: tradePenalty,
    fee_rate: feeRate,
    spread_rate: spreadRate,
    pop_size: population,
    max_generations: generations,
    spawn_mode: spawnMode,
    trace_mode: traceMode,
    compute_monitor_enabled: computeMonitorEnabled,
    continuous_mode: continuousMode,
    continuous_iterations: continuousIterations,
    continuous_unlimited: continuousUnlimited,
    standard_start_ms: continuousMode === "standardized_best" ? dayStartMs(standardStartDate) : undefined,
    standard_end_ms: continuousMode === "standardized_best" ? dayEndMs(standardEndDate) : undefined
  }), [computeMonitorEnabled, continuousIterations, continuousMode, continuousUnlimited, enableWBreakout, enableWMean, enableWMomentum, endDate, evolveForceEmptyThreshold, evolveForceFullThreshold, evolveGamma, evolveRebalanceThreshold, executionMode, feeRate, generations, instrumentId, interval, monthlyDCA, population, positionStructure, selected?.data_source, selected?.symbol, spawnMode, spreadRate, standardEndDate, standardStartDate, startDate, traceMode, tradePenalty]);
  const canEstimateCompute = expanded && computeMonitorEnabled && Boolean(selected) && (enableWMean || enableWMomentum || enableWBreakout);
  const computeEstimateQuery = useQuery({
    queryKey: ["evolution-compute-estimate", taskInput],
    queryFn: () => evolutionApi.estimateCompute(taskInput),
    enabled: canEstimateCompute,
    staleTime: 5_000
  });
  const createMutation = useMutation({
    mutationFn: () => evolutionApi.createTask(taskInput),
    onSuccess: () => {
      setExpanded(false);
      queryClient.invalidateQueries({ queryKey: ["evolution-tasks"] });
    }
  });
  const cancelMutation = useMutation({
    mutationFn: (taskId: number) => evolutionApi.cancel(taskId),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["evolution-tasks"] });
      queryClient.invalidateQueries({ queryKey: ["genomes"] });
      queryClient.invalidateQueries({ queryKey: ["gene-observations"] });
      window.setTimeout(() => {
        queryClient.invalidateQueries({ queryKey: ["genomes"] });
        queryClient.invalidateQueries({ queryKey: ["evolution-tasks"] });
      }, 2500);
    }
  });
  const landscapeQuery = useMemo<GeneObservationQuery>(() => ({
    strategy_id: "sigmoid-dca-btc",
    instrument_id: instrumentId,
    data_source: selected?.data_source,
    interval,
    execution_mode: executionMode,
    train_start_ms: startDate ? dayStartMs(startDate) : undefined,
    train_end_ms: endDate ? dayEndMs(endDate) : undefined,
    spawn_mode: spawnMode,
    limit: 12000
  }), [endDate, executionMode, instrumentId, interval, selected?.data_source, spawnMode, startDate]);

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!enableWMean && !enableWMomentum && !enableWBreakout) return;
    if (referenceIndicatorSearchBlocked) return;
    createMutation.mutate();
  }

  function changeInstrument(nextId: string) {
    const next = instruments.find((item) => item.id === nextId);
    setInstrumentId(nextId);
    setInterval(next?.supported_intervals[0] ?? "1d");
    setReferenceIndicators((current) => current.filter((item) => item.instrument_id !== nextId));
  }

  function addReferenceIndicator() {
    const next = referenceIndicatorOptions.find((item) => !referenceIndicators.some((indicator) => indicator.instrument_id === item.id));
    if (!next) return;
    setReferenceIndicators((current) => [...current, { instrument_id: next.id, interval: next.supported_intervals[0] ?? "1d" }]);
  }

  function updateReferenceIndicator(index: number, patch: Partial<ReferenceIndicatorDraft>) {
    setReferenceIndicators((current) => current.map((item, itemIndex) => (itemIndex === index ? { ...item, ...patch } : item)));
  }

  function removeReferenceIndicator(index: number) {
    setReferenceIndicators((current) => current.filter((_, itemIndex) => itemIndex !== index));
  }

  if (running) {
    const current = running.current_generation ?? Math.round((running.progress || 0) * (running.max_generations ?? 25));
    const max = running.max_generations ?? 25;
    const progressPct = Math.min(100, Math.round((running.progress || 0) * 100));
    const evaluated = running.evaluated_individuals ?? current * (running.pop_size ?? 0);
    const planned = running.planned_evaluations ?? (running.pop_size ?? 0) * max;
    const plannedComputeUnits = running.planned_compute_units ?? 0;
    const computedUnits = running.computed_units ?? 0;
    const computePct = plannedComputeUnits > 0 ? Math.min(100, Math.max(0, (computedUnits / plannedComputeUnits) * 100)) : 0;
    const runningLandscapeQuery: GeneObservationQuery = {
      strategy_id: running.strategy_id ?? "sigmoid-dca-btc",
      instrument_id: running.instrument_id,
      data_source: running.data_source,
      interval: running.interval,
      execution_mode: running.execution_mode,
      train_start_ms: running.train_start_ms,
      train_end_ms: running.train_end_ms,
      spawn_mode: running.spawn_mode,
      limit: 12000
    };
    return (
      <div className="space-y-4">
        <Card>
          <CardHeader>
            <div>
              <CardTitle>參數搜尋運行中</CardTitle>
              <CardDescription>{labelForInstrument(running.instrument_id, instrumentNames)} · {intervalLabels[running.interval ?? "1d"] ?? running.interval}</CardDescription>
            </div>
            <div className="flex items-center gap-2">
              <StatusBadge status="running" />
              <Button icon={Square} variant="secondary" loading={cancelMutation.isPending} onClick={() => cancelMutation.mutate(running.id)}>中止</Button>
            </div>
          </CardHeader>
          <div className="space-y-4">
            <div className="rounded-lg border border-[#2dd4bf]/20 bg-[#2dd4bf]/[0.06] p-4">
              <div className="mb-3 flex items-center gap-2 text-sm font-semibold text-[#99f6e4]"><Activity className="h-4 w-4" />運算監控</div>
              <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-4">
                <Metric label="任務 ID" value={`#${running.id}`} />
                {running.continuous_mode ? <Metric label="連續輪次" value={running.continuous_unlimited ? `${running.current_iteration ?? 0} / 無上限` : `${running.current_iteration ?? 0} / ${running.continuous_iterations ?? 0}`} /> : null}
                <Metric label="資料範圍" value={`${msToDateInput(running.train_start_ms) || "-"} ~ ${msToDateInput(running.train_end_ms) || "-"}`} />
                <Metric label="進度" value={`${progressPct}%`} />
                <Metric label="已評估" value={planned ? `${evaluated.toLocaleString("zh-TW")} / ${planned.toLocaleString("zh-TW")}` : evaluated.toLocaleString("zh-TW")} />
                {running.compute_monitor_enabled ? <Metric label="計算量" value={plannedComputeUnits ? `${formatUnits(animatedComputedUnits)} / ${formatUnits(plannedComputeUnits)}` : formatUnits(animatedComputedUnits)} /> : null}
                {running.compute_monitor_enabled ? <Metric label="計算速度" value={formatUnitRate(running.compute_units_per_sec)} /> : null}
                {running.compute_monitor_enabled ? <Metric label="預估剩餘" value={formatDurationSeconds(running.compute_remaining_sec)} /> : null}
                <Metric label="初始本金" value={formatMoney(running.initial_capital ?? SEARCH_INITIAL_CAPITAL)} />
                <Metric label="每月投入" value={formatMoney(running.monthly_dca ?? 0)} />
                <Metric label="調倉門檻" value={running.evolve_rebalance_threshold ? "參與演化" : "固定為 0"} />
                <Metric label="倉位回饋 Gamma" value={running.evolve_gamma ? "參與演化" : "固定為 0"} />
                <Metric label="手續費率" value={running.fee_rate !== undefined ? formatPercent(running.fee_rate) : "0.00%"} />
                <Metric label="價差 / 滑價率" value={running.spread_rate !== undefined ? formatPercent(running.spread_rate) : "0.00%"} />
                <Metric label="最佳評分" value={(running.best_score ?? 0).toFixed(4)} />
                {running.standard_champion_gene_id ? <Metric label="標準化冠軍" value={`#${running.standard_champion_gene_id} / ${(running.standard_champion_score ?? 0).toFixed(4)}`} /> : null}
                <Metric label="最大回撤" value={running.max_drawdown !== undefined ? formatPercent(running.max_drawdown) : "等待回報"} danger />
                <Metric label="變異機率" value={running.mutation_probability !== undefined ? formatPercent(running.mutation_probability) : "等待回報"} />
                <Metric label="最後更新" value={monitorValue(running.monitor_updated_at ? shortDateTime(running.monitor_updated_at) : undefined).toString()} />
              </div>
            </div>
            <div>
              <div className="mb-2 flex justify-between text-sm">
                <span className="text-slate-400">目前世代</span>
                <span className="font-mono text-slate-200">{current} / {max}</span>
              </div>
              <div className="h-2 overflow-hidden rounded-full bg-slate-800"><div className="h-full rounded-full bg-[#2dd4bf]" style={{ width: `${progressPct}%` }} /></div>
            </div>
            {running.compute_monitor_enabled ? (
              <div className="rounded-lg border border-white/[0.04] bg-slate-950/50 p-4">
                <div className="mb-2 flex justify-between text-sm">
                  <span className="text-slate-400">逐筆計算量</span>
                  <span className="font-mono text-slate-200">
                    {plannedComputeUnits ? `${formatUnits(animatedComputedUnits)} / ${formatUnits(plannedComputeUnits)}` : formatUnits(animatedComputedUnits)}
                  </span>
                </div>
                <div className="h-2 overflow-hidden rounded-full bg-slate-800"><div className="h-full rounded-full bg-[#f59e0b]" style={{ width: `${computePct}%` }} /></div>
                <div className="mt-3 grid gap-2 text-xs text-slate-500 md:grid-cols-3">
                  <div>每組參數約 {formatUnits(running.units_per_individual)} 筆</div>
                  <div>速度 {formatUnitRate(running.compute_units_per_sec)}</div>
                  <div>剩餘 {formatDurationSeconds(running.compute_remaining_sec)}</div>
                </div>
              </div>
            ) : null}
          </div>
        </Card>
        <Card className="p-4">
          <div className="flex flex-wrap items-center justify-between gap-3">
            <div>
              <div className="text-sm font-semibold text-slate-200">參數分佈地圖</div>
              <div className="mt-1 text-xs text-slate-500">預設關閉；打開後才查詢紀錄並計算圖表。</div>
            </div>
            <Button variant="secondary" onClick={() => setShowLandscape((value) => !value)}>
              {showLandscape ? "關閉地圖" : "顯示地圖"}
            </Button>
          </div>
        </Card>
        {showLandscape ? <ParameterLandscape query={runningLandscapeQuery} live /> : null}
        <CurrentBestCard task={running} />
        <TraceConsole task={running} />
      </div>
    );
  }

  return (
    <Card>
      <CardHeader>
        <div>
          <CardTitle>參數搜尋</CardTitle>
          <CardDescription>選擇研究標的、資料範圍與執行假設後建立運算任務。</CardDescription>
        </div>
        <Button icon={FlaskConical} onClick={() => setExpanded((value) => !value)}>建立任務</Button>
      </CardHeader>
      {expanded ? (
        <form className="grid gap-4 md:grid-cols-2" onSubmit={submit}>
          <Select label="研究標的" value={instrumentId} onChange={changeInstrument} options={instruments.map((item) => [item.id, item.display_name])} />
          <Select label="資料週期" value={interval} onChange={setInterval} options={(selected?.supported_intervals ?? ["1d"]).map((item) => [item, intervalLabels[item] ?? item])} />
          <label><span className="mb-2 block text-sm text-slate-300">開始日期</span><input className="h-11 w-full rounded-lg border border-slate-700 bg-slate-900/80 px-3 text-sm text-slate-100 outline-none focus:border-[#2dd4bf]" type="date" value={startDate} onChange={(event) => setStartDate(event.target.value)} /></label>
          <label><span className="mb-2 block text-sm text-slate-300">結束日期</span><input className="h-11 w-full rounded-lg border border-slate-700 bg-slate-900/80 px-3 text-sm text-slate-100 outline-none focus:border-[#2dd4bf]" type="date" value={endDate} onChange={(event) => setEndDate(event.target.value)} /></label>
          <Select label="執行假設" value={executionMode} onChange={setExecutionMode} options={executionModes.map(([value, label]) => [value, label])} />
          <Select label="起始方式" value={spawnMode} onChange={(value) => setSpawnMode(value as typeof spawnMode)} options={[["inherit", "繼承同標的冠軍"], ["random_once", "隨機探索"], ["manual", "手動設定"]]} />
          <div className="rounded-lg border border-white/[0.04] bg-white/[0.02] p-3 md:col-span-2">
            <div className="flex flex-wrap items-center justify-between gap-3">
              <div>
                <div className="text-sm font-semibold text-slate-300">參考指標</div>
                <div className="mt-1 text-xs text-slate-500">可先建立研究資料集；正式指標演算法確認前，選了參考指標會禁止開始搜尋。</div>
              </div>
              <Button type="button" variant="secondary" icon={Plus} onClick={addReferenceIndicator} disabled={!referenceIndicatorOptions.length}>
                新增參考指標
              </Button>
            </div>
            <div className="mt-3 grid gap-3">
              {referenceIndicators.map((indicator, index) => {
                const indicatorInstrument = instruments.find((item) => item.id === indicator.instrument_id);
                const options = referenceIndicatorOptions.concat(indicatorInstrument && !referenceIndicatorOptions.some((item) => item.id === indicatorInstrument.id) ? [indicatorInstrument] : []);
                return (
                  <div key={`${indicator.instrument_id}-${index}`} className="grid gap-3 rounded-lg border border-white/[0.05] p-3 md:grid-cols-[1fr_220px_auto]">
                    <label>
                      <span className="mb-2 block text-xs text-slate-500">序列</span>
                      <select
                        className="h-10 w-full rounded-lg border border-slate-700 bg-slate-900/80 px-3 text-sm text-slate-100 outline-none focus:border-[#2dd4bf]"
                        value={indicator.instrument_id}
                        onChange={(event) => {
                          const next = instruments.find((item) => item.id === event.target.value);
                          updateReferenceIndicator(index, { instrument_id: event.target.value, interval: next?.supported_intervals[0] ?? "1d" });
                        }}
                      >
                        {options.map((item) => <option key={item.id} value={item.id}>{item.display_name}</option>)}
                      </select>
                    </label>
                    <label>
                      <span className="mb-2 block text-xs text-slate-500">週期</span>
                      <select className="h-10 w-full rounded-lg border border-slate-700 bg-slate-900/80 px-3 text-sm text-slate-100 outline-none focus:border-[#2dd4bf]" value={indicator.interval} onChange={(event) => updateReferenceIndicator(index, { interval: event.target.value })}>
                        {(indicatorInstrument?.supported_intervals ?? ["1d"]).map((item) => <option key={item} value={item}>{intervalLabels[item] ?? item}</option>)}
                      </select>
                    </label>
                    <div className="flex items-end">
                      <Button type="button" variant="danger" icon={Trash2} onClick={() => removeReferenceIndicator(index)}>移除</Button>
                    </div>
                  </div>
                );
              })}
              {!referenceIndicators.length ? <div className="rounded-lg border border-white/[0.04] px-3 py-3 text-sm text-slate-500">未選參考指標，會使用既有單商品搜尋流程。</div> : null}
            </div>
            {referenceIndicatorSearchBlocked ? (
              <div className="mt-3 flex items-start gap-2 rounded-lg border border-[#f59e0b]/25 bg-[#f59e0b]/10 px-3 py-2 text-sm leading-6 text-[#fde68a]">
                <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" />
                <span>已選參考指標，但目前尚未啟用任何已確認的指標演算法；請先到「研究資料集」預覽資料，等指標演算法確定後才能開始搜尋。</span>
              </div>
            ) : null}
          </div>
          <NumberInput label="族群數" min={10} max={500} value={population} onChange={setPopulation} />
          <NumberInput label="世代數" min={5} max={50} value={generations} onChange={setGenerations} />
          <ReadOnlyMetric label="初始本金" value={formatMoney(SEARCH_INITIAL_CAPITAL)} />
          <NumberInput label="每月投入" min={0} max={1000000000} value={monthlyDCA} onChange={setMonthlyDCA} />
          <NumberInput label="手續費率" min={0} max={0.2} step={0.0001} value={feeRate} onChange={setFeeRate} />
          <NumberInput label="價差 / 滑價率" min={0} max={0.2} step={0.0001} value={spreadRate} onChange={setSpreadRate} />
          <Select label="倉位結構" value={positionStructure} onChange={(value) => setPositionStructure(value as "dual_layer" | "floating_only")} options={[["floating_only", "純浮動模型"], ["dual_layer", "雙層模型"]]} />
          <div className="rounded-lg border border-white/[0.04] bg-white/[0.02] p-3 md:col-span-2">
            <div className="mb-2 text-sm font-semibold text-slate-300">門檻演化</div>
            <div className="grid gap-2 md:grid-cols-3">
              <label className="flex items-center gap-3 text-sm text-slate-300">
                <input type="checkbox" checked={evolveRebalanceThreshold} onChange={(event) => setEvolveRebalanceThreshold(event.target.checked)} />
                演化調倉門檻
              </label>
              <label className="flex items-center gap-3 text-sm text-slate-300">
                <input type="checkbox" checked={evolveForceFullThreshold} onChange={(event) => setEvolveForceFullThreshold(event.target.checked)} />
                演化強制滿倉門檻
              </label>
              <label className="flex items-center gap-3 text-sm text-slate-300">
                <input type="checkbox" checked={evolveForceEmptyThreshold} onChange={(event) => setEvolveForceEmptyThreshold(event.target.checked)} />
                演化強制空倉門檻
              </label>
            </div>
          </div>
          <div className="rounded-lg border border-white/[0.04] bg-white/[0.02] p-3 md:col-span-2">
            <div className="mb-2 text-sm font-semibold text-slate-300">倉位回饋</div>
            <label className="flex items-start gap-3 text-sm text-slate-300">
              <input className="mt-1" type="checkbox" checked={evolveGamma} onChange={(event) => setEvolveGamma(event.target.checked)} />
              <span>
                <span className="block">演化倉位回饋 Gamma</span>
                <span className="mt-1 block text-xs text-slate-500">預設關閉；關閉時 Gamma 固定為 0，目標權重只由市場訊號產生。</span>
              </span>
            </label>
          </div>
          <Select label="連續搜尋" value={continuousMode} onChange={(value) => setContinuousMode(value as typeof continuousMode)} options={[["", "單次搜尋"], ["standardized_best", "接續標準化最佳"], ["random", "連續隨機搜尋"]]} />
          {!continuousUnlimited ? <NumberInput label="連續輪數" min={1} max={100} value={continuousIterations} onChange={setContinuousIterations} /> : <div />}
          <label className="flex items-center gap-3 rounded-lg border border-white/[0.04] bg-white/[0.02] px-3 py-3 text-sm text-slate-300">
            <input type="checkbox" checked={continuousUnlimited} onChange={(event) => setContinuousUnlimited(event.target.checked)} />
            無上限，直到手動中止
          </label>
          {continuousMode === "standardized_best" ? (
            <>
              <label><span className="mb-2 block text-sm text-slate-300">標準化開始日</span><input className="h-11 w-full rounded-lg border border-slate-700 bg-slate-900/80 px-3 text-sm text-slate-100 outline-none focus:border-[#2dd4bf]" type="date" value={standardStartDate} onChange={(event) => setStandardStartDate(event.target.value)} /></label>
              <label><span className="mb-2 block text-sm text-slate-300">標準化結束日</span><input className="h-11 w-full rounded-lg border border-slate-700 bg-slate-900/80 px-3 text-sm text-slate-100 outline-none focus:border-[#2dd4bf]" type="date" value={standardEndDate} onChange={(event) => setStandardEndDate(event.target.value)} /></label>
              <div className="md:col-span-2 text-xs text-slate-500">每輪結束後會用這段區間比較該標的參數庫，下一輪接續標準化綜合評分最高的參數。</div>
            </>
          ) : null}
          <div className="md:col-span-2">
            <div className="mb-2 text-sm text-slate-300">原始追蹤模式</div>
            <div className="grid gap-2 md:grid-cols-4">
              {traceModeOptions.map(([value, label, description]) => (
                <button key={value} type="button" className={cn("rounded-lg border px-3 py-2 text-left text-sm transition", traceMode === value ? "border-[#2dd4bf]/40 bg-[#2dd4bf]/10 text-[#99f6e4]" : "border-white/[0.04] text-slate-400")} onClick={() => setTraceMode(value)}>
                  <span className="block font-semibold">{label}</span><span className="mt-1 block text-xs text-slate-500">{description}</span>
                </button>
              ))}
            </div>
          </div>
          <div className="rounded-lg border border-white/[0.04] bg-white/[0.02] p-3 md:col-span-2">
            <label className="flex items-start gap-3 text-sm text-slate-300">
              <input className="mt-1" type="checkbox" checked={computeMonitorEnabled} onChange={(event) => setComputeMonitorEnabled(event.target.checked)} />
              <span>
                <span className="block font-semibold text-slate-200">計算量監控</span>
                <span className="mt-1 block text-xs leading-5 text-slate-500">開啟後會預估逐筆計算量，任務執行時以輕量 counter 統計已計算量；關閉時不掛載 counter。</span>
              </span>
            </label>
            {computeMonitorEnabled ? (
              <div className="mt-3 rounded-lg border border-[#f59e0b]/20 bg-[#f59e0b]/10 p-3 text-sm text-[#fde68a]">
                {computeEstimateQuery.isLoading || computeEstimateQuery.isFetching ? "估算中..." : computeEstimateQuery.data ? (
                  <div className="grid gap-2 md:grid-cols-3">
                    <div>總計算量約 {formatUnits(computeEstimateQuery.data.planned_units)} 筆</div>
                    <div>每組參數約 {formatUnits(computeEstimateQuery.data.units_per_individual)} 筆</div>
                    <div>{continuousUnlimited ? "無上限連續搜尋不估最終總量" : "依目前搜尋設定估算"}</div>
                  </div>
                ) : computeEstimateQuery.error ? (
                  <div className="text-[#fecaca]">{String(computeEstimateQuery.error.message)}</div>
                ) : (
                  <div>等待可用資料集與搜尋設定。</div>
                )}
              </div>
            ) : null}
          </div>
          {executionMode === "preclose_10m" ? <div className="md:col-span-2 text-xs text-[#fde68a]">這個模式需要收盤前快照資料；缺資料時任務可能無法產生有效結果。</div> : null}
          <div className="rounded-lg border border-white/[0.04] bg-white/[0.02] p-3 md:col-span-2">
            <div className="mb-2 text-sm font-semibold text-slate-300">啟用市場訊號</div>
            <div className="grid gap-2 md:grid-cols-3">
              <label className="flex items-center gap-3 text-sm text-slate-300"><input type="checkbox" checked={enableWMean} onChange={(event) => setEnableWMean(event.target.checked)} />均值回歸</label>
              <label className="flex items-center gap-3 text-sm text-slate-300"><input type="checkbox" checked={enableWMomentum} onChange={(event) => setEnableWMomentum(event.target.checked)} />動能</label>
              <label className="flex items-center gap-3 text-sm text-slate-300"><input type="checkbox" checked={enableWBreakout} onChange={(event) => setEnableWBreakout(event.target.checked)} />突破</label>
            </div>
            {!enableWMean && !enableWMomentum && !enableWBreakout ? <div className="mt-2 text-xs text-[#fecaca]">至少要啟用一個市場訊號。</div> : null}
          </div>
          <NumberInput label="每次交易懲罰" min={0} max={1} step={0.0001} value={tradePenalty} onChange={setTradePenalty} />
          <div className="rounded-lg border border-white/[0.04] bg-white/[0.02] p-3 md:col-span-2">
            <div className="flex flex-wrap items-center justify-between gap-3">
              <div>
                <div className="text-sm font-semibold text-slate-300">參數分佈地圖</div>
                <div className="mt-1 text-xs text-slate-500">預設關閉；打開後才查詢紀錄並計算圖表。</div>
              </div>
              <Button type="button" variant="secondary" onClick={() => setShowLandscape((value) => !value)}>
                {showLandscape ? "關閉地圖" : "顯示地圖"}
              </Button>
            </div>
          </div>
          {showLandscape ? <ParameterLandscape query={landscapeQuery} /> : null}
          <div className="md:col-span-2">
            <Button type="submit" loading={createMutation.isPending} disabled={referenceIndicatorSearchBlocked || (!enableWMean && !enableWMomentum && !enableWBreakout)}>開始搜尋</Button>
            {createMutation.error ? <div className="mt-2 text-sm text-[#fecaca]">{String(createMutation.error.message)}</div> : null}
          </div>
        </form>
      ) : null}
    </Card>
  );
}

function Select({ label, value, onChange, options }: { label: string; value: string; onChange: (value: string) => void; options: string[][] }) {
  return (
    <label>
      <span className="mb-2 block text-sm text-slate-300">{label}</span>
      <select className="h-11 w-full rounded-lg border border-slate-700 bg-slate-900/80 px-3 text-sm text-slate-100 outline-none focus:border-[#2dd4bf]" value={value} onChange={(event) => onChange(event.target.value)}>
        {options.map(([optionValue, optionLabel]) => <option key={optionValue} value={optionValue}>{optionLabel}</option>)}
      </select>
    </label>
  );
}

function NumberInput({ label, value, min, max, step = 1, onChange }: { label: string; value: number; min: number; max: number; step?: number; onChange: (value: number) => void }) {
  return (
    <label>
      <span className="mb-2 block text-sm text-slate-300">{label}</span>
      <input className="h-11 w-full rounded-lg border border-slate-700 bg-slate-900/80 px-3 font-mono text-sm text-slate-100 outline-none focus:border-[#2dd4bf]" type="number" min={min} max={max} step={step} value={value} onChange={(event) => onChange(Number(event.target.value))} />
    </label>
  );
}

function ReadOnlyMetric({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <span className="mb-2 block text-sm text-slate-300">{label}</span>
      <div className="flex h-11 items-center rounded-lg border border-white/[0.04] bg-white/[0.03] px-3 font-mono text-sm text-slate-200">{value}</div>
    </div>
  );
}

function TaskQueueView({ instrumentNames }: { instrumentNames: Record<string, string> }) {
  const { data: overview, isLoading } = useQuery({ queryKey: ["evolution-tasks"], queryFn: () => evolutionApi.listTasks(), refetchInterval: 5_000 });
  const tasks = overview?.tasks ?? [];
  return (
    <Card>
      <CardHeader><div><CardTitle>任務紀錄</CardTitle><CardDescription>已建立的參數搜尋任務與狀態。</CardDescription></div></CardHeader>
      <div className="space-y-3">
        {isLoading ? <div className="text-sm text-slate-500">載入中...</div> : null}
        {!isLoading && tasks.length === 0 ? <div className="text-sm text-slate-500">尚無任務。</div> : null}
        {tasks.map((task) => (
          <div key={task.id} className="rounded-lg border border-white/[0.04] bg-white/[0.02] p-3">
            <div className="flex flex-wrap items-center justify-between gap-3">
              <div>
                <div className="font-mono text-sm text-slate-200">#{task.id} · {labelForInstrument(task.instrument_id, instrumentNames)}</div>
                <div className="mt-1 text-xs text-slate-500">{relativeTime(task.created_at)} · {intervalLabels[task.interval ?? "1d"] ?? task.interval}</div>
              </div>
              <StatusBadge status={task.status} />
              <div className="text-right font-mono text-sm text-slate-300">{(task.best_score ?? 0).toFixed(3)}</div>
            </div>
            {task.error ? <div className="mt-2 text-xs text-[#fecaca]">{task.error}</div> : null}
          </div>
        ))}
      </div>
    </Card>
  );
}

function ChampionCard({ champion, instrumentNames }: { champion?: GenomeRecord; instrumentNames: Record<string, string> }) {
  return (
    <Card className="border-[#2dd4bf]/20">
      <CardHeader>
        <div><CardTitle>目前採用參數</CardTitle><CardDescription>{champion ? `${labelForInstrument(champion.instrument_id, instrumentNames)} · ${relativeTime(champion.created_at)}` : "尚未採用"}</CardDescription></div>
        <CheckCircle2 className="h-5 w-5 text-[#2dd4bf]" />
      </CardHeader>
      {champion ? (
        <div className="space-y-4">
          <div className="grid gap-3 md:grid-cols-2"><Metric label="評分" value={champion.score_total.toFixed(3)} /><Metric label="最大回撤" value={formatPercent(champion.max_drawdown)} danger /></div>
          <JsonPreview value={champion.param_pack} />
        </div>
      ) : <div className="text-sm text-slate-500">候選參數晉升後會出現在這裡。</div>}
    </Card>
  );
}

function LegacyGenomeLibrary({ genomes, instrumentNames }: { genomes: GenomeRecord[]; instrumentNames: Record<string, string> }) {
  const [confirmPromote, setConfirmPromote] = useState<number | null>(null);
  const queryClient = useQueryClient();
  const promoteMutation = useMutation({
    mutationFn: (id: number) => evolutionApi.promote(id),
    onSuccess: () => {
      setConfirmPromote(null);
      queryClient.invalidateQueries({ queryKey: ["genomes"] });
      queryClient.invalidateQueries({ queryKey: ["evolution-tasks"] });
    }
  });
  return (
    <div className="grid gap-4 lg:grid-cols-2">
      {genomes.map((genome) => {
        const isChampion = genome.role === "champion";
        const canPromote = genome.role === "candidate" || genome.role === "challenger" || genome.role === "retired";
        return (
          <Card key={genome.id} className={cn(isChampion ? "border-[#2dd4bf]/30" : "")}>
            <CardHeader>
              <div><CardTitle>{isChampion ? "已採用參數" : genome.role === "retired" ? "已退休參數" : "候選參數"}</CardTitle><CardDescription>{labelForInstrument(genome.instrument_id, instrumentNames)} · {relativeTime(genome.created_at)}</CardDescription></div>
              <span className="font-mono text-lg font-semibold text-slate-100">{genome.score_total.toFixed(3)}</span>
            </CardHeader>
            <div className="space-y-4">
              <div className="grid grid-cols-2 gap-3"><Metric label="最大回撤" value={formatPercent(genome.max_drawdown)} danger /><Metric label="週期" value={intervalLabels[genome.interval ?? "1d"] ?? genome.interval ?? "-"} /></div>
              <details className="rounded-lg border border-white/[0.04] bg-slate-950/40 p-3"><summary className="cursor-pointer text-sm font-semibold text-slate-300">參數 JSON</summary><div className="mt-3"><JsonPreview value={genome.param_pack} /></div></details>
              <div className="flex flex-wrap gap-2">
                {canPromote && !isChampion ? (confirmPromote === genome.id ? <Button loading={promoteMutation.isPending} onClick={() => promoteMutation.mutate(genome.id)}>確認採用</Button> : <Button onClick={() => setConfirmPromote(genome.id)}>設為採用</Button>) : null}
                <Link to={`/backtesting?genome=${genome.id}`}><Button variant="secondary">回測</Button></Link>
              </div>
            </div>
          </Card>
        );
      })}
    </div>
  );
}

function parseTagInput(value: string) {
  return Array.from(new Set(value.split(",").map((item) => item.trim()).filter(Boolean)));
}

function formatSearchDate(value?: number) {
  return value ? new Date(value).toLocaleDateString("zh-TW") : "未記錄";
}

function roleTitle(role: GenomeRecord["role"]) {
  if (role === "champion") return "已採用參數";
  if (role === "retired" || role === "archived") return "歷史參數";
  return "候選參數";
}

function InfoRow({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-center justify-between gap-3 rounded-md bg-slate-950/40 px-3 py-2">
      <span className="text-slate-500">{label}</span>
      <span className="text-right font-mono text-slate-200">{value}</span>
    </div>
  );
}

function GenomeLibrary({ genomes, instrumentNames }: { genomes: GenomeRecord[]; instrumentNames: Record<string, string> }) {
  const [confirmPromote, setConfirmPromote] = useState<number | null>(null);
  const [confirmDelete, setConfirmDelete] = useState<number | null>(null);
  const [editingId, setEditingId] = useState<number | null>(null);
  const [draftName, setDraftName] = useState("");
  const [draftNotes, setDraftNotes] = useState("");
  const [draftTags, setDraftTags] = useState("");
  const [instrumentFilterMode, setInstrumentFilterMode] = useState<"all" | "include" | "exclude">("all");
  const [selectedInstruments, setSelectedInstruments] = useState<string[]>([]);
  const [tagFilter, setTagFilter] = useState("");
  const queryClient = useQueryClient();
  const promoteMutation = useMutation({
    mutationFn: (id: number) => evolutionApi.promote(id),
    onSuccess: () => {
      setConfirmPromote(null);
      queryClient.invalidateQueries({ queryKey: ["genomes"] });
      queryClient.invalidateQueries({ queryKey: ["evolution-tasks"] });
    }
  });
  const updateMutation = useMutation({
    mutationFn: ({ id, name, notes, tags }: { id: number; name: string; notes: string; tags: string[] }) => evolutionApi.updateGenome(id, { name, notes, tags }),
    onSuccess: () => {
      setEditingId(null);
      queryClient.invalidateQueries({ queryKey: ["genomes"] });
      queryClient.invalidateQueries({ queryKey: ["evolution-tasks"] });
    }
  });
  const deleteMutation = useMutation({
    mutationFn: (id: number) => evolutionApi.deleteGenome(id),
    onSuccess: () => {
      setConfirmDelete(null);
      queryClient.invalidateQueries({ queryKey: ["genomes"] });
      queryClient.invalidateQueries({ queryKey: ["evolution-tasks"] });
    }
  });
  const availableInstruments = useMemo(() => Array.from(new Set(genomes.map((item) => item.instrument_id).filter(Boolean))) as string[], [genomes]);
  const tagFilters = parseTagInput(tagFilter).map((item) => item.toLowerCase());
  const filteredGenomes = genomes.filter((genome) => {
    const instrument = genome.instrument_id ?? "";
    if (instrumentFilterMode === "include" && selectedInstruments.length > 0 && !selectedInstruments.includes(instrument)) return false;
    if (instrumentFilterMode === "exclude" && selectedInstruments.includes(instrument)) return false;
    if (tagFilters.length > 0) {
      const tags = (genome.tags ?? []).map((item) => item.toLowerCase());
      if (!tagFilters.some((tag) => tags.includes(tag))) return false;
    }
    return true;
  });

  function toggleInstrument(id: string) {
    setSelectedInstruments((current) => (current.includes(id) ? current.filter((item) => item !== id) : [...current, id]));
  }

  function startEdit(genome: GenomeRecord) {
    setEditingId(genome.id);
    setDraftName(genome.name ?? "");
    setDraftNotes(genome.notes ?? "");
    setDraftTags((genome.tags ?? []).join(", "));
  }

  function saveEdit(id: number) {
    updateMutation.mutate({ id, name: draftName, notes: draftNotes, tags: parseTagInput(draftTags) });
  }

  return (
    <div className="space-y-4">
      <Card className="p-4">
        <div className="grid gap-4 lg:grid-cols-[12rem_1fr_16rem]">
          <Select label="標的篩選" value={instrumentFilterMode} onChange={(value) => setInstrumentFilterMode(value as typeof instrumentFilterMode)} options={[["all", "全部顯示"], ["include", "只顯示勾選標的"], ["exclude", "排除勾選標的"]]} />
          <div>
            <div className="mb-2 text-sm text-slate-300">標的</div>
            <div className="flex flex-wrap gap-2">
              {availableInstruments.map((id) => (
                <button key={id} type="button" className={cn("rounded-lg border px-3 py-2 text-sm transition", selectedInstruments.includes(id) ? "border-[#2dd4bf]/40 bg-[#2dd4bf]/10 text-[#99f6e4]" : "border-white/[0.06] text-slate-400 hover:text-slate-200")} onClick={() => toggleInstrument(id)}>
                  {labelForInstrument(id, instrumentNames)}
                </button>
              ))}
              {availableInstruments.length === 0 ? <span className="text-sm text-slate-500">尚無標的可篩選</span> : null}
            </div>
          </div>
          <label>
            <span className="mb-2 block text-sm text-slate-300">標籤篩選</span>
            <input className="h-11 w-full rounded-lg border border-slate-700 bg-slate-900/80 px-3 text-sm text-slate-100 outline-none focus:border-[#2dd4bf]" value={tagFilter} onChange={(event) => setTagFilter(event.target.value)} placeholder="例如：穩健, SOXL" />
          </label>
        </div>
      </Card>

      {filteredGenomes.length === 0 ? <Card className="p-4 text-sm text-slate-500">沒有符合篩選條件的參數。</Card> : null}
      <div className="grid gap-4 lg:grid-cols-2">
        {filteredGenomes.map((genome) => {
          const isChampion = genome.role === "champion";
          const canPromote = genome.role === "candidate" || genome.role === "challenger" || genome.role === "retired" || genome.role === "archived";
          const searchConfig = genome.search_config ?? {};
          const isEditing = editingId === genome.id;
          return (
            <Card key={genome.id} className={cn(isChampion ? "border-[#2dd4bf]/30" : "")}>
              <CardHeader>
                <div>
                  <CardTitle>{genome.name?.trim() || roleTitle(genome.role)}</CardTitle>
                  <CardDescription>#{genome.id} · {labelForInstrument(genome.instrument_id, instrumentNames)} · {relativeTime(genome.created_at)}</CardDescription>
                </div>
                <span className="font-mono text-lg font-semibold text-slate-100">{genome.score_total.toFixed(3)}</span>
              </CardHeader>
              <div className="space-y-4">
                {isEditing ? (
                  <div className="space-y-3 rounded-lg border border-white/[0.06] bg-slate-950/40 p-3">
                    <label><span className="mb-2 block text-sm text-slate-300">名稱</span><input className="h-10 w-full rounded-lg border border-slate-700 bg-slate-900/80 px-3 text-sm text-slate-100 outline-none focus:border-[#2dd4bf]" value={draftName} onChange={(event) => setDraftName(event.target.value)} /></label>
                    <label><span className="mb-2 block text-sm text-slate-300">標籤</span><input className="h-10 w-full rounded-lg border border-slate-700 bg-slate-900/80 px-3 text-sm text-slate-100 outline-none focus:border-[#2dd4bf]" value={draftTags} onChange={(event) => setDraftTags(event.target.value)} placeholder="用逗號分隔" /></label>
                    <label><span className="mb-2 block text-sm text-slate-300">備註</span><textarea className="min-h-24 w-full rounded-lg border border-slate-700 bg-slate-900/80 p-3 text-sm text-slate-100 outline-none focus:border-[#2dd4bf]" value={draftNotes} onChange={(event) => setDraftNotes(event.target.value)} /></label>
                    <div className="flex flex-wrap gap-2"><Button icon={Save} loading={updateMutation.isPending} onClick={() => saveEdit(genome.id)}>儲存</Button><Button icon={X} variant="secondary" onClick={() => setEditingId(null)}>取消</Button></div>
                  </div>
                ) : (
                  <>
                    <div className="flex flex-wrap gap-2">
                      {(genome.tags ?? []).map((tag) => <span key={tag} className="rounded-md border border-[#2dd4bf]/20 bg-[#2dd4bf]/10 px-2 py-1 text-xs text-[#99f6e4]">{tag}</span>)}
                      {(genome.tags ?? []).length === 0 ? <span className="text-xs text-slate-500">尚未設定標籤</span> : null}
                    </div>
                    {genome.notes ? <p className="rounded-lg border border-white/[0.04] bg-white/[0.02] p-3 text-sm text-slate-300">{genome.notes}</p> : null}
                  </>
                )}

                <div className="grid grid-cols-2 gap-3">
                  <Metric label="最大回撤" value={formatPercent(genome.max_drawdown)} danger />
                  <Metric label="資料週期" value={intervalLabels[genome.interval ?? "1d"] ?? genome.interval ?? "-"} />
                </div>

                <div className="rounded-lg border border-white/[0.04] bg-white/[0.02] p-3">
                  <div className="mb-3 text-sm font-semibold text-slate-300">搜尋條件</div>
                  <div className="grid gap-2 text-sm md:grid-cols-2">
                    <InfoRow label="研究標的" value={labelForInstrument(searchConfig.instrument_id ?? genome.instrument_id, instrumentNames)} />
                    <InfoRow label="資料週期" value={searchConfig.interval ?? genome.interval ?? "未記錄"} />
                    <InfoRow label="開始日期" value={formatSearchDate(searchConfig.train_start_ms)} />
                    <InfoRow label="結束日期" value={formatSearchDate(searchConfig.train_end_ms)} />
                    <InfoRow label="初始本金" value={searchConfig.initial_capital !== undefined ? formatMoney(searchConfig.initial_capital) : "未記錄"} />
                    <InfoRow label="每月投入" value={searchConfig.monthly_dca !== undefined ? formatMoney(searchConfig.monthly_dca) : "未記錄"} />
                    <InfoRow label="調倉門檻" value={searchConfig.evolve_rebalance_threshold || searchConfig.gene_options?.EvolveRebalanceThreshold || searchConfig.gene_options?.evolve_rebalance_threshold ? "參與演化" : "固定為 0"} />
                    <InfoRow label="強制滿倉門檻" value={searchConfig.evolve_force_full_threshold || searchConfig.gene_options?.EvolveForceFullThreshold ? "參與演化" : "固定為 100%"} />
                    <InfoRow label="強制空倉門檻" value={searchConfig.evolve_force_empty_threshold || searchConfig.gene_options?.EvolveForceEmptyThreshold ? "參與演化" : "固定為 0%"} />
                    <InfoRow label="倉位回饋 Gamma" value={searchConfig.evolve_gamma || searchConfig.gene_options?.EvolveGamma ? "參與演化" : "固定為 0"} />
                    <InfoRow label="倉位結構" value={(searchConfig.position_structure ?? searchConfig.gene_options?.PositionStructure) === "floating_only" ? "純浮動模型" : "雙層模型"} />
                    <InfoRow label="啟用訊號" value={`${searchConfig.enable_w_mean ?? searchConfig.gene_options?.EnableWMean ?? true ? "均值" : "-"} / ${searchConfig.enable_w_momentum ?? searchConfig.gene_options?.EnableWMomentum ?? true ? "動能" : "-"} / ${searchConfig.enable_w_breakout ?? searchConfig.gene_options?.EnableWBreakout ?? true ? "突破" : "-"}`} />
                    <InfoRow label="每次交易懲罰" value={searchConfig.trade_penalty !== undefined ? Number(searchConfig.trade_penalty).toFixed(4) : "0.0000"} />
                    <InfoRow label="手續費率" value={searchConfig.fee_rate !== undefined ? formatPercent(searchConfig.fee_rate) : "未記錄"} />
                    <InfoRow label="價差 / 滑價率" value={searchConfig.spread_rate !== undefined ? formatPercent(searchConfig.spread_rate) : "未記錄"} />
                    <InfoRow label="執行假設" value={searchConfig.execution_mode ?? genome.execution_mode ?? "未記錄"} />
                    <InfoRow label="起始方式" value={searchConfig.spawn_mode ?? "未記錄"} />
                    <InfoRow label="族群數" value={searchConfig.population?.toLocaleString("zh-TW") ?? "未記錄"} />
                    <InfoRow label="世代數" value={searchConfig.generations?.toLocaleString("zh-TW") ?? "未記錄"} />
                  </div>
                </div>

                <details className="rounded-lg border border-white/[0.04] bg-slate-950/40 p-3"><summary className="cursor-pointer text-sm font-semibold text-slate-300">參數 JSON</summary><div className="mt-3"><JsonPreview value={genome.param_pack} /></div></details>
                <div className="flex flex-wrap gap-2">
                  {canPromote && !isChampion ? (confirmPromote === genome.id ? <Button loading={promoteMutation.isPending} onClick={() => promoteMutation.mutate(genome.id)}>確認採用</Button> : <Button onClick={() => setConfirmPromote(genome.id)}>設為採用</Button>) : null}
                  <Button variant="secondary" onClick={() => startEdit(genome)}>編輯資料</Button>
                  <Link to={`/backtesting?genome=${genome.id}`}><Button variant="secondary">回測</Button></Link>
                  {confirmDelete === genome.id ? <Button icon={Trash2} variant="danger" loading={deleteMutation.isPending} onClick={() => deleteMutation.mutate(genome.id)}>確認刪除</Button> : <Button icon={Trash2} variant="danger" onClick={() => setConfirmDelete(genome.id)}>刪除</Button>}
                </div>
              </div>
            </Card>
          );
        })}
      </div>
    </div>
  );
}

export function EvolutionPage() {
  const [tab, setTab] = useState<"optimize" | "library">("optimize");
  const instrumentsQuery = useQuery({ queryKey: ["market-data-instruments"], queryFn: () => marketDataApi.instruments() });
  const instrumentNames = useMemo(() => Object.fromEntries((instrumentsQuery.data?.instruments ?? []).map((item) => [item.id, item.display_name])), [instrumentsQuery.data]);
  const { data: genomes = [] } = useQuery({ queryKey: ["genomes"], queryFn: () => evolutionApi.listGenomes() });
  const champion = useMemo(() => genomes.find((item) => item.role === "champion"), [genomes]);

  return (
    <section className="space-y-4">
      <div><h1 className="text-2xl font-bold text-slate-100">優化實驗室</h1><p className="mt-1 text-sm text-slate-400">針對研究標的搜尋參數、觀察運算過程，並管理可回復的候選參數。</p></div>
      <div className="flex w-fit rounded-lg border border-white/[0.06] bg-white/[0.03] p-1">
        {[["optimize", "參數搜尋"], ["library", "參數庫"]].map(([value, label]) => <button key={value} className={cn("rounded-md px-4 py-2 text-sm font-semibold transition", tab === value ? "bg-[#2dd4bf]/10 text-[#2dd4bf]" : "text-slate-500")} onClick={() => setTab(value as typeof tab)}>{label}</button>)}
      </div>
      {tab === "optimize" ? <div className="space-y-4"><EvolutionPanel instrumentNames={instrumentNames} /><TaskQueueView instrumentNames={instrumentNames} /><ChampionCard champion={champion} instrumentNames={instrumentNames} /></div> : genomes.length > 0 ? <GenomeLibrary genomes={genomes} instrumentNames={instrumentNames} /> : <Card className="p-4 text-sm text-slate-500">尚無參數。</Card>}
    </section>
  );
}
