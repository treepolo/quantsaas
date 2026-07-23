import { useEffect, useMemo, useState } from "react";
import { Check, Pin, Search, X } from "lucide-react";
import { marketSourceCategory, marketSourceCategoryLabels, marketSourceCategoryOrder, marketSourceKey, type MarketSourceCategory } from "../lib/marketChartSources";
import type { MarketChartSource } from "../services/marketData";
import { Button } from "./Button";

type PickerMode = "chart" | "backtest";

type MarketSourcePickerProps = {
  sources: MarketChartSource[];
  value?: string;
  onChange: (source: MarketChartSource) => void;
  mode?: PickerMode;
  label?: string;
  disabled?: boolean;
  emptyLabel?: string;
  selectedKeys?: string[];
  onToggle?: (source: MarketChartSource) => void;
  maximumSelections?: number;
};

const preferenceKey = "quantsaas.market-source-picker.v1";

type Preferences = { pinned: string[]; recent: string[] };

function readPreferences(): Preferences {
  try {
    const value = JSON.parse(localStorage.getItem(preferenceKey) ?? "{}") as Partial<Preferences>;
    return { pinned: Array.isArray(value.pinned) ? value.pinned : [], recent: Array.isArray(value.recent) ? value.recent : [] };
  } catch { return { pinned: [], recent: [] }; }
}

function savePreferences(value: Preferences) {
  localStorage.setItem(preferenceKey, JSON.stringify(value));
}

function dateLabel(time: number) {
  return time > 0 ? new Date(time).toLocaleDateString("zh-TW", { timeZone: "UTC" }) : "資料範圍待確認";
}

function sourceTitle(source?: MarketChartSource) {
  if (!source) return "選擇行情資料";
  return `${source.display_name} · ${source.interval}`;
}

export function MarketSourcePicker({ sources, value, onChange, mode = "chart", label = "行情資料", disabled, emptyLabel = "目前沒有可選的行情資料", selectedKeys, onToggle, maximumSelections }: MarketSourcePickerProps) {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [category, setCategory] = useState<MarketSourceCategory | "all">("all");
  const [interval, setInterval] = useState("all");
  const [preferences, setPreferences] = useState(readPreferences);
  const available = useMemo(() => sources.filter((source) => mode !== "backtest" || source.can_backtest), [mode, sources]);
  const multi = Boolean(onToggle && selectedKeys);
  const selected = sources.find((source) => marketSourceKey(source) === value);
  const intervals = useMemo(() => Array.from(new Set(available.map((source) => source.interval))).sort(), [available]);
  const matches = useMemo(() => {
    const normalized = query.trim().toLocaleLowerCase();
    return available.filter((source) => {
      const searchable = [source.display_name, source.instrument.id, source.instrument.symbol, source.interval, source.series_name ?? "", marketSourceCategoryLabels[marketSourceCategory(source)]].join(" ").toLocaleLowerCase();
      return (!normalized || searchable.includes(normalized)) && (category === "all" || marketSourceCategory(source) === category) && (interval === "all" || source.interval === interval);
    });
  }, [available, category, interval, query]);
  const byKey = useMemo(() => new Map(matches.map((source) => [marketSourceKey(source), source])), [matches]);
  const pinned = preferences.pinned.map((key) => byKey.get(key)).filter((source): source is MarketChartSource => Boolean(source));
  const recent = preferences.recent.filter((key) => !preferences.pinned.includes(key)).map((key) => byKey.get(key)).filter((source): source is MarketChartSource => Boolean(source));
  const remaining = matches.filter((source) => !preferences.pinned.includes(marketSourceKey(source)) && !preferences.recent.includes(marketSourceKey(source)));

  useEffect(() => {
    if (!open) { setQuery(""); setCategory("all"); setInterval("all"); }
  }, [open]);

  function updatePreferences(next: Preferences) { setPreferences(next); savePreferences(next); }
  function choose(source: MarketChartSource) {
    const key = marketSourceKey(source);
    updatePreferences({ ...preferences, recent: [key, ...preferences.recent.filter((item) => item !== key)].slice(0, 12) });
    if (multi) { onToggle?.(source); return; }
    onChange(source);
    setOpen(false);
  }
  function togglePin(key: string) {
    updatePreferences({ ...preferences, pinned: preferences.pinned.includes(key) ? preferences.pinned.filter((item) => item !== key) : [...preferences.pinned, key] });
  }

  return <div className="relative">
    <span className="mb-1 block text-xs text-slate-400">{label}</span>
    <button type="button" disabled={disabled || available.length === 0} onClick={() => setOpen(true)} className="flex min-h-[62px] w-full items-center justify-between gap-4 rounded-lg border border-slate-700 bg-slate-900/80 px-4 py-2.5 text-left text-sm text-slate-100 outline-none transition hover:border-teal-400/50 focus:border-teal-400/70 disabled:opacity-50">
      <span className="min-w-0"><span className="block truncate font-medium">{multi ? `已選 ${selectedKeys?.length ?? 0}${maximumSelections ? `／${maximumSelections}` : ""} 組資料` : sourceTitle(selected)}</span>{multi ? <span className="mt-1 block truncate text-xs leading-5 text-slate-500">點選搜尋結果即可加入或移除副走勢。</span> : selected ? <span className="mt-1 block truncate text-xs leading-5 text-slate-500">{marketSourceCategoryLabels[marketSourceCategory(selected)]} · {dateLabel(selected.start_time_ms)} 至 {dateLabel(selected.end_time_ms)}</span> : null}</span>
      <Search className="h-4 w-4 shrink-0 text-slate-400" />
    </button>
    {!available.length ? <span className="mt-1 block text-xs text-slate-500">{emptyLabel}</span> : null}
    {open ? <div className="fixed inset-0 z-50 flex items-start justify-center bg-slate-950/75 p-4 pt-[8vh]" role="dialog" aria-modal="true" aria-label={label} onMouseDown={() => setOpen(false)}>
      <div className="max-h-[84vh] w-full max-w-3xl overflow-hidden rounded-xl border border-white/10 bg-slate-950 shadow-2xl" onMouseDown={(event) => event.stopPropagation()}>
        <div className="flex items-center justify-between border-b border-white/10 px-5 py-4"><div><div className="font-semibold text-slate-100">選擇{label}</div><div className="mt-1 text-xs text-slate-500">{multi ? `可選最多 ${maximumSelections ?? "多"} 組；點選結果即可加入或移除。` : "搜尋名稱、代號或週期；常用資料可釘選在最上方。"}</div></div><Button size="sm" variant="ghost" icon={X} onClick={() => setOpen(false)}>完成</Button></div>
        <div className="border-b border-white/10 p-4"><label className="flex items-center gap-2 rounded-lg border border-white/10 bg-white/[.03] px-3"><Search className="h-4 w-4 text-slate-500"/><input autoFocus value={query} onChange={(event) => setQuery(event.target.value)} placeholder="例如：SOXL、日 K、局部擾動" className="h-10 min-w-0 flex-1 bg-transparent text-sm text-slate-100 outline-none placeholder:text-slate-600"/></label><div className="mt-3 flex flex-wrap gap-2"><select value={category} onChange={(event) => setCategory(event.target.value as MarketSourceCategory | "all")} className="min-h-9 rounded-md border border-white/10 bg-slate-900 px-2 text-xs text-slate-200"><option value="all">所有類別</option>{marketSourceCategoryOrder.map((item) => <option key={item} value={item}>{marketSourceCategoryLabels[item]}</option>)}</select><select value={interval} onChange={(event) => setInterval(event.target.value)} className="min-h-9 rounded-md border border-white/10 bg-slate-900 px-2 text-xs text-slate-200"><option value="all">所有週期</option>{intervals.map((item) => <option key={item} value={item}>{item}</option>)}</select><span className="self-center text-xs text-slate-500">{matches.length.toLocaleString("zh-TW")} 筆可選資料</span></div></div>
        <div className="max-h-[56vh] overflow-y-auto p-3"><SourceSection title="已釘選" sources={pinned} selectedKey={value} selectedKeys={selectedKeys} pinned={preferences.pinned} onChoose={choose} onTogglePin={togglePin}/><SourceSection title="最近使用" sources={recent} selectedKey={value} selectedKeys={selectedKeys} pinned={preferences.pinned} onChoose={choose} onTogglePin={togglePin}/><SourceSection title="所有結果" sources={remaining} selectedKey={value} selectedKeys={selectedKeys} pinned={preferences.pinned} onChoose={choose} onTogglePin={togglePin} empty={matches.length === 0 ? "沒有符合條件的行情資料。" : undefined}/></div>
      </div>
    </div> : null}
  </div>;
}

