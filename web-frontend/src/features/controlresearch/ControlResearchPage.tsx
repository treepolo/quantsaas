import { useEffect, useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Archive, BarChart3, ChevronRight, Database, FlaskConical, Play, RefreshCw, RotateCcw, X } from "lucide-react";
import { useSearchParams } from "react-router-dom";
import { controlResearchApi, type ControlDetail, type ControlPlan, type ControlTask, type CreateControlTask } from "../../shared/services/controlResearch";
import { evolutionApi } from "../../shared/services/evolution";
import { marketDataApi } from "../../shared/services/marketData";
import { parameterResearchApi } from "../../shared/services/parameterResearch";
import { datasetStartDate } from "../../shared/lib/datasetDates";
import { Button } from "../../shared/ui/Button";
import { Card, CardDescription, CardHeader, CardTitle } from "../../shared/ui/Card";

const inputClass = "min-h-10 w-full rounded-lg border border-white/10 bg-slate-950/70 px-3 text-sm text-slate-200 outline-none focus:border-teal-400/50";
const activeStatuses = new Set(["planned", "queued", "running", "partial", "partially_completed", "supplementing"]);
const stageLabels: Record<string, string> = { baseline: "評估對象回測", random: "隨機參數", random_parameters: "隨機參數", rules: "無意義規則", meaningless_rules: "無意義規則", shuffle: "曝險順序打亂", exposure_shuffle: "曝險順序打亂" };
const ruleLabels: Record<string, string> = { odd_buy_even_sell: "奇數日買、偶數日賣", even_buy_odd_sell: "偶數日買、奇數日賣", fixed_day_toggle: "固定週期切換", open_buy_close_sell: "開盤買、收盤賣" };

function dateMs(value: string, end = false) { return value ? new Date(`${value}T${end ? "23:59:59.999" : "00:00:00"}`).getTime() : 0; }
function number(value: number | undefined, digits = 4) { return value === undefined || !Number.isFinite(value) ? "—" : value.toFixed(digits); }
function percent(value: number | undefined) { return value === undefined || !Number.isFinite(value) ? "—" : `${(value * 100).toFixed(2)}%`; }
function percentile(value: number | undefined) { return value === undefined || !Number.isFinite(value) ? "—" : `${value.toFixed(2)}%`; }
function percentileCellStyle(value: number | undefined) {
  if (value === undefined || !Number.isFinite(value)) return undefined;
  const ratio = Math.max(0, Math.min(1, value / 100));
  const from = ratio <= .5 ? [248, 113, 113] : [250, 204, 21];
  const to = ratio <= .5 ? [250, 204, 21] : [45, 212, 191];
  const progress = ratio <= .5 ? ratio * 2 : (ratio - .5) * 2;
  const [red, green, blue] = from.map((channel, index) => Math.round(channel + (to[index] - channel) * progress));
  return { color: `rgb(${red},${green},${blue})`, backgroundColor: `rgba(${red},${green},${blue},0.16)` };
}
function comparisonLabel(name: string, values: { log_final_nav_ratio: number; max_drawdown: number; sortino?: number }) {
  const metrics = [`報酬第 ${values.log_final_nav_ratio.toFixed(1)} 百分位`, `最大回撤第 ${values.max_drawdown.toFixed(1)} 百分位`];
  if (values.sortino !== undefined && Number.isFinite(values.sortino)) metrics.push(`Sortino 第 ${values.sortino.toFixed(1)} 百分位`);
  return `評估對象於${name}：${metrics.join("、")}`;
}
function useVisible() { const [visible, setVisible] = useState(document.visibilityState === "visible"); useEffect(() => { const update = () => setVisible(document.visibilityState === "visible"); document.addEventListener("visibilitychange", update); return () => document.removeEventListener("visibilitychange", update); }, []); return visible; }

function Stat({ label, value }: { label: string; value: string | number }) { return <div className="rounded-lg border border-white/5 bg-slate-950/50 p-3"><div className="text-xs text-slate-500">{label}</div><div className="mt-1 text-sm font-semibold text-slate-100">{value}</div></div>; }

