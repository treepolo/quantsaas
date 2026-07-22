import { useEffect, useRef, useState, type ReactNode } from "react";
import type { ComputePlanPreview, ComputeTask, ComputeTaskStatus } from "../../shared/services/computeTasks";

const statusLabels: Record<ComputeTaskStatus, string> = {
  planned: "尚未啟動",
  queued: "等待執行",
  running: "計算中",
  partial: "部分完成",
  completed: "已完成",
  failed: "失敗",
  cancelled: "已取消",
  invalidated: "版本失效"
};

const statusClasses: Record<ComputeTaskStatus, string> = {
  planned: "border-slate-700 bg-slate-900 text-slate-300",
  queued: "border-sky-500/25 bg-sky-500/10 text-sky-300",
  running: "border-amber-500/25 bg-amber-500/10 text-amber-300",
  partial: "border-violet-500/25 bg-violet-500/10 text-violet-300",
  completed: "border-teal-500/25 bg-teal-500/10 text-teal-300",
  failed: "border-rose-500/25 bg-rose-500/10 text-rose-300",
  cancelled: "border-slate-600 bg-slate-800 text-slate-300",
  invalidated: "border-orange-500/25 bg-orange-500/10 text-orange-300"
};

export function ComputeStatusBadge({ status }: { status: ComputeTaskStatus }) {
  return <span className={`rounded-full border px-2.5 py-1 text-xs font-medium ${statusClasses[status]}`}>{statusLabels[status]}</span>;
}

export function ComputeProgress({ task, compact = false }: { task: ComputeTask; compact?: boolean }) {
  const progress = Number.isFinite(task.progress) ? task.progress : 0;
  const completed = finiteCount(task.valid_result_count);
  const total = finiteCount(task.total_items);
  const percent = Math.max(0, Math.min(100, progress * 100));
  return (
    <div className="space-y-2">
      <div className="h-2 overflow-hidden rounded-full bg-white/[0.05]">
        <div className="h-full bg-[#2dd4bf] transition-[width] duration-[1200ms] ease-out" style={{ width: `${percent}%` }} />
      </div>
      <div className="flex flex-wrap items-center justify-between gap-2 text-xs text-slate-500">
        <span>
          {completed.toLocaleString()} / {total.toLocaleString()} 已完成
        </span>
        <span>{percent.toFixed(percent >= 10 ? 0 : 1)}%</span>
      </div>
      {!compact && (
        <div className="grid grid-cols-3 gap-2 text-xs">
          <Count label="錯誤" value={finiteCount(task.failed_count)} tone="text-rose-300" />
          <Count label="缺漏" value={finiteCount(task.missing_count)} />
          <Count label="快取" value={finiteCount(task.cache_hit_count)} tone="text-teal-300" />
        </div>
      )}
      {task.compute_monitor_enabled && (
        <div className="mt-2 grid gap-2 text-xs sm:grid-cols-3">
          <Count label="本次計算量" value={<><AnimatedInteger value={finiteCount(task.computed_units)} /> / {finiteCount(task.planned_compute_units ?? task.estimated_units).toLocaleString()}</>} />
          <Count label="計算速度" value={`${finiteCount(task.compute_units_per_sec)} 單位／秒`} />
          <Count label="預估剩餘" value={task.compute_remaining_sec && task.compute_remaining_sec > 0 ? `${Math.ceil(task.compute_remaining_sec)} 秒` : "未知"} />
        </div>
      )}
      {task.compute_monitor_enabled && (task.compute_current_stage || task.compute_last_heartbeat) && (
        <div className="text-xs text-slate-500">目前階段：{task.compute_current_stage || "執行中"} · 最後心跳：{task.compute_last_heartbeat ? new Date(task.compute_last_heartbeat).toLocaleTimeString() : "未知"}</div>
      )}
    </div>
  );
}

function finiteCount(value: number | undefined) {
  return Number.isFinite(value) ? Math.max(0, value as number) : 0;
}

export function ComputePlanSummary({ preview }: { preview: ComputePlanPreview }) {
  return (
    <div className="rounded-xl border border-white/[0.06] bg-black/15 p-4">
      <div className="mb-3 flex items-center justify-between gap-3">
        <div>
          <div className="text-sm font-semibold text-slate-200">本階段計算計畫</div>
          <div className="mt-1 text-xs text-slate-500">固定 manifest，啟動後不會擴張</div>
        </div>
        {preview.requires_confirmation && (
          <span className="rounded-full border border-amber-500/25 bg-amber-500/10 px-2.5 py-1 text-xs text-amber-300">超過建議量</span>
        )}
      </div>
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-5">
        <Metric label="總項目" value={preview.total_items.toLocaleString()} />
        <Metric label="可重用快取" value={preview.cache_hit_count.toLocaleString()} tone="text-teal-300" />
        <Metric label="實際新增" value={preview.new_item_count.toLocaleString()} tone="text-amber-300" />
        <Metric
          label="預估工作量"
          value={preview.unknown_unit_items > 0 ? "部分未知" : preview.estimated_units.toLocaleString()}
        />
        <Metric label="預估耗時" value={preview.estimated_seconds === undefined ? "未知" : formatDuration(preview.estimated_seconds)} />
      </div>
      <div className="mt-3 grid gap-1 text-[11px] text-slate-600">
        <HashLine label="Manifest" value={preview.manifest_hash} />
        <HashLine label="Plan" value={preview.plan_key} />
        <div>
          建議上限 {preview.soft_item_limit.toLocaleString()} · 硬上限 {preview.hard_item_limit.toLocaleString()}
        </div>
      </div>
    </div>
  );
}

function formatDuration(seconds: number) {
  if (seconds < 60) return `${Math.ceil(seconds)} 秒`;
  if (seconds < 3600) return `${Math.ceil(seconds / 60)} 分鐘`;
  return `${(seconds / 3600).toFixed(1)} 小時`;
}

function Metric({ label, value, tone = "text-slate-100" }: { label: string; value: string; tone?: string }) {
  return (
    <div className="rounded-lg border border-white/[0.04] bg-white/[0.025] p-3">
      <div className="text-[11px] text-slate-500">{label}</div>
      <div className={`mt-1 text-lg font-semibold ${tone}`}>{value}</div>
    </div>
  );
}

function Count({ label, value, tone = "text-slate-400" }: { label: string; value: ReactNode; tone?: string }) {
  return (
    <div className="rounded-md bg-white/[0.025] px-2 py-1.5">
      <span className="text-slate-600">{label}</span> <span className={tone}>{value}</span>
    </div>
  );
}

function AnimatedInteger({ value }: { value: number }) {
  const [displayed, setDisplayed] = useState(value);
  const current = useRef(value);
  useEffect(() => {
    const from = current.current;
    const started = performance.now();
    const duration = 1200;
    let frame = 0;
    const animate = (now: number) => {
      const progress = Math.min(1, (now - started) / duration);
      const eased = 1 - Math.pow(1 - progress, 3);
      const next = from + (value - from) * eased;
      current.current = next;
      setDisplayed(next);
      if (progress < 1) frame = requestAnimationFrame(animate);
    };
    frame = requestAnimationFrame(animate);
    return () => cancelAnimationFrame(frame);
  }, [value]);
  return <span>{Math.round(displayed).toLocaleString()}</span>;
}

function HashLine({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex min-w-0 gap-2">
      <span className="shrink-0">{label}</span>
      <code className="truncate">{value}</code>
    </div>
  );
}