function SourceSection({ title, sources, selectedKey, selectedKeys, pinned, onChoose, onTogglePin, empty }: { title: string; sources: MarketChartSource[]; selectedKey?: string; selectedKeys?: string[]; pinned: string[]; onChoose: (source: MarketChartSource) => void; onTogglePin: (key: string) => void; empty?: string }) {
  if (!sources.length && !empty) return null;
  return <section className="mb-4 last:mb-0"><div className="mb-1.5 px-2 text-xs font-semibold text-slate-400">{title}</div>{sources.length ? <div className="space-y-1">{sources.map((source) => { const key = marketSourceKey(source); const active = selectedKeys ? selectedKeys.includes(key) : key === selectedKey; return <div key={key} className={`flex items-center gap-2 rounded-lg border p-2 transition ${active ? "border-teal-400/40 bg-teal-400/10" : "border-transparent hover:bg-white/[.04]"}`}><button type="button" onClick={() => onChoose(source)} className="min-w-0 flex-1 text-left"><span className="flex items-center gap-2"><span className="truncate text-sm text-slate-200">{source.display_name}</span>{active ? <Check className="h-4 w-4 shrink-0 text-teal-300"/> : null}</span><span className="mt-0.5 block truncate text-xs text-slate-500">{source.instrument.symbol} · {source.interval} · {marketSourceCategoryLabels[marketSourceCategory(source)]} · {dateLabel(source.start_time_ms)} 至 {dateLabel(source.end_time_ms)}{source.bar_count ? ` · ${source.bar_count.toLocaleString("zh-TW")} 根` : ""}</span></button><button type="button" title={pinned.includes(key) ? "取消釘選" : "釘選"} aria-label={pinned.includes(key) ? "取消釘選" : "釘選"} onClick={() => onTogglePin(key)} className={`rounded p-2 ${pinned.includes(key) ? "text-amber-300" : "text-slate-600 hover:text-slate-300"}`}><Pin className="h-4 w-4" fill={pinned.includes(key) ? "currentColor" : "none"}/></button></div>; })}</div> : <div className="rounded-lg border border-dashed border-white/10 px-3 py-5 text-sm text-slate-500">{empty}</div>}</section>;
}