function PlanPanel({ plan }: { plan: ControlPlan }) {
  return <Card className="border-teal-400/20"><CardHeader><div><CardTitle>執行前確認</CardTitle><CardDescription>固定維度不抽樣；隨機維度只從來源研究範圍抽取，且不重複。</CardDescription></div></CardHeader><div className="grid gap-2 sm:grid-cols-4"><Stat label="總工作項" value={plan.compute.total_items}/><Stat label="可沿用快取" value={plan.compute.cache_hit_count}/><Stat label="新工作項" value={plan.compute.new_item_count}/><Stat label="拒絕樣本" value={plan.rejection_count}/></div><div className="mt-4 grid gap-3 lg:grid-cols-2"><div><div className="mb-2 text-xs text-slate-500">隨機維度</div><div className="flex flex-wrap gap-1">{plan.random_dimensions.map(name => <span key={name} className="rounded bg-teal-400/10 px-2 py-1 text-xs text-teal-200">{name}</span>)}</div></div><div><div className="mb-2 text-xs text-slate-500">固定維度</div><div className="flex max-h-24 flex-wrap gap-1 overflow-auto">{plan.fixed_dimensions.map(name => <span key={name} className="rounded bg-white/5 px-2 py-1 text-xs text-slate-400">{name}</span>)}</div></div></div><div className="mt-4 space-y-2">{plan.compute.stages.map(stage => <div key={stage.stage_key} className="flex items-center justify-between rounded-lg border border-white/5 px-3 py-2 text-sm"><span>{stageLabels[stage.stage_key ?? ""] ?? stage.stage_key}</span><span className="text-slate-400">{stage.total_items} 項 · 快取 {stage.cache_hit_count}</span></div>)}</div>{plan.compute.requires_confirmation && <p className="mt-3 text-sm text-amber-300">工作量超過軟上限，建立時需勾選確認。</p>}</Card>;
}

