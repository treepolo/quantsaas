import { FormEvent, useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Activity, CheckCircle2, FlaskConical, Save, Square, TerminalSquare, Trash2, X } from "lucide-react";
import { formatPercent, relativeTime, shortDateTime } from "../../shared/lib/format";
import { evolutionApi, type EvolutionTask, type GenomeRecord, type TraceMode } from "../../shared/services/evolution";
import { marketDataApi } from "../../shared/services/marketData";
import { Button } from "../../shared/ui/Button";
import { Card, CardDescription, CardHeader, CardTitle } from "../../shared/ui/Card";
import { StatusBadge } from "../../shared/ui/StatusBadge";
import { cn } from "../../shared/lib/cn";

const intervalLabels: Record<string, string> = { "1d": "日 K", "1h": "1 小時", "15m": "15 分鐘", "5m": "5 分鐘", "1m": "1 分鐘", "1s": "1 秒" };
const executionModes = [
  ["close_same_bar", "收盤同根", "用當根收盤價作為研究判斷基準"],
  ["close_next_open", "隔日開盤", "用收盤訊號，假設下一根開盤才調整"],
  ["preclose_10m", "收盤前 10 分鐘", "需要額外快照資料，缺資料時不會假裝可用"]
] as const;
const traceModeOptions: Array<[TraceMode, string, string]> = [
  ["off", "關閉", "不產生原始追蹤"],
  ["summary", "摘要", "只顯示任務與世代摘要"],
  ["detailed", "詳細", "顯示資料視窗、個體評估與世代資訊"],
  ["full", "逐筆", "顯示策略步進事件，會拖慢運算"]
];

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
  const [open, setOpen] = useState(true);
  const [mode, setMode] = useState<TraceMode>(task.trace_mode ?? "detailed");
  const queryClient = useQueryClient();
  const traceQuery = useQuery({
    queryKey: ["evolution-trace", task.id, open, mode],
    queryFn: () => evolutionApi.trace(task.id, mode === "full" ? 1200 : 600),
    enabled: open,
    refetchInterval: open ? 1000 : false
  });
  const modeMutation = useMutation({
    mutationFn: (nextMode: TraceMode) => evolutionApi.setTraceMode(task.id, nextMode),
    onSuccess: (result) => {
      setMode(result.mode);
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
        {mode === "full" ? <div className="text-xs text-[#fde68a]">逐筆追蹤會拖慢優化，只建議短時間觀察。</div> : null}
        {open ? (
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

function EvolutionPanel({ instrumentNames }: { instrumentNames: Record<string, string> }) {
  const queryClient = useQueryClient();
  const instrumentsQuery = useQuery({ queryKey: ["market-data-instruments"], queryFn: () => marketDataApi.instruments() });
  const instruments = instrumentsQuery.data?.instruments ?? [];
  const [expanded, setExpanded] = useState(false);
  const [instrumentId, setInstrumentId] = useState("BTCUSDT");
  const selected = instruments.find((item) => item.id === instrumentId);
  const [interval, setInterval] = useState("1d");
  const [executionMode, setExecutionMode] = useState("close_same_bar");
  const [startDate, setStartDate] = useState(dateInputValue(new Date(Date.now() - 365 * 24 * 60 * 60 * 1000)));
  const [endDate, setEndDate] = useState(dateInputValue(new Date()));
  const [population, setPopulation] = useState(300);
  const [generations, setGenerations] = useState(25);
  const [spawnMode, setSpawnMode] = useState<"inherit" | "random_once" | "manual">("inherit");
  const [traceMode, setTraceMode] = useState<TraceMode>("detailed");
  const [continuousMode, setContinuousMode] = useState<"" | "standardized_best" | "random">("");
  const [continuousIterations, setContinuousIterations] = useState(3);
  const [continuousUnlimited, setContinuousUnlimited] = useState(false);
  const [standardStartDate, setStandardStartDate] = useState(startDate);
  const [standardEndDate, setStandardEndDate] = useState(endDate);
  const overviewQuery = useQuery({ queryKey: ["evolution-tasks"], queryFn: () => evolutionApi.listTasks(), refetchInterval: 2_000 });
  const running = overviewQuery.data?.current_task ?? overviewQuery.data?.tasks.find((task) => task.status === "running");
  const createMutation = useMutation({
    mutationFn: () =>
      evolutionApi.createTask({
        strategy_id: "sigmoid-dca-btc",
        pair: selected?.symbol ?? instrumentId,
        instrument_id: instrumentId,
        data_source: selected?.data_source,
        interval,
        execution_mode: executionMode,
        train_start_ms: dayStartMs(startDate),
        train_end_ms: dayEndMs(endDate),
        pop_size: population,
        max_generations: generations,
        spawn_mode: spawnMode,
        trace_mode: traceMode,
        continuous_mode: continuousMode,
        continuous_iterations: continuousIterations,
        continuous_unlimited: continuousUnlimited,
        standard_start_ms: continuousMode === "standardized_best" ? dayStartMs(standardStartDate) : undefined,
        standard_end_ms: continuousMode === "standardized_best" ? dayEndMs(standardEndDate) : undefined
      }),
    onSuccess: () => {
      setExpanded(false);
      queryClient.invalidateQueries({ queryKey: ["evolution-tasks"] });
    }
  });
  const cancelMutation = useMutation({
    mutationFn: (taskId: number) => evolutionApi.cancel(taskId),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["evolution-tasks"] })
  });

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    createMutation.mutate();
  }

  function changeInstrument(nextId: string) {
    const next = instruments.find((item) => item.id === nextId);
    setInstrumentId(nextId);
    setInterval(next?.supported_intervals[0] ?? "1d");
  }

  if (running) {
    const current = running.current_generation ?? Math.round((running.progress || 0) * (running.max_generations ?? 25));
    const max = running.max_generations ?? 25;
    const progressPct = Math.min(100, Math.round((running.progress || 0) * 100));
    const evaluated = running.evaluated_individuals ?? current * (running.pop_size ?? 0);
    const planned = running.planned_evaluations ?? (running.pop_size ?? 0) * max;
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
          </div>
        </Card>
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
          <NumberInput label="族群數" min={10} max={500} value={population} onChange={setPopulation} />
          <NumberInput label="世代數" min={5} max={50} value={generations} onChange={setGenerations} />
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
          {executionMode === "preclose_10m" ? <div className="md:col-span-2 text-xs text-[#fde68a]">這個模式需要收盤前快照資料；缺資料時任務可能無法產生有效結果。</div> : null}
          <div className="md:col-span-2">
            <Button type="submit" loading={createMutation.isPending}>開始搜尋</Button>
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

function NumberInput({ label, value, min, max, onChange }: { label: string; value: number; min: number; max: number; onChange: (value: number) => void }) {
  return (
    <label>
      <span className="mb-2 block text-sm text-slate-300">{label}</span>
      <input className="h-11 w-full rounded-lg border border-slate-700 bg-slate-900/80 px-3 font-mono text-sm text-slate-100 outline-none focus:border-[#2dd4bf]" type="number" min={min} max={max} value={value} onChange={(event) => onChange(Number(event.target.value))} />
    </label>
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
