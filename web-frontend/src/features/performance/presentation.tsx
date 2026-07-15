import type { ReactNode } from "react";
import { cn } from "../../shared/lib/cn";

export function finiteNumber(value: number | null | undefined, digits = 4) {
  if (value === undefined || value === null || !Number.isFinite(value)) return "不可計算";
  return value.toLocaleString("zh-TW", { maximumFractionDigits: digits });
}

export function percent(value: number | null | undefined, digits = 2) {
  if (value === undefined || value === null || !Number.isFinite(value)) return "不可計算";
  return `${(value * 100).toLocaleString("zh-TW", { minimumFractionDigits: digits, maximumFractionDigits: digits })}%`;
}

export function reportDate(value?: string) {
  if (!value) return "尚未完成";
  return new Intl.DateTimeFormat("zh-TW", { year: "numeric", month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit" }).format(new Date(value));
}

export function utcDate(value?: number) {
  if (!value) return "-";
  return new Intl.DateTimeFormat("zh-TW", { timeZone: "UTC", year: "numeric", month: "2-digit", day: "2-digit" }).format(new Date(value));
}

export function StatTile({ label, value, note, tone }: { label: string; value: ReactNode; note?: ReactNode; tone?: "normal" | "good" | "danger" }) {
  return (
    <div className="rounded-lg border border-white/[0.05] bg-slate-950/35 p-3">
      <div className="text-xs text-slate-500">{label}</div>
      <div className={cn("mt-1 font-mono text-base font-semibold", tone === "good" ? "text-[#99f6e4]" : tone === "danger" ? "text-[#fecaca]" : "text-slate-100")}>{value}</div>
      {note ? <div className="mt-1 text-xs text-slate-500">{note}</div> : null}
    </div>
  );
}
