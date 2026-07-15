import { useEffect, useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Ban, ChevronRight, ListTree, Play, RefreshCcw, RotateCcw } from "lucide-react";
import { Button } from "../../shared/ui/Button";
import {
  computeTasksApi,
  type ComputeTask,
  type ComputeTaskItem,
  type ComputeTaskStatus
} from "../../shared/services/computeTasks";
import { ComputePlanSummary, ComputeProgress, ComputeStatusBadge } from "./ComputeTaskComponents";

const activeStatuses = new Set<ComputeTaskStatus>(["queued", "running"]);

export function ComputeTasksPage() {
  const visible = usePageVisible();
  const queryClient = useQueryClient();
  const [selectedID, setSelectedID] = useState<number>();
  const [showItems, setShowItems] = useState(false);
  const [actionError, setActionError] = useState("");

  const roots = useQuery({
    queryKey: ["compute-tasks", "roots"],
    queryFn: () => computeTasksApi.list({ rootOnly: true, limit: 100 }),
    refetchInterval: (query) =>
      visible && ((query.state.data as ComputeTask[] | undefined)?.some((task) => activeStatuses.has(task.status)) ?? false)
        ? 2_000
        : false,
    refetchIntervalInBackground: false
  });
  const limits = useQuery({ queryKey: ["compute-tasks", "limits"], queryFn: computeTasksApi.limits, staleTime: 60_000 });

  useEffect(() => {
    if (selectedID === undefined && roots.data?.length) setSelectedID(roots.data[0].id);
  }, [roots.data, selectedID]);

  const task = useQuery({
    queryKey: ["compute-task", selectedID],
    queryFn: () => computeTasksApi.get(selectedID!),
    enabled: selectedID !== undefined,
    refetchInterval: (query) =>
      visible && activeStatuses.has((query.state.data as ComputeTask | undefined)?.status ?? "planned") ? 1_500 : false,
    refetchIntervalInBackground: false
  });

  const children = useQuery({
    queryKey: ["compute-tasks", "children", task.data?.id],
    queryFn: () => computeTasksApi.list({ parentTaskId: task.data!.id, limit: 100 }),
    enabled: task.data?.kind === "composite",
    refetchInterval: (query) =>
      visible && ((query.state.data as ComputeTask[] | undefined)?.some((child) => activeStatuses.has(child.status)) ?? false)
        ? 1_500
        : false,
    refetchIntervalInBackground: false
  });

  const preview = useQuery({
    queryKey: ["compute-task", selectedID, "preview"],
    queryFn: () => computeTasksApi.preview(selectedID!),
    enabled: selectedID !== undefined && task.data?.kind !== "composite" && task.data?.status === "planned",
    staleTime: 10_000
  });

  const items = useQuery({
    queryKey: ["compute-task", selectedID, "items"],
    queryFn: () => computeTasksApi.items(selectedID!, { limit: 200 }),
    enabled: selectedID !== undefined && showItems && task.data?.kind !== "composite"
  });

  const invalidate = async (changed?: ComputeTask) => {
    await Promise.all([
      queryClient.invalidateQueries({ queryKey: ["compute-tasks"] }),
      queryClient.invalidateQueries({ queryKey: ["compute-task", selectedID] }),
      changed?.parent_task_id
        ? queryClient.invalidateQueries({ queryKey: ["compute-task", changed.parent_task_id] })
        : Promise.resolve()
    ]);
  };

  const start = useMutation({
    mutationFn: (id: number) => computeTasksApi.start(id),
    onSuccess: invalidate,
    onError: (error: Error) => setActionError(error.message)
  });
  const cancel = useMutation({
    mutationFn: (id: number) => computeTasksApi.cancel(id),
    onSuccess: invalidate,
    onError: (error: Error) => setActionError(error.message)
  });
  const retry = useMutation({
    mutationFn: (id: number) => computeTasksApi.retry(id),
    onSuccess: invalidate,
    onError: (error: Error) => setActionError(error.message)
  });

  const rootRows = roots.data ?? [];
  const selected = task.data;
  const isMutating = start.isPending || cancel.isPending || retry.isPending;

  const selectTask = (id: number) => {
    setSelectedID(id);
    setShowItems(false);
    setActionError("");
  };

  const startSelected = () => {
    if (!selected || !preview.data) return;
    if (
      preview.data.requires_confirmation &&
      !window.confirm(`本階段將新增 ${preview.data.new_item_count.toLocaleString()} 個計算項目，確定啟動？`)
    ) {
      return;
    }
    setActionError("");
    start.mutate(selected.id);
  };

  return (
    <div className="space-y-5">
      <header className="flex flex-wrap items-start justify-between gap-4">
        <div>
          <div className="text-xs font-semibold uppercase tracking-[0.22em] text-[#2dd4bf]">Compute task center</div>
          <h1 className="mt-2 text-2xl font-bold text-slate-100">計算任務</h1>
          <p className="mt-2 max-w-3xl text-sm leading-6 text-slate-500">
            查看固定計算計畫、快取命中與階段依賴。頁面與圖表切換不會建立新任務；複合研究的每個階段都要分別啟動。
          </p>
        </div>
        <div className="flex gap-2 text-xs text-slate-500">
          <Pill label="Worker" value={limits.data?.workers ?? "—"} />
          <Pill label="建議上限" value={limits.data?.soft_item_limit?.toLocaleString() ?? "—"} />
          <Button
            variant="ghost"
            className="min-h-9 px-3"
            icon={RefreshCcw}
            onClick={() => queryClient.invalidateQueries({ queryKey: ["compute-tasks"] })}
          >
            重新整理
          </Button>
        </div>
      </header>

      <div className="grid min-h-[620px] gap-4 xl:grid-cols-[360px_minmax(0,1fr)]">
        <section className="rounded-2xl border border-white/[0.06] bg-[#07101f]/80 p-3">
          <div className="mb-3 flex items-center justify-between px-2">
            <div className="text-sm font-semibold text-slate-200">任務紀錄</div>
            <div className="text-xs text-slate-600">{rootRows.length} 筆</div>
          </div>
          <div className="space-y-2">
            {roots.isLoading && <EmptyState text="讀取任務中…" />}
            {roots.isError && <EmptyState text="無法讀取任務" tone="text-rose-300" />}
            {!roots.isLoading && !rootRows.length && <EmptyState text="尚無 P05 計算任務；後續研究模組建立任務後會出現在這裡。" />}
            {rootRows.map((row) => (
              <TaskRow key={row.id} task={row} active={selectedID === row.id} onClick={() => selectTask(row.id)} />
            ))}
          </div>
        </section>

        <section className="rounded-2xl border border-white/[0.06] bg-[#07101f]/80 p-4 sm:p-5">
          {!selectedID && <EmptyState text="選擇任務以查看固定計畫與結果。" />}
          {selectedID && task.isLoading && <EmptyState text="讀取任務內容中…" />}
          {selectedID && task.isError && <EmptyState text="任務不存在或無權查看。" tone="text-rose-300" />}
          {selected && (
            <div className="space-y-5">
              <div className="flex flex-wrap items-start justify-between gap-3">
                <div className="min-w-0">
                  <div className="flex flex-wrap items-center gap-2">
                    <ComputeStatusBadge status={selected.status} />
                    <span className="text-xs text-slate-600">#{selected.id}</span>
                    {selected.stage_key && <span className="text-xs text-slate-500">階段 {selected.stage_order} · {selected.stage_key}</span>}
                  </div>
                  <h2 className="mt-3 truncate text-xl font-semibold text-slate-100">{selected.title}</h2>
                  <div className="mt-1 text-xs text-slate-600">{selected.task_type}</div>
                </div>
                <TaskActions
                  task={selected}
                  previewReady={Boolean(preview.data)}
                  busy={isMutating}
                  onStart={startSelected}
                  onCancel={() => cancel.mutate(selected.id)}
                  onRetry={() => retry.mutate(selected.id)}
                />
              </div>

              {actionError && <div className="rounded-lg border border-rose-500/20 bg-rose-500/10 px-3 py-2 text-sm text-rose-200">{actionError}</div>}
              <ComputeProgress task={selected} />
              {selected.error && <div className="rounded-lg border border-rose-500/20 bg-black/15 p-3 text-sm text-rose-200">{selected.error}</div>}

              {selected.kind !== "composite" && selected.status === "planned" && preview.data && <ComputePlanSummary preview={preview.data} />}
              {selected.kind !== "composite" && selected.status === "planned" && preview.isError && (
                <div className="rounded-lg border border-rose-500/20 bg-rose-500/10 p-3 text-sm text-rose-200">計算計畫版本不相容，不能啟動。</div>
              )}

              {selected.kind === "composite" && (
                <StageList stages={children.data ?? []} loading={children.isLoading} onSelect={selectTask} />
              )}

              <TaskMetadata task={selected} />

              {selected.kind !== "composite" && (
                <div>
                  <Button variant="secondary" icon={ListTree} onClick={() => setShowItems((value) => !value)}>
                    {showItems ? "收合項目" : "載入項目明細"}
                  </Button>
                  {showItems && <ItemTable items={items.data ?? []} loading={items.isLoading} />}
                </div>
              )}
            </div>
          )}
        </section>
      </div>
    </div>
  );
}