function SnapshotPanel({ task, onDetail }: { task: ControlTask; onDetail: () => void }) {
  const snapshot = task.latest_snapshot;
  if (!snapshot) return <Card><CardTitle>分析快照</CardTitle><p className="mt-3 text-sm text-slate-400">評估對象工作完成後才會建立第一份不可變快照。</p></Card>;
  const summary = snapshot.summary;
  const rules = summary.rules ?? [];
  const comparisonLabels = [
    summary.random_percentiles && comparisonLabel("同結構隨機參數分佈", summary.random_percentiles),
    summary.shuffle_percentiles && comparisonLabel("曝險順序打亂分佈", summary.shuffle_percentiles),
  ].filter((label): label is string => Boolean(label));
  const labels = comparisonLabels.length > 0 ? comparisonLabels : summary.conclusion_labels ?? [];
  return <Card>
    <CardHeader>
      <div>
        <CardTitle>分析快照 #{snapshot.id}</CardTitle>
        <CardDescription>{snapshot.completeness === "completed" ? "完整" : "部分完成"} · 隨機 {snapshot.random_completed_count}/{task.random_target_count} · 打亂 {snapshot.shuffle_completed_count}/{task.shuffle_target_count}</CardDescription>
      </div>
      <Button size="sm" variant="secondary" icon={Database} onClick={onDetail}>載入明細</Button>
    </CardHeader>
    <div className="grid gap-2 sm:grid-cols-4">
      <Stat label="評估對象報酬率" value={percent(summary.baseline.roi)}/>
      <Stat label="相對最終淨值 log" value={number(summary.baseline.log_final_nav_ratio)}/>
      <Stat label="最大回撤" value={percent(summary.baseline.max_drawdown)}/>
      <Stat label="Sortino" value={number(summary.baseline.sortino)}/>
    </div>
    {(summary.random_distribution || summary.shuffle_distribution) && <div className="mt-5 overflow-x-auto">
      <table className="min-w-[1100px] w-full border-collapse text-left text-sm">
        <thead className="bg-slate-950/50 text-xs text-slate-400">
          <tr>
            <th className="border border-white/10 px-3 py-2 font-medium">對照組名稱／比較結果</th>
            <th className="border border-white/10 px-3 py-2 font-medium">評估對象報酬百分位</th>
            <th className="border border-white/10 px-3 py-2 font-medium">評估對象最大回撤百分位</th>
            <th className="border border-white/10 px-3 py-2 font-medium">評估對象 Sortino 百分位</th>
            <th className="border border-white/10 px-3 py-2 font-medium">P05 / P50 / P95</th>
            <th className="border border-white/10 px-3 py-2 font-medium">樣本數</th>
          </tr>
        </thead>
        <tbody className="text-slate-100">
          {summary.random_distribution && <tr>
            <th className="border border-white/10 bg-slate-950/30 px-3 py-3 font-medium">隨機參數分布</th>
            <td className="border border-white/10 px-3 py-3 font-semibold" style={percentileCellStyle(summary.random_percentiles?.log_final_nav_ratio)}>{percentile(summary.random_percentiles?.log_final_nav_ratio)}</td>
            <td className="border border-white/10 px-3 py-3 font-semibold" style={percentileCellStyle(summary.random_percentiles?.max_drawdown)}>{percentile(summary.random_percentiles?.max_drawdown)}</td>
            <td className="border border-white/10 px-3 py-3 font-semibold" style={percentileCellStyle(summary.random_percentiles?.sortino)}>{percentile(summary.random_percentiles?.sortino)}</td>
            <td className="border border-white/10 px-3 py-3 font-semibold">{`${number(summary.random_distribution.log_final_nav_ratio.p05)} / ${number(summary.random_distribution.log_final_nav_ratio.median)} / ${number(summary.random_distribution.log_final_nav_ratio.p95)}`}</td>
            <td className="border border-white/10 px-3 py-3 font-semibold">{summary.random_distribution.log_final_nav_ratio.count}</td>
          </tr>}
          {summary.shuffle_distribution && <tr>
            <th className="border border-white/10 bg-slate-950/30 px-3 py-3 font-medium">曝險順序打亂</th>
            <td className="border border-white/10 px-3 py-3 font-semibold" style={percentileCellStyle(summary.shuffle_percentiles?.log_final_nav_ratio)}>{percentile(summary.shuffle_percentiles?.log_final_nav_ratio)}</td>
            <td className="border border-white/10 px-3 py-3 font-semibold" style={percentileCellStyle(summary.shuffle_percentiles?.max_drawdown)}>{percentile(summary.shuffle_percentiles?.max_drawdown)}</td>
            <td className="border border-white/10 px-3 py-3 font-semibold" style={percentileCellStyle(summary.shuffle_percentiles?.sortino)}>{percentile(summary.shuffle_percentiles?.sortino)}</td>
            <td className="border border-white/10 px-3 py-3 font-semibold">{`${number(summary.shuffle_distribution.log_final_nav_ratio.p05)} / ${number(summary.shuffle_distribution.log_final_nav_ratio.median)} / ${number(summary.shuffle_distribution.log_final_nav_ratio.p95)}`}</td>
            <td className="border border-white/10 px-3 py-3 font-semibold">{summary.shuffle_distribution.log_final_nav_ratio.count}</td>
          </tr>}
        </tbody>
      </table>
    </div>}
    {rules.length > 0 && <div className="mt-5 overflow-x-auto"><h3 className="mb-2 text-sm font-semibold text-slate-200">無意義規則對照</h3><table className="w-full text-left text-sm"><thead className="text-xs text-slate-500"><tr><th className="py-2">規則</th><th>報酬率</th><th>最大回撤</th><th>Sortino</th><th>交易次數</th></tr></thead><tbody>{rules.map(rule => <tr key={rule.evaluation_id} className="border-t border-white/5"><td className="py-2">{ruleLabels[rule.rule_type] ?? rule.rule_type}</td><td>{percent(rule.metrics.roi)}</td><td>{percent(rule.metrics.max_drawdown)}</td><td>{number(rule.metrics.sortino)}</td><td>{rule.metrics.trade_count}</td></tr>)}</tbody></table></div>}
    {labels.length > 0 && <div className="mt-4 flex flex-wrap gap-2">{labels.map(label => <span key={label} className="rounded-full border border-teal-400/20 bg-teal-400/10 px-3 py-1 text-xs text-teal-200">{label}</span>)}</div>}
  </Card>;
}

