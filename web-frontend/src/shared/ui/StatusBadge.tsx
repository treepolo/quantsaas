import { useI18n } from "../../i18n/useI18n";
import { cn } from "../lib/cn";

type StatusBadgeProps = {
  status?: string | null;
  className?: string;
};

function normalizeStatus(status?: string | null) {
  const normalized = (status ?? "stopped").toLowerCase();
  if (normalized === "running") return "running";
  if (normalized === "error") return "error";
  if (normalized === "halted") return "halted";
  if (normalized === "paused") return "paused";
  if (normalized === "pending") return "pending";
  if (normalized === "completed") return "completed";
  if (normalized === "failed") return "failed";
  if (normalized === "cancelled") return "cancelled";
  return "stopped";
}

const statusClasses: Record<string, string> = {
  running: "border-[#2dd4bf]/30 bg-[#2dd4bf]/10 text-[#99f6e4] before:bg-[#2dd4bf]",
  stopped: "border-slate-500/30 bg-slate-500/10 text-slate-300 before:bg-slate-400",
  paused: "border-[#fbbf24]/30 bg-[#fbbf24]/10 text-[#fde68a] before:bg-[#fbbf24]",
  error: "border-[#f87171]/30 bg-[#f87171]/10 text-[#fecaca] before:bg-[#f87171]",
  halted: "border-[#f87171]/30 bg-[#f87171]/10 text-[#fecaca] before:bg-[#f87171]",
  pending: "border-[#0ea5e9]/30 bg-[#0ea5e9]/10 text-[#bae6fd] before:bg-[#0ea5e9]",
  completed: "border-[#34d399]/30 bg-[#34d399]/10 text-[#bbf7d0] before:bg-[#34d399]",
  failed: "border-[#f87171]/30 bg-[#f87171]/10 text-[#fecaca] before:bg-[#f87171]",
  cancelled: "border-slate-500/30 bg-slate-500/10 text-slate-300 before:bg-slate-400"
};

const statusLabels: Record<string, string> = {
  cancelled: "已中止"
};

export function StatusBadge({ status, className }: StatusBadgeProps) {
  const { t } = useI18n();
  const normalized = normalizeStatus(status);
  return (
    <span
      className={cn(
        "inline-flex items-center gap-2 rounded-full border px-2.5 py-1 text-xs font-medium before:block before:h-1.5 before:w-1.5 before:rounded-full",
        statusClasses[normalized],
        className
      )}
    >
      {statusLabels[normalized] ?? t(`status.${normalized}`)}
    </span>
  );
}