function TaskRow({ task, active, onClick }: { task: ComputeTask; active: boolean; onClick: () => void }) {
  return (
    <button
      onClick={onClick}
      className={`w-full rounded-xl border p-3 text-left ${active ? "border-[#2dd4bf]/25 bg-[#2dd4bf]/[0.06]" : "border-white/[0.04] bg-black/10 hover:bg-white/[0.025]"}`}
    >
      <div className="flex items-start justify-between gap-2">
        <div className="min-w-0">
          <div className="truncate text-sm font-medium text-slate-200">{task.title}</div>
          <div className="mt-1 text-[11px] text-slate-600">#{task.id} · {task.kind === "composite" ? "複合研究" : "原子任務"}</div>
        </div>
        <ChevronRight className="mt-1 h-4 w-4 shrink-0 text-slate-700" />
      </div>
      <div className="mt-3 flex items-center justify-between gap-2">
        <ComputeStatusBadge status={task.status} />
        <span className="text-xs text-slate-600">{Math.round(task.progress * 100)}%</span>
      </div>
    </button>
  );
}

function TaskActions({
  task,
  previewReady,
  busy,
  onStart,
  onCancel,
  onRetry
}: {
  task: ComputeTask;
  previewReady: boolean;
  busy: boolean;
  onStart: () => void;
  onCancel: () => void;
  onRetry: () => void;
}) {
  const canStart = task.kind !== "composite" && task.status === "planned";
  const canCancel = task.status === "queued" || task.status === "running";
  const canRetry = ["failed", "cancelled"].includes(task.status) || (task.kind !== "composite" && task.status === "partial");
  return (
    <div className="flex flex-wrap gap-2">
      {canStart && <Button icon={Play} disabled={!previewReady || busy} onClick={onStart}>啟動本階段</Button>}
      {canCancel && <Button variant="danger" icon={Ban} disabled={busy} onClick={onCancel}>取消</Button>}
      {canRetry && <Button variant="secondary" icon={RotateCcw} disabled={busy} onClick={onRetry}>補算缺漏</Button>}
    </div>
  );
}

