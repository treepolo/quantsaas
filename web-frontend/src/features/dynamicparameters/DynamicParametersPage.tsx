import { useEffect, useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { BrainCircuit, CheckCircle2, Pause, Play, RefreshCw, RotateCcw, Square, Workflow } from "lucide-react";
import { evolutionApi } from "../../shared/services/evolution";
import { marketDataApi } from "../../shared/services/marketData";
import { datasetStartDate } from "../../shared/lib/datasetDates";
import { computeTasksApi, type ComputeTask } from "../../shared/services/computeTasks";
import { dynamicParametersApi, type CreateDynamicStudy, type ParameterMode } from "../../shared/services/dynamicParameters";
import { Button } from "../../shared/ui/Button";
import { Card, CardDescription, CardHeader, CardTitle } from "../../shared/ui/Card";
import { ComputePlanSummary, ComputeProgress, ComputeStatusBadge } from "../computetasks/ComputeTaskComponents";

const inputClass = "min-h-10 w-full rounded-lg border border-white/10 bg-slate-950/70 px-3 text-sm text-slate-200 outline-none focus:border-teal-400/50";
const activeStatuses = new Set(["queued", "running", "partial"]);
const reportBlockLabels: Record<string,string> = {"model-validation":"模型驗證","parameter-comparison":"參數比較","daily-diagnostics":"每日預測與參數"};

function dayStart(value: string) { return value ? new Date(`${value}T00:00:00`).getTime() : 0; }
function dayEnd(value: string) { return value ? new Date(`${value}T23:59:59.999`).getTime() : 0; }
function dateValue(date: Date) { return date.toISOString().slice(0, 10); }
function shortHash(value?: string) { return value ? `${value.slice(0, 12)}…${value.slice(-8)}` : "尚未產生"; }

function usePageVisible() {
  const [visible, setVisible] = useState(() => document.visibilityState === "visible");
  useEffect(() => { const update = () => setVisible(document.visibilityState === "visible"); document.addEventListener("visibilitychange", update); return () => document.removeEventListener("visibilitychange", update); }, []);
  return visible;
}

function chromosomeValue(pack: Record<string, unknown> | null | undefined, key: string, fallback: number) {
  const chromosome = pack?.sigmoid_dca_config;
  if (!chromosome || typeof chromosome !== "object") return fallback;
  const value = (chromosome as Record<string, unknown>)[key];
  return typeof value === "number" && Number.isFinite(value) ? value : fallback;
}

function betaControl(mode: ParameterMode, base: number) {
  const common = { parameter_id: "beta", mode, lower: 0.2, upper: 8, base_value: base };
  if (mode === "global") return { ...common, global_value: base };
  if (mode === "continuous") return { ...common, base_logit: 0, terms: [{ input: "direction_20d", linear: 0, quadratic: 0 }] };
  if (mode === "six_state") return { ...common, base_logit: 0, effects: { direction: { up: 0, uncertain: 0, down: 0 }, volatility: { low: 0, high: 0 }, interaction: { up_low: 0, up_high: 0, uncertain_low: 0, uncertain_high: 0, down_low: 0, down_high: 0 } } };
  return common;
}

export function DynamicParametersPage() {
  const queryClient = useQueryClient();
  const pageVisible = usePageVisible();
  const genomesQuery = useQuery({ queryKey: ["genomes"], queryFn: evolutionApi.listGenomes });
  const instrumentsQuery = useQuery({ queryKey: ["market-data-instruments"], queryFn: marketDataApi.instruments });
  const studiesQuery = useQuery({ queryKey: ["dynamic-parameter-studies"], queryFn: () => dynamicParametersApi.list(80) });
  const genomes = genomesQuery.data ?? [];
  const instruments = instrumentsQuery.data?.instruments ?? [];
  const [genomeId, setGenomeId] = useState(0);
  const [instrumentId, setInstrumentId] = useState("");
  const [route, setRoute] = useState<CreateDynamicStudy["route"]>("explainable_gam");
  const [mode, setMode] = useState<ParameterMode>("fixed");
  const [executionMode, setExecutionMode] = useState<CreateDynamicStudy["execution_mode"]>("close_next_open");
  const [startDate, setStartDate] = useState("");
  const [endDate, setEndDate] = useState(() => dateValue(new Date(Date.now() - 86_400_000)));
  const [folds, setFolds] = useState(4);
  const [minimumTrain, setMinimumTrain] = useState(120);
  const [lookbacksText, setLookbacksText] = useState("5,10,20,40,60,120,250,500");
  const [selectedStudyId, setSelectedStudyId] = useState(0);
  const [taskId, setTaskId] = useState(0);
  const [previewSignature, setPreviewSignature] = useState("");
  const [confirmSoftLimit, setConfirmSoftLimit] = useState(false);
  const [confirmMaterializationSoftLimit, setConfirmMaterializationSoftLimit] = useState(false);
  const [computeMonitorEnabled, setComputeMonitorEnabled] = useState(true);
  const [materializePreview, setMaterializePreview] = useState<Awaited<ReturnType<typeof dynamicParametersApi.previewMaterialize>> | null>(null);
  const [blockId, setBlockId] = useState("model-validation");

  useEffect(() => { if (!genomeId && genomes.length) setGenomeId(genomes[0].id); }, [genomeId, genomes]);
  useEffect(() => { if (!instrumentId && instruments.length) setInstrumentId(instruments.find((item) => item.supported_intervals.includes("1d"))?.id ?? instruments[0].id); }, [instrumentId, instruments]);
  const selectedInstrument = instruments.find((item) => item.id === instrumentId);
  const selectedDatasetStart = datasetStartDate(selectedInstrument, "1d");
  useEffect(() => { if (selectedDatasetStart) setStartDate(selectedDatasetStart); }, [selectedDatasetStart]);
  const selectedGenome = genomes.find((item) => item.id === genomeId);
  const lookbacks = useMemo(() => [...new Set(lookbacksText.split(",").map(Number).filter((value) => [5, 10, 20, 40, 60, 120, 250, 500].includes(value)))].sort((a, b) => a - b), [lookbacksText]);
  const beta = chromosomeValue(selectedGenome?.param_pack, "beta", 1);
  const request: CreateDynamicStudy = {
    name: `${route === "explainable_gam" ? "可解釋模型" : "因果序列模型"} · ${selectedGenome?.name || `參數 #${genomeId}`}`,
    genome_id: genomeId, route, lookbacks, folds, minimum_train: minimumTrain,
    instrument_id: instrumentId, data_source: selectedInstrument?.data_source ?? "", symbol: selectedInstrument?.symbol ?? "", interval: "1d", execution_mode: executionMode,
    train_start_time_ms: dayStart(startDate), train_end_time_ms: dayEnd(endDate), activity_kappa: 20,
    region_rule: { direction_boundary: 0.2, magnitude_boundary: 1 },
    policy: { schema_version: "p09-dynamic-policy-v1", version: `p09-ui-${mode}-v1`, controls: [betaControl(mode, beta)], evolve_gamma: false },
    long_term_filter_enabled: true, long_term_filter_months: 10, compute_monitor_enabled: computeMonitorEnabled, confirm_soft_limit: confirmSoftLimit
  };
  const requestSignature = JSON.stringify({ ...request, confirm_soft_limit: false });
  const formValid = genomeId > 0 && !!selectedInstrument && lookbacks.length > 0 && folds >= 2 && minimumTrain >= 20 && request.train_end_time_ms > request.train_start_time_ms;

  const previewMutation = useMutation({ mutationFn: (input: CreateDynamicStudy) => dynamicParametersApi.preview({ ...input, confirm_soft_limit: false }), onSuccess: (value, input) => { setPreviewSignature(JSON.stringify({ ...input, confirm_soft_limit: false })); setConfirmSoftLimit(false); } });
  const createMutation = useMutation({ mutationFn: () => dynamicParametersApi.create(request), onSuccess: (value) => { setSelectedStudyId(value.study.id); setTaskId(value.task?.id ?? value.study.compute_task_id ?? 0); queryClient.invalidateQueries({ queryKey: ["dynamic-parameter-studies"] }); } });
  const studyQuery = useQuery({ queryKey: ["dynamic-parameter-study", selectedStudyId], queryFn: () => dynamicParametersApi.get(selectedStudyId), enabled: selectedStudyId > 0, refetchInterval: pageVisible && taskId > 0 ? 1500 : false });
  const study = studyQuery.data;
  const taskQuery = useQuery({ queryKey: ["compute-task", taskId], queryFn: () => computeTasksApi.get(taskId), enabled: taskId > 0, refetchInterval: (query) => pageVisible && activeStatuses.has((query.state.data as ComputeTask | undefined)?.status ?? "") ? 1200 : false });
  useEffect(() => { const next = study?.materialization_task_id ?? study?.compute_task_id ?? 0; if (next && next !== taskId) setTaskId(next); }, [study?.compute_task_id, study?.materialization_task_id, taskId]);
  useEffect(() => { if (taskQuery.data && !activeStatuses.has(taskQuery.data.status)) { queryClient.invalidateQueries({ queryKey: ["dynamic-parameter-study", selectedStudyId] }); queryClient.invalidateQueries({ queryKey: ["dynamic-parameter-studies"] }); } }, [taskQuery.data?.status, selectedStudyId, queryClient]);
  const taskAction = useMutation({ mutationFn: ({ action, id }: { action: "start" | "cancel" | "retry"; id: number }) => computeTasksApi[action](id), onSuccess: (value) => queryClient.setQueryData(["compute-task", value.id], value) });
  const materializeMutation = useMutation({ mutationFn: () => dynamicParametersApi.materialize(selectedStudyId, confirmMaterializationSoftLimit, computeMonitorEnabled), onSuccess: (value) => { setTaskId(value.task.id); setConfirmMaterializationSoftLimit(false); queryClient.setQueryData(["dynamic-parameter-study", selectedStudyId], value.study); queryClient.invalidateQueries({ queryKey: ["dynamic-parameter-studies"] }); } });
  const materializePreviewMutation = useMutation({ mutationFn: () => dynamicParametersApi.previewMaterialize(selectedStudyId, computeMonitorEnabled), onSuccess: (value) => setMaterializePreview(value) });
  const blockQuery = useQuery({ queryKey: ["dynamic-parameter-block", selectedStudyId, blockId], queryFn: () => dynamicParametersApi.reportBlock(selectedStudyId, blockId), enabled: selectedStudyId > 0 && !!study?.comparison && blockId !== "daily-diagnostics" || (!!study?.materialization_id && blockId === "daily-diagnostics") });

  return <section className="space-y-5">
    <header className="flex flex-wrap items-start justify-between gap-3"><div><h1 className="flex items-center gap-2 text-2xl font-bold text-slate-100"><BrainCircuit className="h-6 w-6 text-teal-300" />預測與動態參數</h1><p className="mt-1 max-w-3xl text-sm leading-6 text-slate-400">建立 1 日／20 日樣本外預測、校準、結構狀態與每日有效參數。訓練和物化分成兩個固定計算任務，均需明確啟動。</p></div>{!pageVisible && <span className="flex items-center gap-1 rounded-full border border-amber-500/20 bg-amber-500/10 px-3 py-1 text-xs text-amber-300"><Pause className="h-3 w-3" />背景頁面已停止輪詢</span>}</header>
    <div className="grid gap-5 xl:grid-cols-[minmax(0,1.2fr)_minmax(320px,.8fr)]">
      <Card><CardHeader><div><CardTitle>建立模型研究</CardTitle><CardDescription>選擇預測方法與要比較的回看天數；系統會分段驗證並保留表現接近最佳、但較簡單穩定的設定。</CardDescription></div></CardHeader>
        <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
          <Field label="來源參數"><select className={inputClass} value={genomeId} onChange={(event) => setGenomeId(Number(event.target.value))}><option value={0}>選擇參數</option>{genomes.map((item) => <option key={item.id} value={item.id}>#{item.id} · {item.name || item.role}</option>)}</select></Field>
          <Field label="模型路線"><select className={inputClass} value={route} onChange={(event) => setRoute(event.target.value as CreateDynamicStudy["route"])}><option value="explainable_gam">可解釋特徵模型</option><option value="causal_tcn">因果擴張卷積模型</option></select></Field>
          <Field label="Beta 要如何變動"><select className={inputClass} value={mode} onChange={(event) => setMode(event.target.value as ParameterMode)}><option value="fixed">固定不變</option><option value="global">整段使用同一個預測調整值</option><option value="continuous">每天依預測連續調整</option><option value="six_state">依六種市場狀態切換</option></select></Field>
          <Field label="標的（日線）"><select className={inputClass} value={instrumentId} onChange={(event) => setInstrumentId(event.target.value)}>{instruments.filter((item) => item.supported_intervals.includes("1d")).map((item) => <option key={item.id} value={item.id}>{item.display_name} · {item.id}</option>)}</select></Field>
          <Field label="執行時點"><select className={inputClass} value={executionMode} onChange={(event) => setExecutionMode(event.target.value as CreateDynamicStudy["execution_mode"])}><option value="close_next_open">收盤判斷、次日開盤執行</option><option value="close_same_bar">收盤判斷、同日收盤執行</option></select></Field>
          <Field label="分段驗證次數"><input className={inputClass} type="number" min={2} max={10} value={folds} onChange={(event) => setFolds(Number(event.target.value))} /></Field>
          <Field label="開始日期"><input className={inputClass} type="date" value={startDate} onChange={(event) => setStartDate(event.target.value)} /></Field><Field label="結束日期"><input className={inputClass} type="date" value={endDate} onChange={(event) => setEndDate(event.target.value)} /></Field>
          <Field label="每段至少需要多少筆訓練資料"><input className={inputClass} type="number" min={20} value={minimumTrain} onChange={(event) => setMinimumTrain(Number(event.target.value))} /></Field>
          <div className="md:col-span-2 xl:col-span-3"><Field label="要比較的回看天數"><input className={inputClass} value={lookbacksText} onChange={(event) => setLookbacksText(event.target.value)} /><span className="text-xs text-slate-500">可用：5、10、20、40、60、120、250、500；用逗號分隔</span></Field></div>
        </div>
        <label className="mt-5 flex items-start gap-2 rounded-lg border border-teal-400/20 bg-teal-400/5 p-3 text-sm text-teal-100"><input className="mt-1" type="checkbox" checked={computeMonitorEnabled} onChange={(event) => setComputeMonitorEnabled(event.target.checked)} /><span><span className="font-semibold">計算量監控（預設開啟）</span><span className="mt-1 block text-xs text-slate-400">執行中顯示本次已計算量、速度與預估剩餘；關閉不影響基本進度與取消。</span></span></label>
        <div className="mt-3 flex flex-wrap gap-2"><Button variant="secondary" disabled={!formValid || previewMutation.isPending} onClick={() => previewMutation.mutate(request)}><RefreshCw className="mr-2 h-4 w-4" />預覽計算</Button><Button disabled={!formValid || previewSignature !== requestSignature || (!!previewMutation.data?.requires_confirmation && !confirmSoftLimit) || createMutation.isPending} onClick={() => createMutation.mutate()}><Workflow className="mr-2 h-4 w-4" />建立訓練任務</Button></div>
        {(previewMutation.error || createMutation.error) && <p className="mt-3 text-sm text-rose-300">{String((previewMutation.error || createMutation.error)?.message)}</p>}
        {previewMutation.data && <div className="mt-5"><ComputePlanSummary preview={previewMutation.data} />{previewMutation.data.requires_confirmation&&<label className="mt-3 flex items-start gap-2 rounded-lg border border-amber-400/20 bg-amber-400/10 p-3 text-sm text-amber-100"><input className="mt-1" type="checkbox" checked={confirmSoftLimit} onChange={(event)=>setConfirmSoftLimit(event.target.checked)}/><span>這次工作量超過建議值。我了解可能需要較久時間，仍要建立訓練任務。</span></label>}</div>}
      </Card>
      <Card><CardHeader><div><CardTitle>研究紀錄</CardTitle><CardDescription>選取既有研究會讀取固定版本，不會觸發重新訓練。</CardDescription></div></CardHeader><div className="max-h-[520px] space-y-2 overflow-auto">{(studiesQuery.data ?? []).map((item) => <button key={item.id} className={`w-full rounded-lg border p-3 text-left ${selectedStudyId === item.id ? "border-teal-400/40 bg-teal-400/5" : "border-white/[0.06] bg-white/[0.02]"}`} onClick={() => { setSelectedStudyId(item.id); setTaskId(item.materialization_task_id ?? item.compute_task_id ?? 0); }}><div className="flex items-center justify-between gap-2"><span className="text-sm font-medium text-slate-200">{item.name}</span><span className="text-xs text-slate-500">#{item.id}</span></div><div className="mt-1 text-xs text-slate-500">{item.route === "explainable_gam" ? "可解釋模型" : "因果序列模型"} · {item.status}</div></button>)}{studiesQuery.data?.length === 0 && <p className="text-sm text-slate-500">尚無研究。</p>}</div></Card>
    </div>
    {taskQuery.data && <Card><CardHeader><div><CardTitle>目前計算任務</CardTitle><CardDescription>{taskQuery.data.title}</CardDescription></div><ComputeStatusBadge status={taskQuery.data.status} /></CardHeader><ComputeProgress task={taskQuery.data} /><div className="mt-4 flex flex-wrap gap-2">{taskQuery.data.status === "planned" && <Button onClick={() => taskAction.mutate({ action: "start", id: taskQuery.data.id })}><Play className="mr-2 h-4 w-4" />開始</Button>}{activeStatuses.has(taskQuery.data.status) && <Button variant="danger" onClick={() => taskAction.mutate({ action: "cancel", id: taskQuery.data.id })}><Square className="mr-2 h-4 w-4" />取消</Button>}{["failed", "cancelled", "partial"].includes(taskQuery.data.status) && <Button variant="secondary" onClick={() => taskAction.mutate({ action: "retry", id: taskQuery.data.id })}><RotateCcw className="mr-2 h-4 w-4" />重試</Button>}</div></Card>}
    {study?.artifact_set_hash && <Card><CardHeader><div><CardTitle>模型結果與來源追蹤</CardTitle><CardDescription>模型、預測、每日調整規則與報告都固定保存；只有確實勝過簡單預測基準的項目，才能用來調整參數。</CardDescription></div>{study.status === "completed" && <CheckCircle2 className="h-5 w-5 text-teal-300" />}</CardHeader>
      <div className="grid gap-3 md:grid-cols-3"><Hash label="模型內容識別碼" value={study.artifact_set_hash} /><Hash label="研究設定識別碼" value={study.setting_hash} /><Hash label="資料內容識別碼" value={study.dataset_hash} /></div>
      <div className="mt-4 overflow-x-auto"><table className="w-full text-left text-sm"><thead className="text-xs text-slate-500"><tr><th className="p-2">預測幾天後</th><th className="p-2">預測項目</th><th className="p-2">模型誤差</th><th className="p-2">簡單基準誤差</th><th className="p-2">是否證明有效</th></tr></thead><tbody>{(study.reports ?? []).map((report, index) => <tr key={`${report.horizon}-${report.target_kind}-${index}`} className="border-t border-white/[0.05]"><td className="p-2 text-slate-300">{report.horizon} 日</td><td className="p-2 text-slate-300">{report.target_kind}</td><td className="p-2 text-slate-400">{report.mean_loss?.toFixed(5)}</td><td className="p-2 text-slate-400">{report.mean_baseline_loss?.toFixed(5)}</td><td className={`p-2 ${report.baseline_gate_passed ? "text-teal-300" : "text-amber-300"}`}>{report.baseline_gate_passed ? "已證明" : "尚未證明"}</td></tr>)}</tbody></table></div>
      {study.status === "awaiting_materialization" && <div className="mt-4"><Button variant="secondary" onClick={() => materializePreviewMutation.mutate()} disabled={materializePreviewMutation.isPending}><RefreshCw className="mr-2 h-4 w-4" />預覽每日參數與回測計算量</Button>{materializePreview && <div className="mt-3"><ComputePlanSummary preview={materializePreview} /><label className="mt-3 flex items-start gap-2 rounded-lg border border-amber-400/20 bg-amber-400/10 p-3 text-sm text-amber-100"><input className="mt-1" type="checkbox" checked={confirmMaterializationSoftLimit} onChange={(event) => setConfirmMaterializationSoftLimit(event.target.checked)} /><span>我了解這次每日參數與回測可能需要較久時間，仍要建立任務。</span></label><Button className="mt-3" onClick={() => materializeMutation.mutate()} disabled={materializeMutation.isPending || (materializePreview.requires_confirmation && !confirmMaterializationSoftLimit)}><Workflow className="mr-2 h-4 w-4" />建立每日參數與回測任務</Button></div>}{(materializePreviewMutation.error || materializeMutation.error) && <p className="mt-2 text-sm text-rose-300">建立失敗：{String((materializePreviewMutation.error || materializeMutation.error as Error).message)}</p>}</div>}
      {study.comparison && <div className="mt-5 space-y-3"><div className="flex flex-wrap gap-2">{study.comparison.available_blocks.map((id) => <Button key={id} variant={blockId === id ? "primary" : "secondary"} disabled={id === "daily-diagnostics" && !study.materialization_id} onClick={() => setBlockId(id)}>{reportBlockLabels[id]??"其他結果"}</Button>)}</div>{blockQuery.data && <pre className="max-h-96 overflow-auto rounded-lg border border-white/[0.06] bg-slate-950/70 p-3 text-xs text-slate-300">{JSON.stringify(blockQuery.data.payload, null, 2)}</pre>}</div>}
    </Card>}
  </section>;
}

function Field({ label, children }: { label: string; children: React.ReactNode }) { return <label className="space-y-1.5"><span className="text-xs font-medium text-slate-400">{label}</span>{children}</label>; }
function Hash({ label, value }: { label: string; value?: string }) { return <div className="rounded-lg border border-white/[0.05] bg-white/[0.025] p-3"><div className="text-xs text-slate-500">{label}</div><code className="mt-1 block text-xs text-slate-300" title={value}>{shortHash(value)}</code></div>; }