function DetailPanel({ detail, onPath }: { detail?: ControlDetail; onPath: (id: number) => void }) {
  if (!detail) return null;
  return <Card><CardHeader><div><CardTitle>快照明細</CardTitle><CardDescription>只載入已選快照的摘要；路徑區塊仍需按鈕才會讀取。</CardDescription></div></CardHeader><div className="max-h-[420px] space-y-2 overflow-auto">{detail.evaluations.map(item => <div key={item.id} className="flex flex-wrap items-center justify-between gap-2 rounded-lg border border-white/5 p-3 text-sm"><div><span className="text-slate-200">{item.kind} #{item.sequence_index}</span>{item.representative_role && <span className="ml-2 text-xs text-teal-300">{item.representative_role}</span>}<div className="mt-1 text-xs text-slate-500">報酬 {percent(item.metrics.roi)} · 回撤 {percent(item.metrics.max_drawdown)}</div></div><Button size="sm" variant="ghost" icon={BarChart3} onClick={() => onPath(item.id)}>載入首段路徑</Button></div>)}</div></Card>;
}

function taskDate(timeMs: number) { return timeMs > 0 ? new Date(timeMs).toLocaleDateString("zh-TW") : "未設定"; }

function TaskStudySettings({ task }: { task: ControlTask }) {
  const settings = task.study_settings;
  if (!settings) return null;
  const sourceName = settings.subject_display_name || "未命名評估對象";
  const source = task.candidate_id ? `${sourceName}（M 候選 #${task.candidate_id}）` : task.source_genome_id ? `${sourceName}（基因庫 #${task.source_genome_id}）` : sourceName;
  const instrumentName = settings.instrument_display_name || settings.symbol || settings.instrument_id || "未設定";
  const instrument = settings.symbol && instrumentName !== settings.symbol ? `${instrumentName}（${settings.symbol}）` : instrumentName;
  const executionMode = settings.execution_mode === "close_next_open" ? "收盤決策、次開盤成交" : settings.execution_mode === "close_same_bar" ? "收盤同根成交" : settings.execution_mode || "未設定";
  const rows: Array<[string, string]> = [
    ["評估對象", source],
    ["研究商品", instrument],
    ["資料與週期", `${settings.data_source || "未設定"} · ${settings.interval || "未設定"}`],
    ["回測區間", `${taskDate(settings.start_time_ms)} 至 ${taskDate(settings.end_time_ms)}`],
    ["執行假設", executionMode],
    ["初始資金／每月投入", `${settings.initial_capital.toLocaleString("zh-TW")} / ${settings.monthly_dca.toLocaleString("zh-TW")}`],
    ["手續費／價差滑價", `${percent(settings.fee_rate)} / ${percent(settings.spread_rate)}`],
    ["長週期風險濾網", settings.long_term_filter_enabled ? `${settings.long_term_filter_months} 月` : "未啟用"],
    ["隨機參數", `${settings.random_count.toLocaleString("zh-TW")} 組 · seed ${settings.random_seed}`],
    ["曝險順序打亂", `${settings.shuffle_count.toLocaleString("zh-TW")} 組 · seed ${settings.shuffle_seed}`],
    ["固定週期切換", `每 ${settings.toggle_every_n_bars} 根`],
    ["參數結構", `隨機 ${settings.random_dimension_count} 項 · 固定 ${settings.fixed_dimension_count} 項`],
  ];
  return <div className="mt-4 rounded-lg border border-white/10 bg-slate-950/30 p-4"><h3 className="text-sm font-semibold text-slate-200">研究規則與設定</h3><dl className="mt-3 grid gap-x-4 gap-y-3 sm:grid-cols-2 xl:grid-cols-3">{rows.map(([label, value]) => <div key={label} className="border-l border-white/10 pl-3"><dt className="text-xs text-slate-500">{label}</dt><dd className="mt-1 text-sm text-slate-200">{value}</dd></div>)}</dl><div className="mt-4 border-t border-white/10 pt-3"><div className="text-xs text-slate-500">無意義規則對照</div><div className="mt-2 flex flex-wrap gap-2">{settings.meaningless_rule_labels.map(label => <span key={label} className="rounded border border-white/10 bg-white/[0.03] px-2.5 py-1 text-xs text-slate-300">{label}</span>)}</div></div></div>;
}

export function ControlResearchPage() {
  const queryClient = useQueryClient(); const visible = useVisible(); const [search] = useSearchParams();
  const genomesQ = useQuery({ queryKey: ["genomes"], queryFn: evolutionApi.listGenomes });
  const instrumentsQ = useQuery({ queryKey: ["market-data-instruments"], queryFn: marketDataApi.instruments });
  const candidatesQ = useQuery({ queryKey: ["parameter-research-candidates"], queryFn: () => parameterResearchApi.listCandidates() });
  const tasksQ = useQuery({ queryKey: ["control-analysis-tasks"], queryFn: controlResearchApi.list });
  const [sourceKind, setSourceKind] = useState<"gene" | "candidate">(search.get("candidate") ? "candidate" : "gene");
  const [sourceId, setSourceId] = useState(Number(search.get("candidate") ?? 0)); const [taskId, setTaskId] = useState(Number(search.get("task") ?? 0));
  const [name, setName] = useState("對照研究"); const [instrumentId, setInstrumentId] = useState(""); const [startDate, setStartDate] = useState(""); const [endDate, setEndDate] = useState(new Date(Date.now() - 86_400_000).toISOString().slice(0, 10));
  const [interval, setInterval] = useState("1d"); const [executionMode, setExecutionMode] = useState("close_next_open");
  const [randomSeed, setRandomSeed] = useState(42); const [randomCount, setRandomCount] = useState(100); const [shuffleSeed, setShuffleSeed] = useState(84); const [shuffleCount, setShuffleCount] = useState(100); const [toggleEvery, setToggleEvery] = useState(7);
  const [plan, setPlan] = useState<ControlPlan>(); const [confirm, setConfirm] = useState(false); const [detail, setDetail] = useState<ControlDetail>(); const [pathMessage, setPathMessage] = useState(""); const [extension, setExtension] = useState<ControlPlan>(); const [extensionRandomCount, setExtensionRandomCount] = useState(0); const [extensionShuffleCount, setExtensionShuffleCount] = useState(0);
  const instruments = instrumentsQ.data?.instruments ?? []; const sources = sourceKind === "gene" ? (genomesQ.data ?? []) : (candidatesQ.data ?? []).filter(item => !item.archived);
  useEffect(() => { if (!sourceId && sources.length) setSourceId(sources[0].id); }, [sourceId, sources]);
  useEffect(() => { if (!instrumentId && instruments.length) { setInstrumentId(instruments[0].id); setInterval(instruments[0].supported_intervals[0] ?? "1d"); } }, [instrumentId, instruments]);
  useEffect(() => { if (!taskId && tasksQ.data?.length) setTaskId(tasksQ.data[0].id); }, [taskId, tasksQ.data]);
  const taskQ = useQuery({ queryKey: ["control-analysis-task", taskId], queryFn: () => controlResearchApi.get(taskId), enabled: taskId > 0, refetchInterval: query => visible && activeStatuses.has((query.state.data as ControlTask | undefined)?.status ?? "") ? 1500 : false });
  const task = taskQ.data; const selectedInstrument = instruments.find(item => item.id === instrumentId);
  useEffect(() => { if (task) { setExtensionRandomCount(task.random_target_count + 1); setExtensionShuffleCount(task.shuffle_target_count + 1); setExtension(undefined); } }, [task?.id]);
  const selectedDatasetStart = datasetStartDate(selectedInstrument, interval);
  useEffect(() => { if (sourceKind === "gene" && selectedDatasetStart) setStartDate(selectedDatasetStart); }, [sourceKind, selectedDatasetStart]);
  const payload = useMemo<CreateControlTask>(() => ({ name, ...(sourceKind === "gene" ? { genome_id: sourceId } : { candidate_id: sourceId }), backtest: { instrument_id: instrumentId, data_source: selectedInstrument?.data_source ?? "", symbol: selectedInstrument?.symbol ?? "", interval, execution_mode: executionMode, start_time_ms: dateMs(startDate), end_time_ms: dateMs(endDate, true) }, random_seed: randomSeed, random_count: randomCount, shuffle_seed: shuffleSeed, shuffle_count: shuffleCount, toggle_every_n_bars: toggleEvery, confirm_soft_limit: confirm, expected_plan_key: plan?.plan_key }), [name, sourceKind, sourceId, instrumentId, selectedInstrument, interval, executionMode, startDate, endDate, randomSeed, randomCount, shuffleSeed, shuffleCount, toggleEvery, confirm, plan]);
  const invalidate = () => { queryClient.invalidateQueries({ queryKey: ["control-analysis-tasks"] }); if (taskId) queryClient.invalidateQueries({ queryKey: ["control-analysis-task", taskId] }); };
  const preview = useMutation({ mutationFn: () => controlResearchApi.preview(payload), onSuccess: setPlan });
  const create = useMutation({ mutationFn: () => controlResearchApi.create(payload), onSuccess: value => { setTaskId(value.id); setPlan(undefined); invalidate(); } });
  const action = useMutation({ mutationFn: (kind: "start" | "cancel" | "retry") => kind === "start" ? controlResearchApi.startNext(taskId) : kind === "cancel" ? controlResearchApi.cancel(taskId) : controlResearchApi.retry(taskId), onSuccess: invalidate });
  const detailMutation = useMutation({ mutationFn: () => controlResearchApi.detail(task!.id, task!.latest_snapshot!.id), onSuccess: setDetail });
  const pathMutation = useMutation({ mutationFn: (id: number) => controlResearchApi.pathBlock(id), onSuccess: value => setPathMessage(`路徑區塊已載入（${JSON.stringify(value).length.toLocaleString()} bytes）`) });
  const extendPreview = useMutation({ mutationFn: () => controlResearchApi.previewExtension(taskId, extensionRandomCount, extensionShuffleCount), onSuccess: setExtension });
  const extend = useMutation({ mutationFn: () => controlResearchApi.extend(taskId, extensionRandomCount, extensionShuffleCount, confirm), onSuccess: () => { setExtension(undefined); invalidate(); } });
  const archive = useMutation({ mutationFn: () => controlResearchApi.updateMetadata(taskId, { name: task?.name, notes: task?.notes, tags: task?.tags, archived: !task?.archived }), onSuccess: invalidate });
	const cleanup = useMutation({ mutationFn: async (kind: "paths" | "task") => { if (!task) return; if (kind === "paths") return controlResearchApi.deletePathDetails(task.id); const batchId = task.random_batch_id; await controlResearchApi.deleteTask(task.id); await controlResearchApi.deleteUnusedBatch(batchId); }, onSuccess: () => { setTaskId(0); setDetail(undefined); invalidate(); } });

  return <div className="space-y-5"><div><p className="text-xs uppercase tracking-[0.24em] text-teal-300">P11 · H</p><h1 className="mt-2 text-2xl font-semibold text-white">隨機參數與無意義規則對照</h1><p className="mt-2 max-w-4xl text-sm leading-6 text-slate-400">檢查候選結果是否真的優於合法參數抽樣、四種無意義規則，以及相同曝險量但不同時間順序的對照。每階段獨立確認，頁面隱藏時停止輪詢。</p></div>
  <Card><CardHeader><div><CardTitle>1. 建立研究</CardTitle><CardDescription>可直接使用基因，或使用 M 候選的固定／動態結構；動態候選不會在 H 重新實作 K 規則。</CardDescription></div></CardHeader><div className="grid gap-3 md:grid-cols-2 xl:grid-cols-4"><label className="text-xs text-slate-400">來源<select className={`${inputClass} mt-1`} value={sourceKind} onChange={event => { setSourceKind(event.target.value as "gene" | "candidate"); setSourceId(0); setPlan(undefined); }}><option value="gene">基因庫</option><option value="candidate">M 候選</option></select></label><label className="text-xs text-slate-400">來源項目<select className={`${inputClass} mt-1`} value={sourceId} onChange={event => { setSourceId(Number(event.target.value)); setPlan(undefined); }}>{sources.map(item => <option key={item.id} value={item.id}>#{item.id} {"name" in item ? item.name : ""}</option>)}</select></label><label className="text-xs text-slate-400">名稱<input className={`${inputClass} mt-1`} value={name} onChange={event => setName(event.target.value)}/></label><label className="text-xs text-slate-400">標的<select className={`${inputClass} mt-1`} value={instrumentId} disabled={sourceKind === "candidate"} onChange={event => setInstrumentId(event.target.value)}>{instruments.map(item => <option key={item.id} value={item.id}>{item.display_name}</option>)}</select></label><label className="text-xs text-slate-400">開始日<input type="date" className={`${inputClass} mt-1`} value={startDate} disabled={sourceKind === "candidate"} onChange={event => setStartDate(event.target.value)}/></label><label className="text-xs text-slate-400">結束日<input type="date" className={`${inputClass} mt-1`} value={endDate} disabled={sourceKind === "candidate"} onChange={event => setEndDate(event.target.value)}/></label><label className="text-xs text-slate-400">週期<input className={`${inputClass} mt-1`} value={interval} disabled={sourceKind === "candidate"} onChange={event => setInterval(event.target.value)}/></label><label className="text-xs text-slate-400">成交模式<select className={`${inputClass} mt-1`} value={executionMode} disabled={sourceKind === "candidate"} onChange={event => setExecutionMode(event.target.value)}><option value="close_next_open">收盤決策、次開盤成交</option><option value="close">收盤成交</option></select></label><label className="text-xs text-slate-400">隨機參數 seed<input type="number" className={`${inputClass} mt-1`} value={randomSeed} onChange={event => setRandomSeed(Number(event.target.value))}/></label><label className="text-xs text-slate-400">隨機參數數量<input type="number" min={1} max={10000} className={`${inputClass} mt-1`} value={randomCount} onChange={event => setRandomCount(Number(event.target.value))}/></label><label className="text-xs text-slate-400">曝險打亂 seed<input type="number" className={`${inputClass} mt-1`} value={shuffleSeed} onChange={event => setShuffleSeed(Number(event.target.value))}/></label><label className="text-xs text-slate-400">曝險打亂數量<input type="number" min={1} max={10000} className={`${inputClass} mt-1`} value={shuffleCount} onChange={event => setShuffleCount(Number(event.target.value))}/></label><label className="text-xs text-slate-400">固定切換間隔（日）<input type="number" min={1} className={`${inputClass} mt-1`} value={toggleEvery} onChange={event => setToggleEvery(Number(event.target.value))}/></label></div><div className="mt-4 flex flex-wrap items-center gap-2"><Button icon={FlaskConical} loading={preview.isPending} disabled={!sourceId} onClick={() => preview.mutate()}>預覽完整計畫</Button>{plan && <Button icon={Play} loading={create.isPending} onClick={() => create.mutate()}>建立並執行評估對象階段</Button>}{plan?.compute.requires_confirmation && <label className="flex items-center gap-2 text-sm text-amber-200"><input type="checkbox" checked={confirm} onChange={event => setConfirm(event.target.checked)}/>確認超過軟上限</label>}</div>{(preview.error || create.error) && <p className="mt-3 text-sm text-rose-300">{String((preview.error ?? create.error)?.message)}</p>}</Card>
  {plan && <PlanPanel plan={plan}/>}<Card><CardHeader><div><CardTitle>2. 任務與階段</CardTitle><CardDescription>評估對象完成後，依序手動啟動隨機參數、四種規則、曝險打亂；失敗可保留成功結果後重試。</CardDescription></div></CardHeader><select className={`${inputClass} max-w-xl`} value={taskId} onChange={event => { setTaskId(Number(event.target.value)); setDetail(undefined); }}>{(tasksQ.data ?? []).map(item => <option key={item.id} value={item.id}>#{item.id} {item.name} · {item.status}</option>)}</select>{task && <><TaskStudySettings task={task}/><div className="mt-4 flex flex-wrap gap-2"><Button icon={ChevronRight} loading={action.isPending} onClick={() => action.mutate("start")}>啟動下一階段</Button><Button variant="secondary" icon={RefreshCw} onClick={() => taskQ.refetch()}>更新</Button><Button variant="secondary" icon={RotateCcw} onClick={() => action.mutate("retry")}>重試失敗項目</Button><Button variant="danger" icon={X} onClick={() => action.mutate("cancel")}>取消</Button><Button variant="ghost" icon={Archive} onClick={() => archive.mutate()}>{task.archived ? "取消封存" : "封存"}</Button></div><div className="mt-4 space-y-2">{task.stages.map(stage => <div key={stage.id} className="rounded-lg border border-white/5 p-3"><div className="flex justify-between gap-3 text-sm"><span>{stageLabels[stage.key] ?? stage.key}</span><span className="text-slate-400">{stage.status} · {stage.completed_count}/{stage.total_count}</span></div><div className="mt-2 h-1.5 overflow-hidden rounded bg-white/5"><div className="h-full bg-teal-400" style={{ width: `${Math.max(0, Math.min(100, stage.progress * 100))}%` }}/></div>{stage.error && <p className="mt-2 text-xs text-rose-300">{stage.error}</p>}</div>)}</div></>}</Card>
  {task && <SnapshotPanel task={task} onDetail={() => detailMutation.mutate()}/>}<DetailPanel detail={detail} onPath={id => pathMutation.mutate(id)}/>{pathMessage && <p className="text-xs text-slate-400">{pathMessage}</p>}
  {task && <Card><CardHeader><div><CardTitle>3. 追加樣本與人工清理</CardTitle><CardDescription>輸入追加後的總數；至少一種樣本數必須增加。相同 seed 會保留原有前綴，只新增缺少的樣本與工作。</CardDescription></div></CardHeader><div className="grid gap-3 sm:grid-cols-2"><label className="text-xs text-slate-400">隨機參數追加後總數<input type="number" min={task.random_target_count} className={`${inputClass} mt-1`} value={extensionRandomCount} onChange={event => { setExtensionRandomCount(Number(event.target.value)); setExtension(undefined); }}/></label><label className="text-xs text-slate-400">曝險打亂追加後總數<input type="number" min={task.shuffle_target_count} className={`${inputClass} mt-1`} value={extensionShuffleCount} onChange={event => { setExtensionShuffleCount(Number(event.target.value)); setExtension(undefined); }}/></label></div><div className="mt-3 flex flex-wrap gap-2"><Button variant="secondary" loading={extendPreview.isPending} disabled={extensionRandomCount < task.random_target_count || extensionShuffleCount < task.shuffle_target_count || (extensionRandomCount === task.random_target_count && extensionShuffleCount === task.shuffle_target_count)} onClick={() => extendPreview.mutate()}>預覽追加</Button>{extension && <Button loading={extend.isPending} onClick={() => extend.mutate()}>執行追加</Button>}<Button variant="ghost" disabled={!task.latest_snapshot || cleanup.isPending} onClick={() => window.confirm("刪除本任務引用結果的詳細 NAV 路徑，只保留摘要？這可能影響共用同一標準結果的其他研究。") && cleanup.mutate("paths")}>刪除詳細 NAV</Button><Button variant="danger" disabled={activeStatuses.has(task.status) || cleanup.isPending} onClick={() => window.confirm("永久刪除此 H 任務、評估與快照？標準回測和計算稽核仍會保留。") && cleanup.mutate("task")}>刪除任務</Button></div>{(extendPreview.error || extend.error) && <p className="mt-3 text-sm text-rose-300">{String((extendPreview.error ?? extend.error)?.message)}</p>}{extension && <div className="mt-3 grid gap-2 sm:grid-cols-3"><Stat label="總工作項" value={extension.compute.total_items}/><Stat label="沿用快取" value={extension.compute.cache_hit_count}/><Stat label="新工作項" value={extension.compute.new_item_count}/></div>}</Card>}</div>;
}