function StageList({ stages, loading, onSelect }: { stages: ComputeTask[]; loading: boolean; onSelect: (id: number) => void }) {
  return (
    <div className="rounded-xl border border-white/[0.06] bg-black/15 p-4">
      <div className="mb-3">
        <div className="text-sm font-semibold text-slate-200">研究階段</div>
        <div className="mt-1 text-xs text-slate-500">點入階段查看精確計算量；系統不會自動啟動下一階段。</div>
      </div>
      {loading && <div className="text-sm text-slate-500">讀取階段中…</div>}
      <div className="space-y-2">
        {stages.map((stage) => (
          <button key={stage.id} onClick={() => onSelect(stage.id)} className="flex w-full items-center gap-3 rounded-lg border border-white/[0.04] bg-white/[0.02] p-3 text-left hover:bg-white/[0.04]">
            <div className="flex h-8 w-8 shrink-0 items-center justify-center rounded-full border border-white/[0.06] text-xs text-slate-400">{stage.stage_order}</div>
            <div className="min-w-0 flex-1">
              <div className="truncate text-sm text-slate-200">{stage.title}</div>
              <div className="mt-1 text-xs text-slate-600">{stage.valid_result_count}/{stage.total_items} · 依賴 {stage.dependency_task_ids.length} 階段</div>
            </div>
            <ComputeStatusBadge status={stage.status} />
            <ChevronRight className="h-4 w-4 text-slate-700" />
          </button>
        ))}
      </div>
    </div>
  );
}

