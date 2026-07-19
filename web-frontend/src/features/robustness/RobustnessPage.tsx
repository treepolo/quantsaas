import { useEffect, useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { AlertTriangle, Box, Check, FlaskConical, Grid3X3, Pause, Play, RefreshCw, RotateCcw, X } from "lucide-react";
import { Bar, BarChart, CartesianGrid, Cell, Line, LineChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";
import { evolutionApi } from "../../shared/services/evolution";
import { marketDataApi } from "../../shared/services/marketData";
import { datasetStartDate } from "../../shared/lib/datasetDates";
import { computeTasksApi, type ComputeTask } from "../../shared/services/computeTasks";
import {
  robustnessApi,
  type CreateRobustnessStudy,
  type EvaluationPoint,
  type ParameterDefinition,
  type RobustnessMetric,
  type RobustnessStudy
} from "../../shared/services/robustness";
import { Button } from "../../shared/ui/Button";
import { Card, CardDescription, CardHeader, CardTitle } from "../../shared/ui/Card";
import { ComputePlanSummary, ComputeProgress, ComputeStatusBadge } from "../computetasks/ComputeTaskComponents";

const metricLabels: Record<RobustnessMetric, string> = {
  log_final_nav_ratio: "log 期末淨值比",
  drawdown_residual_ratio: "最大回撤殘值比",
  log_drawdown_residual_ratio: "log 最大回撤殘值比",
  performance_drawdown_composite: "績效回撤綜合指標",
  qualification: "合格狀態"
};

const modeLabels = {
  one_dimensional: "一維曲線",
  two_dimensional: "二維格點",
  multidimensional: "多維抽樣",
  imported_evaluations: "既有評估點"
} as const;

const inputClass = "min-h-10 w-full rounded-lg border border-white/10 bg-slate-950/70 px-3 text-sm text-slate-200 outline-none focus:border-teal-400/50";
const activeStatuses = new Set(["queued", "running", "partial"]);

function dayStart(value: string) {
  return value ? new Date(`${value}T00:00:00`).getTime() : 0;
}

function dayEnd(value: string) {
  return value ? new Date(`${value}T23:59:59.999`).getTime() : 0;
}

function dateValue(date: Date) {
  return date.toISOString().slice(0, 10);
}

function metricValue(point: EvaluationPoint, metric: RobustnessMetric) {
  if (!point.metrics) return undefined;
  if (metric === "qualification") return point.metrics.qualified ? 1 : 0;
  return point.metrics[metric];
}

function formatMetric(value: number | undefined) {
  if (value === undefined || !Number.isFinite(value)) return "未知";
  return Math.abs(value) >= 100 ? value.toFixed(1) : value.toFixed(4);
}

function pointTone(point: EvaluationPoint) {
  if (point.kind === "predicted") return "#a78bfa";
  if (point.kind === "proposed") return "#f59e0b";
  if (point.state === "qualified") return "#2dd4bf";
  if (point.state === "unqualified") return "#fb7185";
  return "#64748b";
}

function usePageVisible() {
  const [visible, setVisible] = useState(() => document.visibilityState === "visible");
  useEffect(() => {
    const update = () => setVisible(document.visibilityState === "visible");
    document.addEventListener("visibilitychange", update);
    return () => document.removeEventListener("visibilitychange", update);
  }, []);
  return visible;
}

export function RobustnessPage() {
  const queryClient = useQueryClient();
  const pageVisible = usePageVisible();
  const genomesQuery = useQuery({ queryKey: ["genomes"], queryFn: evolutionApi.listGenomes });
  const instrumentsQuery = useQuery({ queryKey: ["market-data-instruments"], queryFn: marketDataApi.instruments });
  const studiesQuery = useQuery({ queryKey: ["robustness-studies"], queryFn: () => robustnessApi.list(80) });
  const genomes = genomesQuery.data ?? [];
  const instruments = instrumentsQuery.data?.instruments ?? [];
  const [genomeId, setGenomeId] = useState(0);
  const [instrumentId, setInstrumentId] = useState("");
  const [mode, setMode] = useState<CreateRobustnessStudy["mode"]>("one_dimensional");
  const [axes, setAxes] = useState<string[]>([]);
  const [radius, setRadius] = useState(3);
  const [sampleCount, setSampleCount] = useState(48);
  const [radiiText, setRadiiText] = useState("1,2,3,5,8,13");
  const [interval, setInterval] = useState("1d");
  const [executionMode, setExecutionMode] = useState("close_next_open");
  const [startDate, setStartDate] = useState("");
  const [endDate, setEndDate] = useState(() => dateValue(new Date(Date.now() - 86_400_000)));
  const [selectedStudyId, setSelectedStudyId] = useState(0);
  const [taskId, setTaskId] = useState(0);
  const [confirmSoftLimit, setConfirmSoftLimit] = useState(false);
  const [zMetric, setZMetric] = useState<RobustnessMetric>("log_final_nav_ratio");
  const [analysisMetric, setAnalysisMetric] = useState<RobustnessMetric>("log_final_nav_ratio");
  const [view3D, setView3D] = useState(false);
  const [previewSignature, setPreviewSignature] = useState("");
  const [overlays, setOverlays] = useState({ nav: true, drawdown: true, composite: false, qualification: true, centers: true });

  useEffect(() => {
    if (!genomeId && genomes.length) setGenomeId(genomes[0].id);
  }, [genomeId, genomes]);
  useEffect(() => {
    if (!instrumentId && instruments.length) {
      setInstrumentId(instruments[0].id);
      setInterval(instruments[0].supported_intervals[0] ?? "1d");
    }
  }, [instrumentId, instruments]);
  const selectedInstrument = instruments.find((item) => item.id === instrumentId);
  const selectedDatasetStart = datasetStartDate(selectedInstrument, interval);
  useEffect(() => {
    if (selectedDatasetStart) setStartDate(selectedDatasetStart);
  }, [selectedDatasetStart]);

  const parametersQuery = useQuery({
    queryKey: ["robustness-parameters", genomeId],
    queryFn: () => robustnessApi.parameters(genomeId),
    enabled: genomeId > 0
  });
  const definitions = (parametersQuery.data?.definitions ?? []).filter((item) => item.active);
  useEffect(() => {
    if (!definitions.length) return;
    const wanted = mode === "one_dimensional" ? 1 : Math.min(mode === "two_dimensional" ? 2 : 4, definitions.length);
    setAxes((current) => {
      const valid = current.filter((name) => definitions.some((item) => item.name === name));
      return valid.length === wanted ? valid : definitions.slice(0, wanted).map((item) => item.name);
    });
  }, [mode, parametersQuery.data]);

  const radii = useMemo(
    () => [...new Set(radiiText.split(",").map(Number).filter((value) => Number.isInteger(value) && value > 0 && value <= 100))].sort((a, b) => a - b),
    [radiiText]
  );
  const customSteps = useMemo(
    () => Object.fromEntries(axes.map((name) => [name, definitions.find((item) => item.name === name)?.default_step ?? 0.05])),
    [axes, definitions]
  );
  const request: CreateRobustnessStudy = {
    name: `${modeLabels[mode]} · ${genomes.find((item) => item.id === genomeId)?.name || `參數 #${genomeId}`}`,
    mode,
    genome_id: genomeId,
    axes,
    radius,
    radii,
    metric: analysisMetric,
    custom_steps: customSteps,
    sample_count: mode === "multidimensional" ? sampleCount : undefined,
    sample_offset: 0,
    confirm_soft_limit: confirmSoftLimit,
    backtest: {
      instrument_id: instrumentId,
      data_source: selectedInstrument?.data_source,
      symbol: selectedInstrument?.symbol ?? instrumentId,
      interval,
      execution_mode: executionMode,
      start_time_ms: dayStart(startDate),
      end_time_ms: dayEnd(endDate),
      long_term_filter_enabled: interval === "1d",
      long_term_filter_months: 10
    }
  };
  const requestSignature = JSON.stringify(request);
  const axisCountValid = mode === "one_dimensional" ? axes.length === 1 : mode === "two_dimensional" ? axes.length === 2 : axes.length >= 2;
  const formValid = genomeId > 0 && instrumentId !== "" && axisCountValid && radii.length > 0 && request.backtest.end_time_ms > request.backtest.start_time_ms;

  const previewMutation = useMutation({
    mutationFn: (input: CreateRobustnessStudy) => robustnessApi.preview(input),
    onSuccess: (value, input) => {
      setPreviewSignature(JSON.stringify(input));
      setConfirmSoftLimit(!value.requires_confirmation);
    }
  });
  const createMutation = useMutation({
    mutationFn: () => robustnessApi.create(request),
    onSuccess: (value) => {
      setSelectedStudyId(value.study.id);
      setTaskId(value.task?.id ?? value.study.compute_task_id ?? 0);
      queryClient.invalidateQueries({ queryKey: ["robustness-studies"] });
    }
  });
  const taskQuery = useQuery({
    queryKey: ["compute-task", taskId],
    queryFn: () => computeTasksApi.get(taskId),
    enabled: taskId > 0,
    refetchInterval: (query) => pageVisible && activeStatuses.has((query.state.data as ComputeTask | undefined)?.status ?? "") ? 1200 : false
  });
  const selectedStudyQuery = useQuery({
    queryKey: ["robustness-study", selectedStudyId],
    queryFn: () => robustnessApi.get(selectedStudyId),
    enabled: selectedStudyId > 0,
    refetchInterval: pageVisible && taskQuery.data && activeStatuses.has(taskQuery.data.status) ? 1500 : false
  });
  const study = selectedStudyQuery.data;
  useEffect(() => {
    if (study?.compute_task_id && study.compute_task_id !== taskId) setTaskId(study.compute_task_id);
  }, [study?.compute_task_id, taskId]);
  useEffect(() => {
    if (taskQuery.data?.status === "completed" && selectedStudyId) {
      queryClient.invalidateQueries({ queryKey: ["robustness-study", selectedStudyId] });
      queryClient.invalidateQueries({ queryKey: ["robustness-studies"] });
    }
  }, [taskQuery.data?.status, selectedStudyId, queryClient]);
  useEffect(() => {
    if (study?.status === "completed") queryClient.invalidateQueries({ queryKey: ["robustness-studies"] });
  }, [study?.status, queryClient]);

  const taskAction = useMutation({
    mutationFn: ({ action, id }: { action: "start" | "cancel" | "retry"; id: number }) => computeTasksApi[action](id),
    onSuccess: (value) => {
      queryClient.setQueryData(["compute-task", value.id], value);
      if (selectedStudyId) queryClient.invalidateQueries({ queryKey: ["robustness-study", selectedStudyId] });
    }
  });
  const analyzeMutation = useMutation({
    mutationFn: () => robustnessApi.analyze(selectedStudyId, analysisMetric, radii),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["robustness-study", selectedStudyId] })
  });

  return (
    <section className="space-y-5">
      <header className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h1 className="flex items-center gap-2 text-2xl font-bold text-slate-100"><Grid3X3 className="h-6 w-6 text-teal-300" />參數穩健區域</h1>
          <p className="mt-1 max-w-3xl text-sm leading-6 text-slate-400">以實際回測格點檢查參數附近是否仍然合格。預測與提案點只作提示，不會進入正式區域、邊界或中心判定。</p>
        </div>
        {!pageVisible && <span className="flex items-center gap-1 rounded-full border border-amber-500/20 bg-amber-500/10 px-3 py-1 text-xs text-amber-300"><Pause className="h-3 w-3" />背景頁面已停止輪詢</span>}
      </header>

      <div className="grid gap-5 xl:grid-cols-[minmax(0,1.25fr)_minmax(320px,.75fr)]">
        <Card>
          <CardHeader><div><CardTitle>建立研究</CardTitle><CardDescription>先預覽固定計算清單，再由你明確啟動；切換圖表與疊圖不會重跑回測。</CardDescription></div></CardHeader>
          <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
            <Field label="來源參數">
              <select className={inputClass} value={genomeId} onChange={(event) => setGenomeId(Number(event.target.value))}>
                <option value={0}>選擇參數</option>
                {genomes.map((item) => <option key={item.id} value={item.id}>#{item.id} · {item.name || item.role}</option>)}
              </select>
            </Field>
            <Field label="研究模式">
              <select className={inputClass} value={mode} onChange={(event) => setMode(event.target.value as CreateRobustnessStudy["mode"])}>
                <option value="one_dimensional">一維曲線</option><option value="two_dimensional">二維 heatmap / 3D</option><option value="multidimensional">多維稀疏抽樣</option>
              </select>
            </Field>
            <Field label="研究半徑（格）"><input className={inputClass} type="number" min={1} max={100} value={radius} onChange={(event) => setRadius(Number(event.target.value))} /></Field>
            <Field label="標的">
              <select className={inputClass} value={instrumentId} onChange={(event) => { const value = event.target.value; setInstrumentId(value); setInterval(instruments.find((item) => item.id === value)?.supported_intervals[0] ?? "1d"); }}>
                {instruments.map((item) => <option key={item.id} value={item.id}>{item.display_name} · {item.id}</option>)}
              </select>
            </Field>
            <Field label="週期">
              <select className={inputClass} value={interval} onChange={(event) => setInterval(event.target.value)}>{(selectedInstrument?.supported_intervals ?? ["1d"]).map((item) => <option key={item}>{item}</option>)}</select>
            </Field>
            <Field label="執行時點">
              <select className={inputClass} value={executionMode} onChange={(event) => setExecutionMode(event.target.value)}><option value="close_next_open">收盤判斷、次日開盤執行</option><option value="close_same_close">收盤判斷、同日收盤執行</option></select>
            </Field>
            <Field label="開始日期"><input className={inputClass} type="date" value={startDate} onChange={(event) => setStartDate(event.target.value)} /></Field>
            <Field label="結束日期"><input className={inputClass} type="date" value={endDate} onChange={(event) => setEndDate(event.target.value)} /></Field>
            <Field label="多尺度半徑"><input className={inputClass} value={radiiText} onChange={(event) => setRadiiText(event.target.value)} placeholder="1,2,3,5,8,13" /></Field>
            {mode === "multidimensional" && <Field label="抽樣點數"><input className={inputClass} type="number" min={1} value={sampleCount} onChange={(event) => setSampleCount(Number(event.target.value))} /></Field>}
          </div>
          <div className="mt-4">
            <div className="mb-2 text-xs font-semibold uppercase tracking-wider text-slate-500">研究變數</div>
            <div className="grid gap-2 sm:grid-cols-2 lg:grid-cols-3">
              {definitions.map((definition) => <AxisToggle key={definition.name} definition={definition} selected={axes.includes(definition.name)} mode={mode} onChange={() => setAxes((current) => toggleAxis(current, definition.name, mode))} />)}
            </div>
          </div>
          <div className="mt-5 flex flex-wrap gap-2">
            <Button variant="secondary" icon={FlaskConical} loading={previewMutation.isPending} disabled={!formValid} onClick={() => previewMutation.mutate(request)}>預覽計算量</Button>
            <Button icon={Check} loading={createMutation.isPending} disabled={!formValid || !previewMutation.data || previewSignature !== requestSignature || (previewMutation.data.requires_confirmation && !confirmSoftLimit)} onClick={() => createMutation.mutate()}>建立固定任務</Button>
          </div>
          {previewMutation.error && <ErrorText error={previewMutation.error} />}
          {createMutation.error && <ErrorText error={createMutation.error} />}
          {previewMutation.data && previewSignature === requestSignature && <div className="mt-4 space-y-3"><ComputePlanSummary preview={previewMutation.data} />{previewMutation.data.requires_confirmation && <label className="flex items-start gap-2 rounded-lg border border-amber-500/20 bg-amber-500/5 p-3 text-sm text-amber-200"><input className="mt-1" type="checkbox" checked={confirmSoftLimit} onChange={(event) => setConfirmSoftLimit(event.target.checked)} />我已確認本次新增計算量超過建議上限，仍要建立任務。</label>}</div>}
        </Card>

        <Card>
          <CardHeader><div><CardTitle>既有研究</CardTitle><CardDescription>研究設定與分析快照不可變；補算會形成新的評估點集合與分析版本。</CardDescription></div></CardHeader>
          <div className="max-h-[590px] space-y-2 overflow-auto pr-1">
            {(studiesQuery.data ?? []).map((item) => (
              <button key={item.id} className={`w-full rounded-xl border p-3 text-left transition ${selectedStudyId === item.id ? "border-teal-400/35 bg-teal-400/[0.08]" : "border-white/[0.06] bg-white/[0.025] hover:bg-white/[0.05]"}`} onClick={() => { setSelectedStudyId(item.id); setTaskId(item.compute_task_id ?? 0); }}>
                <div className="flex items-start justify-between gap-2"><span className="font-medium text-slate-200">{item.name}</span><span className="text-xs text-slate-500">#{item.id}</span></div>
                <div className="mt-2 flex flex-wrap gap-2 text-xs text-slate-500"><span>{modeLabels[item.mode]}</span><span>{item.actual_point_count}/{item.expected_point_count} 實測</span>{item.predicted_point_count > 0 && <span className="text-violet-300">{item.predicted_point_count} 提示</span>}<span>{item.status}</span></div>
              </button>
            ))}
            {!studiesQuery.isLoading && !studiesQuery.data?.length && <div className="rounded-xl border border-dashed border-white/10 p-8 text-center text-sm text-slate-500">尚無研究</div>}
          </div>
        </Card>
      </div>

      {taskQuery.data && <TaskPanel task={taskQuery.data} pending={taskAction.isPending} onAction={(action) => taskAction.mutate({ action, id: taskQuery.data.id })} />}
      {study && <StudyPanel study={study} zMetric={zMetric} setZMetric={setZMetric} analysisMetric={analysisMetric} setAnalysisMetric={setAnalysisMetric} radii={radii} overlays={overlays} setOverlays={setOverlays} view3D={view3D} setView3D={setView3D} analyzing={analyzeMutation.isPending} analyze={() => analyzeMutation.mutate()} pageVisible={pageVisible} />}
    </section>
  );
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return <label className="space-y-1.5"><span className="text-xs font-medium text-slate-400">{label}</span>{children}</label>;
}

function AxisToggle({ definition, selected, mode, onChange }: { definition: ParameterDefinition; selected: boolean; mode: CreateRobustnessStudy["mode"]; onChange: () => void }) {
  return <button type="button" onClick={onChange} className={`rounded-lg border p-3 text-left ${selected ? "border-teal-400/30 bg-teal-400/[0.08]" : "border-white/[0.06] bg-white/[0.02]"}`}><div className="flex items-center justify-between gap-2 text-sm text-slate-200"><span>{definition.label}</span>{selected && <Check className="h-4 w-4 text-teal-300" />}</div><div className="mt-1 text-[11px] text-slate-500">步長 {definition.default_step} · {definition.legal_min}～{definition.legal_max}{mode !== "multidimensional" && selected ? " · 圖表軸" : ""}</div></button>;
}

function toggleAxis(current: string[], name: string, mode: CreateRobustnessStudy["mode"]): string[] {
  if (current.includes(name)) return mode === "multidimensional" && current.length > 2 ? current.filter((item) => item !== name) : current;
  const limit = mode === "one_dimensional" ? 1 : mode === "two_dimensional" ? 2 : 12;
  return [...current, name].slice(-limit);
}

function ErrorText({ error }: { error: Error }) {
  return <div className="mt-3 flex items-start gap-2 rounded-lg border border-rose-500/20 bg-rose-500/10 p-3 text-sm text-rose-200"><AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" />{error.message}</div>;
}

function TaskPanel({ task, pending, onAction }: { task: ComputeTask; pending: boolean; onAction: (action: "start" | "cancel" | "retry") => void }) {
  return <Card><CardHeader><div><CardTitle>計算任務 #{task.id}</CardTitle><CardDescription>固定 {task.total_items.toLocaleString()} 個格點；快取命中不重算，取消後保留已完成結果。</CardDescription></div><ComputeStatusBadge status={task.status} /></CardHeader><ComputeProgress task={task} /><div className="mt-4 flex flex-wrap gap-2">{task.status === "planned" && <Button icon={Play} loading={pending} onClick={() => onAction("start")}>明確啟動</Button>}{activeStatuses.has(task.status) && <Button variant="danger" icon={X} loading={pending} onClick={() => onAction("cancel")}>取消</Button>}{["failed", "cancelled", "partial"].includes(task.status) && <Button variant="secondary" icon={RotateCcw} loading={pending} onClick={() => onAction("retry")}>重試未完成項目</Button>}</div>{task.error && <ErrorText error={new Error(task.error)} />}</Card>;
}

function StudyPanel(props: {
  study: RobustnessStudy;
  zMetric: RobustnessMetric;
  setZMetric: (value: RobustnessMetric) => void;
  analysisMetric: RobustnessMetric;
  setAnalysisMetric: (value: RobustnessMetric) => void;
  radii: number[];
  overlays: Record<string, boolean>;
  setOverlays: React.Dispatch<React.SetStateAction<{ nav: boolean; drawdown: boolean; composite: boolean; qualification: boolean; centers: boolean }>>;
  view3D: boolean;
  setView3D: (value: boolean) => void;
  analyzing: boolean;
  analyze: () => void;
  pageVisible: boolean;
}) {
  const { study, zMetric, setZMetric, analysisMetric, setAnalysisMetric, overlays, setOverlays, view3D, setView3D, analyzing, analyze, pageVisible } = props;
  const points = study.points ?? [];
  const actual = points.filter((point) => point.kind === "actual");
  const hints = points.filter((point) => point.kind !== "actual");
  const analysis = study.latest_analysis?.result;
  return <div className="space-y-5">
    <Card>
      <CardHeader><div><CardTitle>{study.name}</CardTitle><CardDescription>實測 {actual.length} · 預測／提案 {hints.length} · 缺漏 {analysis?.missing_coordinates.length ?? "尚未分析"}</CardDescription></div><div className="flex flex-wrap gap-2"><select className={inputClass} value={zMetric} onChange={(event) => setZMetric(event.target.value as RobustnessMetric)}>{Object.entries(metricLabels).map(([value, label]) => <option key={value} value={value}>圖表 Z：{label}</option>)}</select></div></CardHeader>
      <div className="mb-4 flex flex-wrap items-center gap-3 text-xs text-slate-400"><Legend color="#2dd4bf" label="實測合格" /><Legend color="#fb7185" label="實測不合格" /><Legend color="#64748b" label="未知" /><Legend color="#a78bfa" label="預測（非證據）" /><Legend color="#f59e0b" label="提案（非證據）" /></div>
      {study.mode === "one_dimensional" && <OneDimensionalView study={study} metric={zMetric} />}
      {study.mode === "two_dimensional" && <TwoDimensionalView study={study} metric={zMetric} overlays={overlays} setOverlays={setOverlays} view3D={view3D} setView3D={setView3D} renderSurface={pageVisible} centerIDs={analysis?.regions.flatMap((region) => region.center_ids) ?? []} />}
      {(study.mode === "multidimensional" || study.mode === "imported_evaluations") && <MultidimensionalView points={points} metric={zMetric} />}
    </Card>

    <Card>
      <CardHeader><div><CardTitle>正式區域分析</CardTitle><CardDescription>分析只讀取現有實測點並建立不可變快照；圖表 Z 軸切換不會觸發此操作。</CardDescription></div></CardHeader>
      <div className="flex flex-wrap items-end gap-3"><Field label="多尺度統計指標"><select className={`${inputClass} min-w-60`} value={analysisMetric} onChange={(event) => setAnalysisMetric(event.target.value as RobustnessMetric)}>{Object.entries(metricLabels).map(([value, label]) => <option key={value} value={value}>{label}</option>)}</select></Field><Button icon={RefreshCw} loading={analyzing} disabled={actual.length === 0} onClick={analyze}>建立分析快照</Button>{study.latest_analysis && <span className="pb-2 text-xs text-slate-500">快照 #{study.latest_analysis.id} · {study.latest_analysis.content_hash.slice(0, 12)}</span>}</div>
      {analysis ? <AnalysisView study={study} /> : <div className="mt-5 rounded-xl border border-dashed border-white/10 p-8 text-center text-sm text-slate-500">計算完成後建立分析快照，即可查看多尺度衰退、連通區域、正式前緣與穩健中心。</div>}
    </Card>
  </div>;
}

function Legend({ color, label }: { color: string; label: string }) { return <span className="flex items-center gap-1.5"><i className="h-2.5 w-2.5 rounded-full" style={{ background: color }} />{label}</span>; }

function OneDimensionalView({ study, metric }: { study: RobustnessStudy; metric: RobustnessMetric }) {
  const axis = study.parameter_space.axes[0];
  const data = (study.points ?? []).map((point) => ({ ...point, x: point.parameters[axis.name], value: metricValue(point, metric) })).sort((a, b) => a.x - b.x);
  return <div className="h-80"><ResponsiveContainer width="100%" height="100%"><LineChart data={data} margin={{ top: 20, right: 20, bottom: 20, left: 10 }}><CartesianGrid stroke="rgba(148,163,184,.09)" /><XAxis dataKey="x" stroke="#64748b" label={{ value: axis.label, position: "insideBottom", offset: -10, fill: "#64748b" }} /><YAxis stroke="#64748b" /><Tooltip contentStyle={{ background: "#0f172a", border: "1px solid rgba(255,255,255,.1)" }} formatter={(value) => [formatMetric(Number(value)), metricLabels[metric]]} /><Line type="monotone" dataKey="value" stroke="#64748b" strokeWidth={1.5} dot={(dot) => { const point = data[dot.index]; return <circle key={point.id} cx={dot.cx} cy={dot.cy} r={point.id === study.center_point_key ? 6 : 4} fill={pointTone(point)} stroke={point.id === study.center_point_key ? "#fff" : "none"} />; }} connectNulls={false} isAnimationActive={false} /></LineChart></ResponsiveContainer></div>;
}

function TwoDimensionalView({ study, metric, overlays, setOverlays, view3D, setView3D, renderSurface, centerIDs }: { study: RobustnessStudy; metric: RobustnessMetric; overlays: Record<string, boolean>; setOverlays: React.Dispatch<React.SetStateAction<any>>; view3D: boolean; setView3D: (value: boolean) => void; renderSurface: boolean; centerIDs: string[] }) {
  const [xAxis, yAxis] = study.parameter_space.axes;
  const points = study.points ?? [];
  const values = points.map((point) => metricValue(point, metric)).filter((value): value is number => value !== undefined);
  const min = Math.min(...values, 0), max = Math.max(...values, 1);
  const byCoordinate = new Map(points.map((point) => [point.coordinates.join(":"), point]));
  return <div className="space-y-4">
    <div className="flex flex-wrap gap-2">{(["nav", "drawdown", "composite", "qualification", "centers"] as const).map((key) => <label key={key} className="flex items-center gap-1.5 rounded-lg border border-white/[0.06] bg-white/[0.025] px-3 py-2 text-xs text-slate-400"><input type="checkbox" checked={Boolean(overlays[key])} onChange={(event) => setOverlays((current: any) => ({ ...current, [key]: event.target.checked }))} />{{ nav: "期末淨值", drawdown: "回撤殘值", composite: "綜合指標", qualification: "合格框", centers: "中心" }[key]}</label>)}<Button variant="secondary" icon={Box} onClick={() => setView3D(!view3D)}>{view3D ? "顯示 heatmap" : "顯示同資料 3D"}</Button></div>
    {view3D ? renderSurface ? <SurfaceView points={points} metric={metric} /> : <div className="rounded-xl border border-dashed border-white/10 p-12 text-center text-sm text-slate-500">背景頁面暫停 3D 繪製</div> : <div className="overflow-auto"><div className="grid min-w-[520px] gap-1" style={{ gridTemplateColumns: `90px repeat(${xAxis.values.length}, minmax(58px, 1fr))` }}><div />{xAxis.values.map((value) => <div key={`x-${value}`} className="text-center text-[10px] text-slate-500">{value}</div>)}{[...yAxis.values].reverse().map((yValue, reverseY) => { const y = yAxis.values.length - 1 - reverseY; return [<div key={`yl-${y}`} className="flex items-center text-xs text-slate-500">{yValue}</div>, ...xAxis.values.map((_, x) => { const point = byCoordinate.get(`${x}:${y}`); const value = point ? metricValue(point, metric) : undefined; const ratio = value === undefined ? 0 : (value - min) / Math.max(1e-12, max - min); const center = point && (point.id === study.center_point_key || centerIDs.includes(point.id)); return <div key={`${x}:${y}`} title={point ? `${xAxis.label} ${point.parameters[xAxis.name]} · ${yAxis.label} ${point.parameters[yAxis.name]}` : "未計算"} className={`min-h-16 rounded-md border p-1 text-center text-[10px] ${point?.state === "unqualified" && overlays.qualification ? "border-rose-400" : point?.state === "qualified" && overlays.qualification ? "border-teal-300/50" : "border-white/[0.04]"}`} style={{ background: value === undefined ? "rgba(51,65,85,.28)" : `rgba(45,212,191,${0.08 + ratio * 0.62})`, outline: center && overlays.centers ? "2px solid #fbbf24" : "none" }}><div className="font-semibold text-slate-100">{formatMetric(value)}</div>{point && <div className="mt-1 space-y-0.5 text-slate-400">{overlays.nav && <div>N {formatMetric(point.metrics?.log_final_nav_ratio)}</div>}{overlays.drawdown && <div>D {formatMetric(point.metrics?.log_drawdown_residual_ratio)}</div>}{overlays.composite && <div>C {formatMetric(point.metrics?.performance_drawdown_composite)}</div>}</div>}</div>; })]; })}</div><div className="mt-2 text-center text-xs text-slate-500">{xAxis.label}（橫） · {yAxis.label}（縱）</div></div>}
  </div>;
}

function SurfaceView({ points, metric }: { points: EvaluationPoint[]; metric: RobustnessMetric }) {
  const actual = points.filter((point) => point.coordinates.length >= 2 && metricValue(point, metric) !== undefined);
  const values = actual.map((point) => metricValue(point, metric) as number); const min = Math.min(...values, 0), max = Math.max(...values, 1);
  const xMax = Math.max(...actual.map((point) => point.coordinates[0]), 1), yMax = Math.max(...actual.map((point) => point.coordinates[1]), 1);
  const projected = actual.map((point) => { const z = ((metricValue(point, metric) ?? min) - min) / Math.max(1e-12, max - min); const x = 80 + point.coordinates[0] / xMax * 360 + point.coordinates[1] / yMax * 90; const y = 270 - point.coordinates[1] / yMax * 80 - z * 170; return { point, x, y, z }; }).sort((a, b) => a.point.coordinates[1] - b.point.coordinates[1] || a.point.coordinates[0] - b.point.coordinates[0]);
  return <svg viewBox="0 0 560 310" className="h-auto w-full rounded-xl border border-white/[0.06] bg-slate-950/50" role="img" aria-label="使用 heatmap 相同評估點繪製的三維曲面"><path d="M80 270 L440 270 L530 190 L170 190 Z" fill="rgba(15,23,42,.8)" stroke="rgba(148,163,184,.2)" />{projected.map(({ point, x, y, z }) => <g key={point.id}><line x1={x} x2={x} y1={270 - point.coordinates[1] / yMax * 80} y2={y} stroke="rgba(148,163,184,.14)" /><circle cx={x} cy={y} r={5} fill={pointTone(point)} opacity={0.55 + z * 0.45} /><title>{formatMetric(metricValue(point, metric))}</title></g>)}</svg>;
}

function MultidimensionalView({ points, metric }: { points: EvaluationPoint[]; metric: RobustnessMetric }) {
  const actual = points.filter((point) => point.kind === "actual" && point.metrics); const values = actual.map((point) => metricValue(point, metric) as number); const min = Math.min(...values, 0), max = Math.max(...values, 1); const bins = Array.from({ length: 12 }, (_, index) => ({ label: `${index + 1}`, count: 0, qualified: 0 })); values.forEach((value, index) => { const bin = Math.min(11, Math.max(0, Math.floor((value - min) / Math.max(1e-12, max - min) * 12))); bins[bin].count++; if (actual[index].state === "qualified") bins[bin].qualified++; });
  return <div className="grid gap-4 lg:grid-cols-[2fr_1fr]"><div className="h-72"><ResponsiveContainer width="100%" height="100%"><BarChart data={bins}><CartesianGrid stroke="rgba(148,163,184,.09)" /><XAxis dataKey="label" stroke="#64748b" /><YAxis stroke="#64748b" /><Tooltip contentStyle={{ background: "#0f172a", border: "1px solid rgba(255,255,255,.1)" }} /><Bar dataKey="count" name="實測點" isAnimationActive={false}>{bins.map((_, index) => <Cell key={index} fill={`rgba(45,212,191,${0.25 + index / 18})`} />)}</Bar></BarChart></ResponsiveContainer></div><div className="grid content-center gap-3"><MetricCard label="實測點" value={actual.length} /><MetricCard label="合格點" value={actual.filter((point) => point.state === "qualified").length} tone="text-teal-300" /><MetricCard label="非證據提示" value={points.length - actual.length} tone="text-violet-300" /></div></div>;
}

function AnalysisView({ study }: { study: RobustnessStudy }) {
  const analysis = study.latest_analysis!.result;
  const centers = analysis.regions.flatMap((region) => region.center_ids);
  const frontier = analysis.regions.flatMap((region) => region.frontier_ids);
  return <div className="mt-5 space-y-5"><div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4"><MetricCard label="合格連通區域" value={analysis.regions.length} /><MetricCard label="候選前緣點" value={frontier.length} /><MetricCard label="穩健中心提案" value={centers.length} tone="text-amber-300" /><MetricCard label="未知格點" value={analysis.missing_coordinates.length} tone="text-slate-300" /></div><div className="h-72"><ResponsiveContainer width="100%" height="100%"><LineChart data={analysis.scales}><CartesianGrid stroke="rgba(148,163,184,.09)" /><XAxis dataKey="radius" stroke="#64748b" /><YAxis yAxisId="ratio" domain={[0, 1]} stroke="#64748b" /><YAxis yAxisId="dispersion" orientation="right" stroke="#64748b" /><Tooltip contentStyle={{ background: "#0f172a", border: "1px solid rgba(255,255,255,.1)" }} /><Line yAxisId="ratio" dataKey="qualification_ratio" name="鄰域合格率" stroke="#2dd4bf" strokeWidth={2} dot isAnimationActive={false} /><Line yAxisId="dispersion" dataKey="standard_deviation" name="績效離散" stroke="#f59e0b" strokeWidth={2} dot isAnimationActive={false} /></LineChart></ResponsiveContainer></div><div className="grid gap-3 lg:grid-cols-2">{analysis.regions.map((region) => <div key={region.id} className="rounded-xl border border-white/[0.06] bg-white/[0.025] p-4"><div className="flex items-center justify-between"><div className="font-semibold text-slate-200">{region.id}</div><div className="text-xs text-slate-500">{region.point_ids.length} 點</div></div><div className="mt-3 text-xs leading-6 text-slate-400">前緣：{region.frontier_ids.join(", ") || "無"}<br />中心：{region.center_ids.join(", ") || "無"}</div>{region.proposals.map((proposal) => <div key={proposal.point_id} className="mt-2 rounded-lg border border-white/[0.05] bg-slate-950/40 px-3 py-2 text-xs text-slate-300">{proposal.point_id} · {proposal.roles.join("、")}{proposal.provisional && <span className="ml-2 text-amber-300">暫定</span>}</div>)}</div>)}</div><div className="text-[11px] text-slate-600">點集合 {analysis.observed_point_set_hash} · 連通 {analysis.connectivity_version} · 前緣 {analysis.frontier_version} · 中心 {analysis.center_version}</div></div>;
}

function MetricCard({ label, value, tone = "text-slate-100" }: { label: string; value: number | string; tone?: string }) { return <div className="rounded-xl border border-white/[0.06] bg-white/[0.025] p-4"><div className="text-xs text-slate-500">{label}</div><div className={`mt-1 text-2xl font-semibold ${tone}`}>{value}</div></div>; }
