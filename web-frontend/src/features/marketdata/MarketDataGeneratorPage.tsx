import { type FormEvent, useEffect, useMemo, useRef, useState } from "react";
import { useMutation, useQueries, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowDown, ArrowUp, CheckCircle2, Copy, Database, GripVertical, Play, Scissors, Trash2, WandSparkles } from "lucide-react";
import {
  marketDataApi,
  type GenerateLeveragedResult,
  type MarketVersionBar,
  type RecompositionSegmentInput,
  type RecompositionSource,
  type ResearchInstrument
} from "../../shared/services/marketData";
import { computeTasksApi, type ComputeTask } from "../../shared/services/computeTasks";
import { datasetStartDate } from "../../shared/lib/datasetDates";
import { Button } from "../../shared/ui/Button";
import { Card, CardDescription, CardHeader, CardTitle } from "../../shared/ui/Card";
import { PerturbationWorkspace } from "./PerturbationWorkspace";
import { SearchablePicker } from "../../shared/ui/SearchablePicker";

const intervalLabels: Record<string, string> = {
  "1d": "日 K", "1h": "1 小時 K", "15m": "15 分 K", "5m": "5 分 K", "1m": "1 分 K", "1s": "1 秒 K", "1w": "週 K", "1M": "月 K"
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

function formatMs(value?: number) {
  if (!value) return "-";
  return new Intl.DateTimeFormat("zh-TW", { year: "numeric", month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", hour12: false }).format(new Date(value));
}

function sourceLabel(source?: string) {
  if (source === "yahoo") return "Yahoo";
  if (source === "binance") return "Binance";
  if (source === "generated") return "產生資料";
  if (source === "fred") return "FRED";
  return source || "-";
}

function newItemID() {
  return `segment-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
}

export function MarketDataGeneratorPage() {
  const [mode, setMode] = useState<"leverage" | "perturbation" | "recomposition">(() => new URLSearchParams(location.search).get("mode") === "perturbation" ? "perturbation" : "recomposition");
  const instrumentsQuery = useQuery({ queryKey: ["market-data-instruments"], queryFn: () => marketDataApi.instruments() });
  const instruments = instrumentsQuery.data?.instruments ?? [];
  return (
    <div className="mx-auto flex w-full max-w-7xl flex-col gap-5 p-6">
      <div>
        <div className="flex items-center gap-3 text-sm font-medium text-[#99f6e4]"><WandSparkles className="h-4 w-4" />行情資料產生器</div>
        <h1 className="mt-2 text-2xl font-semibold text-slate-100">建立可重現的研究行情</h1>
        <p className="mt-2 max-w-4xl text-sm leading-6 text-slate-400">每日倍數模式維持既有覆寫行為；局部擾動與片段重組使用不可變來源、版本化配方與內容稽核，成品不會被後續操作覆寫。</p>
      </div>
      <div className="flex gap-2 rounded-xl border border-white/10 bg-white/[0.02] p-1">
        <ModeButton active={mode === "leverage"} onClick={() => setMode("leverage")}>每日倍數做多</ModeButton>
        <ModeButton active={mode === "perturbation"} onClick={() => setMode("perturbation")}>局部行情擾動</ModeButton>
        <ModeButton active={mode === "recomposition"} onClick={() => setMode("recomposition")}>K 線片段重組</ModeButton>
      </div>
      <div className={mode === "recomposition" ? "block" : "hidden"}><RecompositionEditor /></div>
      <div className={mode === "perturbation" ? "block" : "hidden"}><PerturbationWorkspace /></div>
      <div className={mode === "leverage" ? "block" : "hidden"}><LeverageGenerator instruments={instruments} /></div>
    </div>
  );
}

function ModeButton({ active, onClick, children }: { active: boolean; onClick: () => void; children: React.ReactNode }) {
  return <button type="button" onClick={onClick} className={`flex-1 rounded-lg px-4 py-2 text-sm font-semibold transition ${active ? "bg-[#2dd4bf] text-slate-950" : "text-slate-400 hover:bg-white/[0.05] hover:text-slate-100"}`}>{children}</button>;
}

type EditorSegment = RecompositionSegmentInput & {
  sourceKey: string;
  sourceName: string;
  sourceSymbol: string;
  barTimes: number[];
};

function sourceKey(source: RecompositionSource) {
  return source.version_id ? `version:${source.version_id}` : `instrument:${source.instrument.id}`;
}

function RecompositionEditor() {
  const queryClient = useQueryClient();
  const sourcesQuery = useQuery({ queryKey: ["recomposition-sources"], queryFn: () => marketDataApi.recompositionSources() });
  const seriesQuery = useQuery({ queryKey: ["market-series"], queryFn: () => marketDataApi.marketSeries() });
  const sources = sourcesQuery.data?.items ?? [];
  const [interval, setInterval] = useState("1d");
  const eligibleSources = useMemo(() => sources.filter((source) => source.instrument.supported_intervals.includes(interval) && !source.archived), [sources, interval]);
  const [selectedSourceKey, setSelectedSourceKey] = useState("");
  const selectedSource = eligibleSources.find((source) => sourceKey(source) === selectedSourceKey) ?? eligibleSources[0];
  const now = new Date();
  const [startDate, setStartDate] = useState("");
  const [endDate, setEndDate] = useState(dateInputValue(now));
  const [bars, setBars] = useState<MarketVersionBar[]>([]);
  const [selectionStart, setSelectionStart] = useState(0);
  const [selectionEnd, setSelectionEnd] = useState(0);
  const [segments, setSegments] = useState<EditorSegment[]>([]);
  const [calendarKey, setCalendarKey] = useState("");
  const [outputStartMs, setOutputStartMs] = useState(0);
  const [confirmSoftLimit, setConfirmSoftLimit] = useState(false);
  const [previewTaskID, setPreviewTaskID] = useState<number>();
  const [planID, setPlanID] = useState<number>();
  const [seriesName, setSeriesName] = useState("片段重組行情");
  const [generationID, setGenerationID] = useState<number>();
  const [generationRootID, setGenerationRootID] = useState<number>();
  const [draggingID, setDraggingID] = useState<string>();

  useEffect(() => {
    if (!selectedSourceKey && selectedSource) setSelectedSourceKey(sourceKey(selectedSource));
  }, [selectedSource, selectedSourceKey]);
  const selectedDatasetStart = datasetStartDate(selectedSource?.instrument, interval);
  useEffect(() => {
    if (selectedDatasetStart) setStartDate(selectedDatasetStart);
  }, [selectedDatasetStart]);

  const loadBars = useMutation({
    mutationFn: () => marketDataApi.recompositionSourceBars({
      instrumentId: selectedSource?.version_id ? undefined : selectedSource?.instrument.id,
      versionId: selectedSource?.version_id,
      interval,
      startTimeMs: dayStartMs(startDate),
      endTimeMs: dayEndMs(endDate),
      limit: 5000
    }),
    onSuccess: (result) => {
      setBars(result.rows);
      setSelectionStart(0);
      setSelectionEnd(Math.max(0, result.rows.length - 1));
    }
  });

  const previewMutation = useMutation({
    mutationFn: () => {
      const calendar = sources.find((source) => sourceKey(source) === calendarKey);
      if (!calendar) throw new Error("請指定目前載入來源為輸出日曆");
      return marketDataApi.createRecompositionPreview({
        segments: segments.map(({ item_id, source_instrument_id, source_version_id, start_time_ms, end_time_ms, repeat_count }) => ({ item_id, source_instrument_id, source_version_id, start_time_ms, end_time_ms, repeat_count })),
        interval,
        calendar_instrument_id: calendar.version_id ? undefined : calendar.instrument.id,
        calendar_source_version_id: calendar.version_id,
        output_start_time_ms: outputStartMs
      }, confirmSoftLimit);
    },
    onSuccess: (result) => {
      setPreviewTaskID(result.task.id);
      setPlanID(undefined);
      setGenerationID(undefined);
      setGenerationRootID(undefined);
    }
  });

  const previewTaskQuery = useQuery({
    queryKey: ["compute-task", previewTaskID],
    queryFn: () => computeTasksApi.get(previewTaskID!),
    enabled: Boolean(previewTaskID),
    refetchInterval: previewTaskID ? 1000 : false
  });
  const previewItemsQuery = useQuery({
    queryKey: ["compute-task-items", previewTaskID, previewTaskQuery.data?.status],
    queryFn: () => computeTasksApi.items(previewTaskID!, { includeResult: true }),
    enabled: Boolean(previewTaskID && previewTaskQuery.data?.status === "completed")
  });
  useEffect(() => {
    const result = previewItemsQuery.data?.[0]?.result as { plan_id?: number } | undefined;
    if (result?.plan_id) setPlanID(result.plan_id);
  }, [previewItemsQuery.data]);
  const planQuery = useQuery({ queryKey: ["recomposition-plan", planID], queryFn: () => marketDataApi.recompositionPlan(planID!), enabled: Boolean(planID) });
  const previewBarsQuery = useQuery({ queryKey: ["recomposition-plan-bars", planID], queryFn: () => marketDataApi.recompositionPreviewBars(planID!, 5000), enabled: Boolean(planID) });

  const startTask = useMutation({
    mutationFn: (taskID: number) => computeTasksApi.start(taskID),
    onSuccess: (task) => queryClient.setQueryData(["compute-task", task.id], task)
  });
  const generationMutation = useMutation({
    mutationFn: () => {
      if (!planQuery.data) throw new Error("請先完成預覽");
      return marketDataApi.createRecompositionGeneration({ plan_id: planQuery.data.plan_id, plan_hash: planQuery.data.plan_hash, series_name: seriesName }, confirmSoftLimit);
    },
    onSuccess: (result) => {
      setGenerationID(result.generation.generation_id);
      setGenerationRootID(result.task.id);
    }
  });
  const generationRootQuery = useQuery({
    queryKey: ["compute-task", generationRootID], queryFn: () => computeTasksApi.get(generationRootID!), enabled: Boolean(generationRootID), refetchInterval: generationRootID ? 1000 : false
  });
  const stageQueries = useQueries({
    queries: (generationRootQuery.data?.child_task_ids ?? []).map((id) => ({ queryKey: ["compute-task", id], queryFn: () => computeTasksApi.get(id), refetchInterval: 1000 }))
  });
  const generationQuery = useQuery({
    queryKey: ["recomposition-generation", generationID, generationRootQuery.data?.status],
    queryFn: () => marketDataApi.recompositionGeneration(generationID!), enabled: Boolean(generationID), refetchInterval: generationID ? 1500 : false
  });
  useEffect(() => {
    if (!generationQuery.data?.published) return;
    void queryClient.invalidateQueries({ queryKey: ["market-series"] });
    void queryClient.invalidateQueries({ queryKey: ["recomposition-sources"] });
  }, [generationQuery.data?.published, queryClient]);
  const archiveVersion = useMutation({
    mutationFn: (id: number) => marketDataApi.archiveMarketVersion(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["market-series"] });
      queryClient.invalidateQueries({ queryKey: ["recomposition-sources"] });
    }
  });

  const editorError = [sourcesQuery.error, loadBars.error, previewMutation.error, startTask.error, generationMutation.error].find((error) => error instanceof Error) as Error | undefined;
  const selectedLow = Math.min(selectionStart, selectionEnd);
  const selectedHigh = Math.max(selectionStart, selectionEnd);

  function addSegment() {
    if (!selectedSource || !bars.length) return;
    const selected = bars.slice(selectedLow, selectedHigh + 1);
    if (!selected.length) return;
    setSegments((current) => [...current, {
      item_id: newItemID(), sourceKey: sourceKey(selectedSource), sourceName: selectedSource.instrument.display_name,
      sourceSymbol: selectedSource.instrument.symbol, source_instrument_id: selectedSource.version_id ? undefined : selectedSource.instrument.id,
      source_version_id: selectedSource.version_id, start_time_ms: selected[0].open_time, end_time_ms: selected[selected.length - 1].open_time,
      repeat_count: 1, barTimes: selected.map((bar) => bar.open_time)
    }]);
  }

  function updateSegment(id: string, update: Partial<EditorSegment>) {
    setSegments((current) => current.map((segment) => segment.item_id === id ? { ...segment, ...update } : segment));
  }

  function moveSegment(id: string, delta: number) {
    setSegments((current) => {
      const from = current.findIndex((segment) => segment.item_id === id);
      const to = Math.max(0, Math.min(current.length - 1, from + delta));
      if (from < 0 || from === to) return current;
      const next = [...current];
      const [item] = next.splice(from, 1);
      next.splice(to, 0, item);
      return next;
    });
  }

  function splitSegment(segment: EditorSegment) {
    if (segment.barTimes.length < 2) return;
    const midpoint = Math.ceil(segment.barTimes.length / 2);
    const firstTimes = segment.barTimes.slice(0, midpoint);
    const secondTimes = segment.barTimes.slice(midpoint);
    const first = { ...segment, end_time_ms: firstTimes[firstTimes.length - 1], barTimes: firstTimes };
    const second = { ...segment, item_id: newItemID(), start_time_ms: secondTimes[0], end_time_ms: secondTimes[secondTimes.length - 1], barTimes: secondTimes };
    setSegments((current) => current.flatMap((item) => item.item_id === segment.item_id ? [first, second] : [item]));
  }

  function dropOn(targetID: string) {
    if (!draggingID || draggingID === targetID) return;
    setSegments((current) => {
      const next = [...current];
      const from = next.findIndex((item) => item.item_id === draggingID);
      const to = next.findIndex((item) => item.item_id === targetID);
      if (from < 0 || to < 0) return current;
      const [item] = next.splice(from, 1);
      next.splice(to, 0, item);
      return next;
    });
    setDraggingID(undefined);
  }

  return (
    <div className="grid gap-5">
      <div className="grid gap-5 xl:grid-cols-[minmax(0,1.25fr)_minmax(390px,0.75fr)]">
        <Card className="p-5">
          <CardHeader><div><CardTitle>1. 選取來源片段</CardTitle><CardDescription>同一週期可混用不同標的；點圖兩次或拖動下方範圍控制選取。</CardDescription></div><Database className="h-5 w-5 text-slate-500" /></CardHeader>
          <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-4">
            <Field label="週期"><select value={interval} onChange={(event) => { setInterval(event.target.value); setBars([]); }} className={inputClass}>{Object.keys(intervalLabels).map((item) => <option key={item} value={item}>{intervalLabels[item]}</option>)}</select></Field>
            <SearchablePicker label="來源" value={selectedSource ? sourceKey(selectedSource) : ""} onChange={setSelectedSourceKey} options={eligibleSources.map((source) => ({ value: sourceKey(source), label: source.instrument.display_name, detail: `${source.instrument.symbol} · ${source.immutable ? `版本 ${source.version_id}` : sourceLabel(source.instrument.data_source)}` }))}/>
            <Field label="查詢起日"><input type="date" value={startDate} onChange={(event) => setStartDate(event.target.value)} className={inputClass} /></Field>
            <Field label="查詢迄日"><input type="date" value={endDate} onChange={(event) => setEndDate(event.target.value)} className={inputClass} /></Field>
          </div>
          <div className="mt-3 flex flex-wrap gap-2"><Button type="button" variant="secondary" loading={loadBars.isPending} disabled={!selectedSource} onClick={() => loadBars.mutate()}>載入 K 線</Button><Button type="button" variant="secondary" disabled={!bars.length} onClick={() => { if (selectedSource) { setCalendarKey(sourceKey(selectedSource)); setOutputStartMs(bars[selectedLow]?.open_time ?? 0); } }}>目前來源作為輸出日曆</Button><Button type="button" disabled={!bars.length} onClick={addSegment}>加入所選片段</Button></div>
          <div className="mt-4"><CandlestickCanvas bars={bars} selectionStart={selectedLow} selectionEnd={selectedHigh} onSelection={(start, end) => { setSelectionStart(start); setSelectionEnd(end); }} /></div>
          {bars.length ? <div className="mt-3 grid gap-2 md:grid-cols-2"><RangeInput label={`起點：${formatMs(bars[selectedLow]?.open_time)}`} value={selectedLow} max={bars.length - 1} onChange={setSelectionStart} /><RangeInput label={`終點：${formatMs(bars[selectedHigh]?.open_time)}`} value={selectedHigh} max={bars.length - 1} onChange={setSelectionEnd} /></div> : <p className="mt-4 text-sm text-slate-500">尚未載入來源 K 線。</p>}
        </Card>

        <Card className="p-5">
          <CardHeader><div><CardTitle>2. 編排片段</CardTitle><CardDescription>可拖曳排序、重複、複製、對半切分或移除。</CardDescription></div><GripVertical className="h-5 w-5 text-slate-500" /></CardHeader>
          <div className="grid max-h-[530px] gap-2 overflow-auto pr-1">
            {segments.map((segment, index) => <div key={segment.item_id} draggable onDragStart={() => setDraggingID(segment.item_id)} onDragOver={(event) => event.preventDefault()} onDrop={() => dropOn(segment.item_id)} className="rounded-lg border border-white/10 bg-slate-950/70 p-3">
              <div className="flex items-start gap-2"><GripVertical className="mt-1 h-4 w-4 shrink-0 cursor-grab text-slate-600" /><div className="min-w-0 flex-1"><div className="text-sm font-semibold text-slate-200">{index + 1}. {segment.sourceName}</div><div className="mt-1 text-xs text-slate-500">{segment.sourceSymbol} · {segment.barTimes.length} 根 · {formatMs(segment.start_time_ms)} → {formatMs(segment.end_time_ms)}</div></div></div>
              <div className="mt-3 flex flex-wrap items-end gap-2"><Field label="重複次數"><input type="number" min={1} max={1000} value={segment.repeat_count} onChange={(event) => updateSegment(segment.item_id, { repeat_count: Math.max(1, Number(event.target.value) || 1) })} className={`${inputClass} w-24`} /></Field><IconButton title="上移" onClick={() => moveSegment(segment.item_id, -1)}><ArrowUp /></IconButton><IconButton title="下移" onClick={() => moveSegment(segment.item_id, 1)}><ArrowDown /></IconButton><IconButton title="複製" onClick={() => setSegments((current) => [...current.slice(0, index + 1), { ...segment, item_id: newItemID(), barTimes: [...segment.barTimes] }, ...current.slice(index + 1)])}><Copy /></IconButton><IconButton title="對半切分" disabled={segment.barTimes.length < 2} onClick={() => splitSegment(segment)}><Scissors /></IconButton><IconButton title="移除" danger onClick={() => setSegments((current) => current.filter((item) => item.item_id !== segment.item_id))}><Trash2 /></IconButton></div>
            </div>)}
            {!segments.length ? <div className="rounded-lg border border-dashed border-white/10 p-8 text-center text-sm text-slate-500">從左側選取並加入至少一個片段。</div> : null}
          </div>
        </Card>
      </div>

      <Card className="p-5">
        <CardHeader><div><CardTitle>3. 預覽與發布</CardTitle><CardDescription>預覽不會發布資料；正式成品須依序啟動三個階段。</CardDescription></div><WandSparkles className="h-5 w-5 text-slate-500" /></CardHeader>
        <div className="grid gap-4 lg:grid-cols-3">
          <div className="grid content-start gap-3 rounded-lg border border-white/10 p-4">
            <div className="text-sm font-semibold text-slate-200">預覽任務</div>
            <InfoRow label="輸出日曆" value={calendarKey ? sources.find((source) => sourceKey(source) === calendarKey)?.instrument.display_name ?? "-" : "未指定"} />
            <InfoRow label="輸出起點" value={formatMs(outputStartMs)} />
            <InfoRow label="估計輸出" value={`${segments.reduce((sum, segment) => sum + segment.barTimes.length * segment.repeat_count, 0).toLocaleString("zh-TW")} 根`} />
            <label className="flex items-center gap-2 text-xs text-slate-400"><input type="checkbox" checked={confirmSoftLimit} onChange={(event) => setConfirmSoftLimit(event.target.checked)} />確認執行超過建議上限的工作量</label>
            <Button type="button" loading={previewMutation.isPending} disabled={!segments.length || !calendarKey || !outputStartMs} onClick={() => previewMutation.mutate()}>建立預覽任務</Button>
            {previewTaskQuery.data ? <TaskRow task={previewTaskQuery.data} onStart={() => startTask.mutate(previewTaskQuery.data.id)} starting={startTask.isPending} /> : null}
          </div>
          <div className="grid content-start gap-3 rounded-lg border border-white/10 p-4">
            <div className="text-sm font-semibold text-slate-200">預覽結果</div>
            {planQuery.data ? <><InfoRow label="內容雜湊" value={shortHash(planQuery.data.content_hash)} /><InfoRow label="片段實例" value={String(planQuery.data.instance_count)} /><InfoRow label="完整 K 線" value={planQuery.data.total_output_bars.toLocaleString("zh-TW")} /><InfoRow label="缺少前錨點" value={`${planQuery.data.anchor_warning_count} 段`} />{previewBarsQuery.data ? <CandlestickCanvas bars={previewBarsQuery.data.rows} selectionStart={-1} selectionEnd={-1} /> : null}</> : <p className="text-sm text-slate-500">啟動並完成預覽任務後顯示。</p>}
          </div>
          <div className="grid content-start gap-3 rounded-lg border border-white/10 p-4">
            <div className="text-sm font-semibold text-slate-200">正式不可變版本</div>
            <Field label="行情系列名稱"><input value={seriesName} onChange={(event) => setSeriesName(event.target.value)} className={inputClass} /></Field>
            <Button type="button" disabled={!planQuery.data || !seriesName.trim()} loading={generationMutation.isPending} onClick={() => generationMutation.mutate()}>建立正式產生流程</Button>
            <div className="grid gap-2">{stageQueries.map((query, index) => query.data ? <TaskRow key={query.data.id} task={query.data} onStart={() => startTask.mutate(query.data!.id)} starting={startTask.isPending} label={`${index + 1}. ${query.data.title}`} /> : null)}</div>
            {generationQuery.data ? <div className={`rounded-lg border p-3 text-sm ${generationQuery.data.published ? "border-[#2dd4bf]/30 bg-[#2dd4bf]/10 text-[#99f6e4]" : "border-white/10 text-slate-400"}`}><div className="font-semibold">{generationQuery.data.series_name} v{generationQuery.data.version_number}</div><div className="mt-1">{generationQuery.data.published ? `已發布為 ${generationQuery.data.output_instrument_id}` : `狀態：${generationQuery.data.status} / ${generationQuery.data.integrity_status}`}</div></div> : null}
          </div>
        </div>
        {editorError ? <div className="mt-4 rounded-md border border-[#f87171]/25 bg-[#f87171]/10 px-3 py-2 text-sm text-[#fecaca]">{editorError.message}</div> : null}
      </Card>
      <Card className="p-5">
        <CardHeader><div><CardTitle>已發布的行情版本</CardTitle><CardDescription>封存只會從新選擇清單隱藏版本；既有研究資料集仍可依版本 ID 與內容雜湊讀取。</CardDescription></div><Database className="h-5 w-5 text-slate-500" /></CardHeader>
        <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-3">
          {(seriesQuery.data?.items ?? []).flatMap((series) => series.versions.map((version) => <div key={version.id} className="rounded-lg border border-white/10 bg-slate-950/60 p-3"><div className="flex items-start justify-between gap-3"><div><div className="text-sm font-semibold text-slate-200">{series.name} v{version.version_number}</div><div className="mt-1 text-xs text-slate-500">{version.instrument_id} · {intervalLabels[version.interval] ?? version.interval} · {version.bar_count.toLocaleString("zh-TW")} 根</div></div><IconButton title="封存版本" danger disabled={archiveVersion.isPending} onClick={() => archiveVersion.mutate(version.id)}><Trash2 /></IconButton></div><div className="mt-3"><InfoRow label="內容雜湊" value={shortHash(version.content_hash)} /><InfoRow label="範圍" value={`${formatMs(version.start_time_ms)} → ${formatMs(version.end_time_ms)}`} /></div></div>))}
          {!seriesQuery.isLoading && !(seriesQuery.data?.items ?? []).some((series) => series.versions.length) ? <div className="text-sm text-slate-500">尚無已發布版本。</div> : null}
        </div>
      </Card>
    </div>
  );
}

function TaskRow({ task, onStart, starting, label }: { task: ComputeTask; onStart: () => void; starting: boolean; label?: string }) {
  const canStart = task.status === "planned" || task.status === "partial" || task.status === "failed" || task.status === "cancelled";
  return <div className="rounded-lg border border-white/10 bg-slate-950/60 p-3"><div className="flex items-center justify-between gap-3"><div className="min-w-0"><div className="truncate text-xs font-medium text-slate-300">{label ?? task.title}</div><div className="mt-1 text-xs text-slate-500">{task.status} · {(task.progress * 100).toFixed(0)}%</div></div>{canStart ? <Button type="button" variant="secondary" icon={Play} loading={starting} onClick={onStart}>啟動</Button> : task.status === "completed" ? <CheckCircle2 className="h-5 w-5 text-[#2dd4bf]" /> : null}</div>{task.error ? <div className="mt-2 text-xs text-[#fecaca]">{task.error}</div> : null}</div>;
}

function CandlestickCanvas({ bars, selectionStart, selectionEnd, onSelection }: { bars: MarketVersionBar[]; selectionStart: number; selectionEnd: number; onSelection?: (start: number, end: number) => void }) {
  const ref = useRef<HTMLCanvasElement>(null);
  const anchor = useRef<number>();
  useEffect(() => {
    const canvas = ref.current;
    if (!canvas) return;
    const ctx = canvas.getContext("2d");
    if (!ctx) return;
    const width = canvas.width, height = canvas.height;
    ctx.clearRect(0, 0, width, height);
    ctx.fillStyle = "#020617"; ctx.fillRect(0, 0, width, height);
    if (!bars.length) { ctx.fillStyle = "#64748b"; ctx.font = "16px sans-serif"; ctx.fillText("尚無 K 線", 24, 40); return; }
    const low = Math.min(...bars.map((bar) => bar.low)), high = Math.max(...bars.map((bar) => bar.high));
    const range = Math.max(high - low, Number.EPSILON), step = width / bars.length;
    if (selectionStart >= 0 && selectionEnd >= selectionStart) { ctx.fillStyle = "rgba(45,212,191,.10)"; ctx.fillRect(selectionStart * step, 0, (selectionEnd - selectionStart + 1) * step, height); }
    bars.forEach((bar, index) => {
      const x = index * step + step / 2;
      const y = (value: number) => 8 + (high - value) / range * (height - 16);
      const rising = bar.close >= bar.open;
      ctx.strokeStyle = rising ? "#2dd4bf" : "#f87171"; ctx.fillStyle = ctx.strokeStyle;
      ctx.beginPath(); ctx.moveTo(x, y(bar.high)); ctx.lineTo(x, y(bar.low)); ctx.stroke();
      const top = Math.min(y(bar.open), y(bar.close)), bodyHeight = Math.max(1, Math.abs(y(bar.open) - y(bar.close)));
      ctx.fillRect(x - Math.max(1, step * .28), top, Math.max(1, step * .56), bodyHeight);
    });
  }, [bars, selectionStart, selectionEnd]);
  return <canvas ref={ref} width={1100} height={270} className={`h-64 w-full rounded-lg border border-white/10 ${onSelection ? "cursor-crosshair" : ""}`} onClick={(event) => {
    if (!onSelection || !bars.length) return;
    const bounds = event.currentTarget.getBoundingClientRect();
    const index = Math.max(0, Math.min(bars.length - 1, Math.floor((event.clientX - bounds.left) / bounds.width * bars.length)));
    if (anchor.current === undefined) { anchor.current = index; onSelection(index, index); } else { onSelection(Math.min(anchor.current, index), Math.max(anchor.current, index)); anchor.current = undefined; }
  }} />;
}

function RangeInput({ label, value, max, onChange }: { label: string; value: number; max: number; onChange: (value: number) => void }) {
  return <label className="grid gap-1 text-xs text-slate-400"><span>{label}</span><input type="range" min={0} max={max} value={value} onChange={(event) => onChange(Number(event.target.value))} className="accent-[#2dd4bf]" /></label>;
}

function IconButton({ title, onClick, disabled, danger, children }: { title: string; onClick: () => void; disabled?: boolean; danger?: boolean; children: React.ReactElement }) {
  return <button type="button" title={title} disabled={disabled} onClick={onClick} className={`grid h-10 w-10 place-items-center rounded-lg border text-slate-400 disabled:opacity-30 ${danger ? "border-[#f87171]/20 hover:bg-[#f87171]/10 hover:text-[#fecaca]" : "border-white/10 hover:bg-white/[0.05] hover:text-slate-100"}`}>{children}</button>;
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return <label className="grid gap-1.5 text-xs"><span className="text-slate-400">{label}</span>{children}</label>;
}

const inputClass = "rounded-lg border border-white/10 bg-slate-950 px-3 py-2 text-sm text-slate-100 outline-none focus:border-[#2dd4bf]/50";

function shortHash(value?: string) {
  if (!value) return "-";
  return value.length > 24 ? `${value.slice(0, 12)}…${value.slice(-10)}` : value;
}

function LeverageGenerator({ instruments }: { instruments: ResearchInstrument[] }) {
  const queryClient = useQueryClient();
  const sources = useMemo(() => instruments.filter((item) => item.data_source !== "fred" && item.data_source !== "generated"), [instruments]);
  const [sourceID, setSourceID] = useState("");
  const source = sources.find((item) => item.id === sourceID) ?? sources[0];
  const [interval, setInterval] = useState("1d");
  const now = new Date();
  const [startDate, setStartDate] = useState("");
  const [endDate, setEndDate] = useState(dateInputValue(now));
  const [multiplier, setMultiplier] = useState(2);
  const [targetID, setTargetID] = useState("");
  const [targetName, setTargetName] = useState("");
  const [result, setResult] = useState<GenerateLeveragedResult>();
  useEffect(() => {
    if (!source) return;
    if (!sourceID) setSourceID(source.id);
    const base = source.id.replace(/[^A-Za-z0-9]/g, "");
    const id = `${base}_${String(multiplier).replace(/[^0-9A-Za-z]/g, "P")}X`.toUpperCase().slice(0, 32);
    setTargetID(id); setTargetName(`${source.display_name} ${multiplier} 倍做多`);
    if (!source.supported_intervals.includes(interval)) setInterval(source.supported_intervals[0] ?? "1d");
  }, [source?.id, multiplier]);
  const sourceDatasetStart = datasetStartDate(source, interval);
  useEffect(() => {
    if (sourceDatasetStart) setStartDate(sourceDatasetStart);
  }, [sourceDatasetStart]);
  const mutation = useMutation({ mutationFn: () => marketDataApi.generateLeveraged({ source_instrument_id: sourceID, source_interval: interval, start_time_ms: dayStartMs(startDate), end_time_ms: dayEndMs(endDate), multiplier, target_instrument_id: targetID, target_symbol: targetID, target_display_name: targetName }), onSuccess: (value) => { setResult(value); queryClient.invalidateQueries({ queryKey: ["market-data-instruments"] }); } });
  function submit(event: FormEvent) { event.preventDefault(); mutation.mutate(); }
  return <form onSubmit={submit} className="grid gap-5 lg:grid-cols-2"><Card className="p-5"><CardHeader><div><CardTitle>來源與倍數</CardTitle><CardDescription>依既有每日倍數做多規則產生資料。</CardDescription></div></CardHeader><div className="grid gap-3 md:grid-cols-2"><Field label="來源"><select value={sourceID} onChange={(event) => setSourceID(event.target.value)} className={inputClass}>{sources.map((item) => <option key={item.id} value={item.id}>{item.display_name}</option>)}</select></Field><Field label="週期"><select value={interval} onChange={(event) => setInterval(event.target.value)} className={inputClass}>{(source?.supported_intervals ?? ["1d"]).map((item) => <option key={item} value={item}>{intervalLabels[item] ?? item}</option>)}</select></Field><Field label="起日"><input type="date" value={startDate} onChange={(event) => setStartDate(event.target.value)} className={inputClass} /></Field><Field label="迄日"><input type="date" value={endDate} onChange={(event) => setEndDate(event.target.value)} className={inputClass} /></Field><Field label="倍數"><input type="number" min="0.01" step="0.01" value={multiplier} onChange={(event) => setMultiplier(Number(event.target.value))} className={inputClass} /></Field></div></Card><Card className="p-5"><CardHeader><div><CardTitle>輸出資料</CardTitle><CardDescription>此舊模式仍以標的 ID 覆寫同一份產生資料。</CardDescription></div></CardHeader><div className="grid gap-3"><Field label="標的 ID / 代號"><input value={targetID} maxLength={32} onChange={(event) => setTargetID(event.target.value.toUpperCase())} className={inputClass} /></Field><Field label="顯示名稱"><input value={targetName} onChange={(event) => setTargetName(event.target.value)} className={inputClass} /></Field><Button type="submit" loading={mutation.isPending} disabled={!sourceID || !targetID || multiplier <= 0}>產生每日倍數行情</Button>{mutation.error instanceof Error ? <div className="text-sm text-[#fecaca]">{mutation.error.message}</div> : null}{result ? <div className="rounded-lg border border-[#2dd4bf]/25 bg-[#2dd4bf]/10 p-3 text-sm text-[#99f6e4]">已產生 {result.generated_bars.toLocaleString("zh-TW")} 根：{result.instrument.display_name}</div> : null}</div></Card></form>;
}

function InfoRow({ label, value }: { label: string; value: string }) {
  return <div className="flex items-center justify-between gap-3 text-xs"><span className="text-slate-500">{label}</span><span className="max-w-[65%] truncate text-right font-mono text-slate-200" title={value}>{value}</span></div>;
}