function TaskMetadata({ task }: { task: ComputeTask }) {
  const rows = useMemo(
    () => [
      ["Plan key", task.plan_key],
      ["Manifest", task.manifest_hash || "—"],
      ["設定 hash", task.settings_hash || "—"],
      ["任務／生命週期版本", `${task.task_schema_version} / ${task.lifecycle_version}`],
      ["執行器版本", task.executor.type ? `${task.executor.type} ${task.executor.version} / ${task.executor.result_schema_version}` : "父任務不直接執行"],
      ["亂數 checkpoint", task.rng_algorithm ? `${task.rng_algorithm} ${task.rng_version} · 位置 ${task.rng_position}` : "未使用亂數"],
      ["建立時間", formatTime(task.created_at)]
    ],
    [task]
  );
  return (
    <div className="rounded-xl border border-white/[0.06] bg-black/15 p-4">
      <div className="mb-3 text-sm font-semibold text-slate-200">追溯資訊</div>
      <dl className="grid gap-3 text-xs sm:grid-cols-2">
        {rows.map(([label, value]) => (
          <div key={label} className="min-w-0">
            <dt className="text-slate-600">{label}</dt>
            <dd className="mt-1 truncate font-mono text-slate-400" title={value}>{value}</dd>
          </div>
        ))}
      </dl>
    </div>
  );
}

function ItemTable({ items, loading }: { items: ComputeTaskItem[]; loading: boolean }) {
  if (loading) return <div className="mt-3 text-sm text-slate-500">讀取項目中…</div>;
  return (
    <div className="mt-3 overflow-x-auto rounded-xl border border-white/[0.06]">
      <table className="w-full min-w-[760px] text-left text-xs">
        <thead className="bg-white/[0.025] text-slate-500">
          <tr><th className="px-3 py-2">#</th><th className="px-3 py-2">項目</th><th className="px-3 py-2">狀態</th><th className="px-3 py-2">進度</th><th className="px-3 py-2">嘗試</th><th className="px-3 py-2">結果 hash</th></tr>
        </thead>
        <tbody>
          {items.map((item) => (
            <tr key={item.id} className="border-t border-white/[0.04] text-slate-400">
              <td className="px-3 py-2 text-slate-600">{item.index + 1}</td>
              <td className="max-w-[240px] truncate px-3 py-2 text-slate-300" title={item.key}>{item.key}</td>
              <td className="px-3 py-2">{item.status}</td>
              <td className="px-3 py-2">{Math.round(item.progress * 100)}%</td>
              <td className="px-3 py-2">{item.attempt}</td>
              <td className="max-w-[260px] truncate px-3 py-2 font-mono text-slate-600" title={item.result_hash}>{item.result_hash || item.error || "—"}</td>
            </tr>
          ))}
        </tbody>
      </table>
      {!items.length && <div className="p-4 text-sm text-slate-600">沒有項目。</div>}
    </div>
  );
}

function Pill({ label, value }: { label: string; value: string | number }) {
  return <div className="rounded-lg border border-white/[0.05] bg-white/[0.025] px-3 py-2"><span className="text-slate-600">{label}</span> <span className="text-slate-300">{value}</span></div>;
}

function EmptyState({ text, tone = "text-slate-600" }: { text: string; tone?: string }) {
  return <div className={`rounded-xl border border-dashed border-white/[0.06] p-6 text-center text-sm ${tone}`}>{text}</div>;
}

function formatTime(value?: string) {
  if (!value) return "—";
  return new Intl.DateTimeFormat("zh-TW", { dateStyle: "medium", timeStyle: "short" }).format(new Date(value));
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
