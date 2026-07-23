import { FormEvent, useEffect, useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { ArrowDown, ArrowUp, CheckCircle2, ChevronDown, Database, Plus, RefreshCw, Search, Trash2, TriangleAlert, Wrench } from "lucide-react";
import { marketSourceCategory, marketSourceCategoryLabels, marketSourceKey } from "../../shared/lib/marketChartSources";
import { marketDataApi, type DatasetSummary, type MarketChartSource, type ResearchInstrument, type UpsertInstrumentInput } from "../../shared/services/marketData";
import { Button } from "../../shared/ui/Button";
import { Card, CardDescription, CardHeader, CardTitle } from "../../shared/ui/Card";
import { cn } from "../../shared/lib/cn";
import { MarketPriceExplorer } from "./MarketPriceExplorer";

const intervalLabels: Record<string, string> = { "1d": "日 K", "1h": "1 小時 K", "15m": "15 分 K", "5m": "5 分 K", "1m": "1 分 K", "1s": "1 秒 K", "1w": "週 K", "1M": "月 K" };
const yahooIntervals = ["1d", "1h", "1m", "1w", "1M"];
const binanceIntervals = ["1d", "1h", "15m", "5m", "1m", "1s", "1w", "1M"];
const fredIntervals = ["1d"];
const builtinFredIndicators = [
  { id: "UNRATE", symbol: "UNRATE", display_name: "美國失業率" },
  { id: "SOFR", symbol: "SOFR", display_name: "SOFR 擔保隔夜融資利率" },
  { id: "BAMLH0A0HYM2", symbol: "BAMLH0A0HYM2", display_name: "美國高收益債信用利差" }
];
type SourceTab = "original" | "reference" | "research";
type StatusFilter = "all" | "attention" | "fresh";
const inputClass = "min-h-10 w-full rounded-lg border border-white/10 bg-slate-950/70 px-3 text-sm text-slate-200 outline-none focus:border-teal-400/50";

function intervalsForSource(source?: string) { return source === "binance" ? binanceIntervals : source === "fred" ? fredIntervals : yahooIntervals; }
function dateInputValue(date: Date) { return date.toISOString().slice(0, 10); }
function dayStartMs(value: string) { return new Date(`${value}T00:00:00.000Z`).getTime(); }
function dayEndMs(value: string) { return new Date(`${value}T23:59:59.999Z`).getTime(); }
function defaultStart(instrument?: ResearchInstrument, interval = "1d") {
  const detected = instrument?.available_start_ms?.[interval];
  if (detected && detected > 0) return dateInputValue(new Date(detected));
  const now = new Date();
  if (interval === "1m") return dateInputValue(new Date(Date.now() - 7 * 24 * 60 * 60 * 1000));
  if (interval === "1h") return dateInputValue(new Date(Date.UTC(now.getUTCFullYear() - 2, now.getUTCMonth(), now.getUTCDate())));
  if (interval === "1s") return dateInputValue(new Date(Date.now() - 24 * 60 * 60 * 1000));
  if (instrument?.data_source === "binance" && interval === "1d") return "2017-08-17";
  return dateInputValue(new Date(Date.UTC(now.getUTCFullYear() - 10, now.getUTCMonth(), now.getUTCDate())));
}
function formatMs(value?: number) { return value ? new Intl.DateTimeFormat("zh-TW", { year: "numeric", month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", hour12: false }).format(new Date(value)) : "尚無資料"; }
function marketName(market?: string) { return market === "tw" ? "台股" : market === "us" ? "美股" : market === "crypto" ? "加密貨幣" : market === "macro" ? "總經指標" : market || "其他"; }
function timeLabelPrefix(interval: string) { return interval === "1w" || interval === "1M" ? "期間起點" : "開始時間"; }
function InfoRow({ label, value }: { label: string; value: string }) { return <div className="flex items-center justify-between gap-3 text-sm"><span className="text-slate-500">{label}</span><span className="text-right font-mono text-slate-200">{value}</span></div>; }

function DatasetCard({ dataset }: { dataset: DatasetSummary }) {
  const fresh = Boolean(dataset.is_fresh);
  return <div className={cn("rounded-lg border p-3", fresh ? "border-teal-400/20 bg-teal-400/[0.03]" : "border-amber-400/25 bg-amber-400/[0.04]")}>
    <div className="flex items-center justify-between gap-3"><div className="font-semibold text-slate-100">{intervalLabels[dataset.interval] ?? dataset.interval}</div>{fresh ? <CheckCircle2 className="h-4 w-4 text-teal-300" /> : <TriangleAlert className="h-4 w-4 text-amber-300" />}</div>
    <div className="mt-3 grid gap-2 text-xs"><InfoRow label="筆數" value={dataset.count.toLocaleString("zh-TW")} /><InfoRow label={`第一筆${timeLabelPrefix(dataset.interval)}`} value={formatMs(dataset.first_open_ms)} /><InfoRow label={`最後一筆${timeLabelPrefix(dataset.interval)}`} value={formatMs(dataset.last_open_ms)} /><InfoRow label="價格口徑" value={dataset.price_adjustment_label ?? "未記錄"} /></div>
    {dataset.needs_full_reimport ? <div className="mt-3 text-xs leading-5 text-amber-200">此資料為舊口徑或未知口徑；完整重匯會改變使用它的既有回測結果。</div> : null}
  </div>;
}

export function MarketDataPage() {
  const queryClient = useQueryClient();
  const instrumentsQuery = useQuery({ queryKey: ["market-data-instruments"], queryFn: () => marketDataApi.instruments() });
  const overviewQuery = useQuery({ queryKey: ["market-data-overview"], queryFn: () => marketDataApi.overview() });
  const chartSourcesQuery = useQuery({ queryKey: ["market-chart-sources"], queryFn: marketDataApi.chartSources });
  const instruments = instrumentsQuery.data?.instruments ?? [];
  const [instrumentId, setInstrumentId] = useState("BTCUSDT");
  const selected = instruments.find((item) => item.id === instrumentId) ?? instruments[0];
  const [interval, setInterval] = useState("1d");
  const [startDate, setStartDate] = useState(defaultStart(undefined, "1d"));
  const [endDate, setEndDate] = useState(dateInputValue(new Date()));
  const [includePreclose, setIncludePreclose] = useState(false);
  const [sourceTab, setSourceTab] = useState<SourceTab>("original");
  const [marketFilter, setMarketFilter] = useState("all");
  const [statusFilter, setStatusFilter] = useState<StatusFilter>("all");
  const [search, setSearch] = useState("");
  const [selectedResearchKey, setSelectedResearchKey] = useState("");
  const [newInstrument, setNewInstrument] = useState<UpsertInstrumentInput>({ symbol: "", display_name: "", data_source: "yahoo", market: "us", supported_intervals: yahooIntervals, sort_order: 1000 });

  useEffect(() => { if (!selected && instruments[0]) setInstrumentId(instruments[0].id); }, [instruments, selected]);
  const statusQuery = useQuery({ queryKey: ["market-data", selected?.id ?? instrumentId], queryFn: () => marketDataApi.status(selected?.id ?? instrumentId), enabled: Boolean(selected?.id ?? instrumentId) });
  const intervals = statusQuery.data?.supported_intervals ?? selected?.supported_intervals ?? ["1d"];
  const datasets = useMemo(() => statusQuery.data?.datasets ?? [], [statusQuery.data]);
  const selectedDataset = datasets.find((dataset) => dataset.interval === interval);

  function refreshMarketQueries() {
    queryClient.invalidateQueries({ queryKey: ["market-data-instruments"] });
    queryClient.invalidateQueries({ queryKey: ["market-data-overview"] });
    queryClient.invalidateQueries({ queryKey: ["market-chart-sources"] });
    queryClient.invalidateQueries({ queryKey: ["market-data"] });
    queryClient.invalidateQueries({ queryKey: ["research-status"] });
  }
  const importMutation = useMutation({ mutationFn: () => marketDataApi.importKLines({ instrument_id: selected?.id ?? instrumentId, data_source: selected?.data_source, symbol: selected?.symbol ?? instrumentId, interval, start_time_ms: dayStartMs(startDate), end_time_ms: dayEndMs(endDate), include_preclose_snapshots: includePreclose && interval === "1d" }), onSuccess: refreshMarketQueries });
  const upsertMutation = useMutation({ mutationFn: () => marketDataApi.upsertInstrument(newInstrument), onSuccess: (instrument) => { setInstrumentId(instrument.id); setNewInstrument({ symbol: "", display_name: "", data_source: "yahoo", market: "us", supported_intervals: yahooIntervals, sort_order: 1000 }); refreshMarketQueries(); } });
  const deleteMutation = useMutation({ mutationFn: (id: string) => marketDataApi.deleteInstrument(id), onSuccess: refreshMarketQueries });
  const reorderMutation = useMutation({ mutationFn: (ids: string[]) => marketDataApi.reorderInstruments(ids), onSuccess: refreshMarketQueries });
  const refreshStartsMutation = useMutation({ mutationFn: () => marketDataApi.refreshInstrumentStarts(selected?.id ?? instrumentId), onSuccess: (result) => { refreshMarketQueries(); const detected = result.starts?.[interval]; if (detected && detected > 0) setStartDate(dateInputValue(new Date(detected))); } });
  const updateLatestMutation = useMutation({ mutationFn: marketDataApi.updateLatest, onSuccess: refreshMarketQueries });
  const auditMaintenanceMutation = useMutation({ mutationFn: () => marketDataApi.auditMaintenance(selected?.id ?? instrumentId), onSuccess: refreshMarketQueries });
  const repairMaintenanceMutation = useMutation({ mutationFn: () => marketDataApi.repairMaintenance(selected?.id ?? instrumentId), onSuccess: refreshMarketQueries });
  const auditAllMaintenanceMutation = useMutation({ mutationFn: () => marketDataApi.auditMaintenance(), onSuccess: refreshMarketQueries });
  const repairAllMaintenanceMutation = useMutation({ mutationFn: () => marketDataApi.repairMaintenance(), onSuccess: refreshMarketQueries });

  function submit(event: FormEvent<HTMLFormElement>) { event.preventDefault(); if (selectedDataset?.needs_full_reimport && !window.confirm(`目前 ${selected?.display_name ?? selected?.symbol ?? instrumentId} 的 ${intervalLabels[interval] ?? interval} 是舊口徑或未知口徑。完整重匯會改變既有回測結果。確定繼續？`)) return; importMutation.mutate(); }
  function changeInstrument(nextId: string) { const next = instruments.find((item) => item.id === nextId); const nextInterval = next?.supported_intervals[0] ?? "1d"; setInstrumentId(nextId); setInterval(nextInterval); setStartDate(defaultStart(next, nextInterval)); setSelectedResearchKey(""); }
  function changeInterval(next: string) { setInterval(next); setStartDate(defaultStart(selected, next)); }
  function changeSource(source: string) { setNewInstrument((current) => ({ ...current, data_source: source, market: source === "binance" ? "crypto" : source === "fred" ? "macro" : current.market || "us", supported_intervals: intervalsForSource(source) })); }
  function toggleNewInterval(value: string) { setNewInstrument((current) => { const values = current.supported_intervals ?? []; return { ...current, supported_intervals: values.includes(value) ? values.filter((item) => item !== value) : [...values, value] }; }); }
  function moveInstrument(index: number, delta: number) { const nextIndex = index + delta; if (nextIndex < 0 || nextIndex >= instruments.length) return; const ids = instruments.map((item) => item.id); const [moved] = ids.splice(index, 1); ids.splice(nextIndex, 0, moved); reorderMutation.mutate(ids); }
  function repairSelectedMaintenance() { if (window.confirm("這會檢查並修復目前選取商品的日 K、週 K、月 K；使用這批資料的舊回測結果可能改變。確定繼續？")) repairMaintenanceMutation.mutate(); }
  function repairAllMaintenance() { if (window.confirm("這會檢查並修復所有商品的日 K、週 K、月 K，可能花較久；使用這些資料的舊回測結果可能改變。確定繼續？")) repairAllMaintenanceMutation.mutate(); }

  const overviewItems = overviewQuery.data?.items ?? [];
  const filteredInstruments = useMemo(() => overviewItems.filter((item) => {
    const haystack = `${item.instrument.display_name} ${item.instrument.symbol} ${item.instrument.id}`.toLowerCase();
    const attention = Boolean(item.instrument.last_auto_update_error) || item.datasets.some((dataset) => !dataset.is_fresh);
    const tabMatches = sourceTab === "reference" ? item.instrument.data_source === "fred" : sourceTab === "original" ? item.instrument.data_source !== "fred" && item.instrument.data_source !== "generated" : false;
    return tabMatches && (marketFilter === "all" || item.instrument.market === marketFilter) && (statusFilter === "all" || (statusFilter === "attention" ? attention : !attention)) && haystack.includes(search.trim().toLowerCase());
  }).sort((a, b) => {
    const rank = (item: typeof a) => Boolean(item.instrument.last_auto_update_error) || item.datasets.some((dataset) => !dataset.is_fresh) ? 0 : item.instrument.id === selected?.id ? 1 : 2;
    return rank(a) - rank(b) || (a.instrument.sort_order ?? 9999) - (b.instrument.sort_order ?? 9999);
  }), [overviewItems, sourceTab, marketFilter, statusFilter, search, selected?.id]);
  const researchSources = useMemo(() => (chartSourcesQuery.data?.items ?? []).filter((source) => !["original", "reference"].includes(marketSourceCategory(source))), [chartSourcesQuery.data]);
  const updateSummary = useMemo(() => { const rows = overviewItems.flatMap((item) => item.datasets); const errors = overviewItems.filter((item) => item.instrument.last_auto_update_error).length; const stale = rows.filter((item) => !item.is_fresh).length; return { rows: rows.length, errors, stale }; }, [overviewItems]);
  const maintenanceResults = repairAllMaintenanceMutation.data?.results ?? auditAllMaintenanceMutation.data?.results ?? repairMaintenanceMutation.data?.results ?? auditMaintenanceMutation.data?.results ?? [];

  return <section className="space-y-4">
    <div className="flex flex-wrap items-start justify-between gap-4"><div><h1 className="text-2xl font-bold text-slate-100">行情資料</h1><p className="mt-1 text-sm text-slate-400">先看資料是否可用，再在需要時進行匯入、修復與商品管理。</p></div><Button icon={RefreshCw} loading={updateLatestMutation.isPending} onClick={() => updateLatestMutation.mutate()}>更新全部</Button></div>
    <div className={cn("rounded-lg border px-4 py-3 text-sm", updateSummary.errors || updateSummary.stale ? "border-amber-400/25 bg-amber-400/[0.06] text-amber-100" : "border-teal-400/20 bg-teal-400/[0.05] text-teal-100")}>{updateSummary.errors ? `有 ${updateSummary.errors} 個商品更新異常。` : updateSummary.stale ? `有 ${updateSummary.stale} 組資料待更新。` : updateSummary.rows ? `${updateSummary.rows} 組資料皆為最新。` : "正在載入資料狀態…"}</div>
    {updateLatestMutation.data ? <div className="rounded-lg border border-teal-400/20 bg-teal-400/[0.05] px-4 py-3 text-sm text-teal-100">全部更新完成；請從左側清單查看各商品狀態。</div> : null}
    {updateLatestMutation.error ? <div className="text-sm text-rose-300">{String(updateLatestMutation.error.message)}</div> : null}

    <div className="grid gap-4 xl:grid-cols-[330px_minmax(0,1fr)]">
      <Card className="h-fit p-3 xl:sticky xl:top-4">
        <div className="mb-3 flex rounded-lg border border-white/10 bg-slate-950/50 p-1">{([ ["original", "原始行情"], ["reference", "參考指標"], ["research", "研究行情"] ] as [SourceTab, string][]).map(([value, label]) => <button key={value} type="button" onClick={() => setSourceTab(value)} className={cn("flex-1 rounded-md px-2 py-2 text-xs", sourceTab === value ? "bg-teal-400/15 text-teal-200" : "text-slate-400 hover:bg-white/[0.04]")}>{label}</button>)}</div>
        <label className="relative block"><Search className="pointer-events-none absolute left-3 top-3 h-4 w-4 text-slate-500" /><input className={`${inputClass} pl-9`} value={search} onChange={(event) => setSearch(event.target.value)} placeholder="搜尋商品或代號" /></label>
        <div className="mt-2 grid grid-cols-2 gap-2"><select className={inputClass} value={marketFilter} onChange={(event) => setMarketFilter(event.target.value)}><option value="all">所有市場</option><option value="tw">台股</option><option value="us">美股</option><option value="crypto">加密貨幣</option><option value="macro">總經指標</option></select><select className={inputClass} value={statusFilter} onChange={(event) => setStatusFilter(event.target.value as StatusFilter)}><option value="all">所有狀態</option><option value="attention">需處理</option><option value="fresh">已更新</option></select></div>
        <div className="mt-3 max-h-[65vh] space-y-1 overflow-auto pr-1">
          {sourceTab !== "research" ? filteredInstruments.map((item) => { const active = item.instrument.id === selected?.id && !selectedResearchKey; const attention = Boolean(item.instrument.last_auto_update_error) || item.datasets.some((dataset) => !dataset.is_fresh); return <button key={item.instrument.id} type="button" onClick={() => changeInstrument(item.instrument.id)} className={cn("w-full rounded-lg border p-3 text-left transition", active ? "border-teal-400/35 bg-teal-400/[0.08]" : "border-white/[0.06] bg-white/[0.02] hover:bg-white/[0.05]")}><div className="flex items-start justify-between gap-2"><div className="min-w-0"><div className="truncate text-sm font-semibold text-slate-100">{item.instrument.display_name}</div><div className="mt-0.5 truncate font-mono text-xs text-slate-500">{item.instrument.symbol} · {marketName(item.instrument.market)}</div></div><span className={cn("shrink-0 rounded-full px-2 py-1 text-[11px]", attention ? "bg-amber-400/10 text-amber-200" : "bg-teal-400/10 text-teal-200")}>{attention ? "需處理" : "已更新"}</span></div><div className="mt-2 flex flex-wrap gap-1">{item.datasets.map((dataset) => <span key={dataset.interval} className={cn("rounded px-1.5 py-0.5 text-[11px]", dataset.is_fresh ? "bg-white/[0.05] text-slate-400" : "bg-amber-400/10 text-amber-200")}>{intervalLabels[dataset.interval] ?? dataset.interval}</span>)}</div></button>; }) : <ResearchList sources={researchSources} selectedKey={selectedResearchKey} search={search} onSelect={(source) => { setSelectedResearchKey(marketSourceKey(source)); }} />}
          {sourceTab !== "research" && !overviewQuery.isLoading && !filteredInstruments.length ? <div className="p-5 text-center text-sm text-slate-500">沒有符合篩選條件的資料。</div> : null}
        </div>
        {sourceTab === "research" ? <Link to="/generator" className="mt-3 flex min-h-10 items-center justify-center rounded-lg border border-teal-400/30 bg-teal-400/10 px-3 text-sm font-semibold text-teal-100 hover:bg-teal-400/15">建立研究行情</Link> : null}
      </Card>

      <div className="min-w-0 space-y-4">
        <MarketPriceExplorer selectedInstrumentId={selectedResearchKey ? undefined : selected?.id} selectedSourceKey={selectedResearchKey || undefined} />
        {selectedResearchKey ? <Card><CardHeader><div><CardTitle>研究行情已選取</CardTitle><CardDescription>圖表已切換至左側選取的研究版本。研究行情的資料血緣與不可變版本資訊維持既有規則。</CardDescription></div></CardHeader><Button variant="secondary" onClick={() => { setSelectedResearchKey(""); setSourceTab("original"); }}>改看原始行情</Button></Card> : <>
          <details open className="qs-card group"><summary className="flex cursor-pointer list-none items-center justify-between gap-3"><div><div className="text-base font-semibold text-slate-100">資料狀態</div><div className="mt-1 text-sm text-slate-400">{selected?.display_name ?? "正在載入商品"}的已匯入資料與價格口徑。</div></div><ChevronDown className="h-5 w-5 text-slate-500 transition group-open:rotate-180" /></summary><div className="mt-4 grid gap-3 md:grid-cols-2 xl:grid-cols-3">{statusQuery.isLoading ? <div className="text-sm text-slate-500">載入中…</div> : datasets.map((dataset) => <DatasetCard key={`${dataset.instrument_id}-${dataset.interval}`} dataset={dataset} />)}</div></details>
          <details className="qs-card group"><summary className="flex cursor-pointer list-none items-center justify-between gap-3"><div><div className="text-base font-semibold text-slate-100">資料操作</div><div className="mt-1 text-sm text-slate-400">手動補匯資料，或重新偵測來源可用的最早日期。</div></div><ChevronDown className="h-5 w-5 text-slate-500 transition group-open:rotate-180" /></summary><div className="mt-4">{selectedDataset?.needs_full_reimport ? <div className="mb-4 rounded-lg border border-amber-400/25 bg-amber-400/[0.06] px-4 py-3 text-sm leading-6 text-amber-100">目前資料為「{selectedDataset.price_adjustment_label ?? "舊口徑或未知"}」。完整重匯會改變使用這批資料的回測結果。</div> : null}<form className="grid gap-3 md:grid-cols-2 xl:grid-cols-5" onSubmit={submit}><label><span className="mb-1 block text-xs text-slate-400">資料週期</span><select className={inputClass} value={interval} onChange={(event) => changeInterval(event.target.value)}>{intervals.map((item) => <option key={item} value={item}>{intervalLabels[item] ?? item}</option>)}</select></label><label><span className="mb-1 block text-xs text-slate-400">開始日期</span><input className={inputClass} type="date" value={startDate} onChange={(event) => setStartDate(event.target.value)} /></label><label><span className="mb-1 block text-xs text-slate-400">結束日期</span><input className={inputClass} type="date" value={endDate} onChange={(event) => setEndDate(event.target.value)} /></label><div className="flex items-end"><Button className="w-full" icon={RefreshCw} loading={refreshStartsMutation.isPending} type="button" variant="secondary" onClick={() => refreshStartsMutation.mutate()}>偵測起始日</Button></div><div className="flex items-end"><Button className="w-full" icon={RefreshCw} loading={importMutation.isPending} type="submit">匯入資料</Button></div><label className="flex items-start gap-3 rounded-lg border border-white/[0.06] bg-white/[0.02] p-3 text-sm md:col-span-2 xl:col-span-5"><input className="mt-1 accent-teal-400" type="checkbox" checked={includePreclose} disabled={interval !== "1d"} onChange={(event) => setIncludePreclose(event.target.checked)} /><span><span className="font-semibold text-slate-200">同步匯入收盤前 10 分鐘快照</span><span className="mt-1 block text-xs text-slate-500">只適用日 K，快照會獨立保存，不混入標準 K 線。</span></span></label></form>{importMutation.data ? <div className="mt-4 text-sm text-teal-200">已匯入 {importMutation.data.fetched_bars.toLocaleString("zh-TW")} 筆，寫入 {importMutation.data.stored_bars.toLocaleString("zh-TW")} 筆。</div> : null}{[importMutation.error, refreshStartsMutation.error].find(Boolean) instanceof Error ? <div className="mt-3 text-sm text-rose-300">{String(([importMutation.error, refreshStartsMutation.error].find(Boolean) as Error).message)}</div> : null}</div></details>
          <details className="qs-card group"><summary className="flex cursor-pointer list-none items-center justify-between gap-3"><div><div className="flex items-center gap-2 text-base font-semibold text-slate-100"><Wrench className="h-4 w-4" />維護與進階</div><div className="mt-1 text-sm text-slate-400">修復、商品設定與排序。這些操作不會預設顯示，避免干擾日常查看。</div></div><ChevronDown className="h-5 w-5 text-slate-500 transition group-open:rotate-180" /></summary><div className="mt-4 space-y-5"><div><div className="mb-3 text-sm font-semibold text-slate-200">日週月資料維護</div><div className="grid gap-2 sm:grid-cols-2"><Button icon={TriangleAlert} loading={auditMaintenanceMutation.isPending} variant="secondary" onClick={() => auditMaintenanceMutation.mutate()}>稽核選取商品</Button><Button icon={RefreshCw} loading={repairMaintenanceMutation.isPending} variant="secondary" onClick={repairSelectedMaintenance}>修復選取商品</Button><Button icon={TriangleAlert} loading={auditAllMaintenanceMutation.isPending} variant="secondary" onClick={() => auditAllMaintenanceMutation.mutate()}>稽核全部商品</Button><Button icon={RefreshCw} loading={repairAllMaintenanceMutation.isPending} variant="secondary" onClick={repairAllMaintenance}>修復全部商品</Button></div>{maintenanceResults.length ? <div className="mt-3 text-sm text-teal-200">維護作業完成，已檢查 {maintenanceResults.length.toLocaleString("zh-TW")} 個商品；請查看資料狀態確認結果。</div> : null}</div><div className="border-t border-white/[0.06] pt-5"><div className="mb-3 text-sm font-semibold text-slate-200">研究商品管理</div><div className="mb-3 flex flex-wrap gap-2">{builtinFredIndicators.map((item) => <Button key={item.id} type="button" variant="secondary" onClick={() => setNewInstrument({ id: item.id, symbol: item.symbol, display_name: item.display_name, data_source: "fred", market: "macro", supported_intervals: fredIntervals, sort_order: 1000 })}>{item.display_name}</Button>)}</div><form className="grid gap-3 md:grid-cols-2 xl:grid-cols-6" onSubmit={(event) => { event.preventDefault(); upsertMutation.mutate(); }}><label><span className="mb-1 block text-xs text-slate-400">代碼</span><input className={inputClass} placeholder="TQQQ" value={newInstrument.symbol} onChange={(event) => setNewInstrument((current) => ({ ...current, symbol: event.target.value }))} /></label><label><span className="mb-1 block text-xs text-slate-400">名稱</span><input className={inputClass} placeholder="顯示名稱" value={newInstrument.display_name ?? ""} onChange={(event) => setNewInstrument((current) => ({ ...current, display_name: event.target.value }))} /></label><label><span className="mb-1 block text-xs text-slate-400">來源</span><select className={inputClass} value={newInstrument.data_source} onChange={(event) => changeSource(event.target.value)}><option value="yahoo">Yahoo Finance</option><option value="binance">Binance</option><option value="fred">FRED</option></select></label><label><span className="mb-1 block text-xs text-slate-400">市場</span><select className={inputClass} value={newInstrument.market} onChange={(event) => setNewInstrument((current) => ({ ...current, market: event.target.value }))}><option value="us">美股</option><option value="tw">台股</option><option value="crypto">加密貨幣</option><option value="macro">總經指標</option><option value="other">其他</option></select></label><label><span className="mb-1 block text-xs text-slate-400">排序</span><input className={inputClass} type="number" value={newInstrument.sort_order ?? 1000} onChange={(event) => setNewInstrument((current) => ({ ...current, sort_order: Number(event.target.value) }))} /></label><div className="flex items-end"><Button className="w-full" icon={Plus} loading={upsertMutation.isPending} type="submit">新增</Button></div><div className="flex flex-wrap gap-2 md:col-span-2 xl:col-span-6">{intervalsForSource(newInstrument.data_source).map((item) => <label key={item} className="inline-flex items-center gap-2 rounded-full border border-white/[0.06] bg-white/[0.03] px-3 py-1.5 text-xs text-slate-300"><input className="accent-teal-400" type="checkbox" checked={(newInstrument.supported_intervals ?? []).includes(item)} onChange={() => toggleNewInterval(item)} />{intervalLabels[item] ?? item}</label>)}</div></form><div className="mt-4 grid gap-2">{instruments.map((item, index) => <div key={item.id} className="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-white/[0.06] bg-white/[0.02] p-3"><div><div className="font-semibold text-slate-100">{item.display_name}</div><div className="font-mono text-xs text-slate-500">{item.id} · {item.symbol} · {marketName(item.market)}</div></div><div className="flex gap-2"><Button icon={ArrowUp} size="sm" variant="secondary" disabled={index === 0 || reorderMutation.isPending} onClick={() => moveInstrument(index, -1)}>上移</Button><Button icon={ArrowDown} size="sm" variant="secondary" disabled={index === instruments.length - 1 || reorderMutation.isPending} onClick={() => moveInstrument(index, 1)}>下移</Button><Button icon={Trash2} size="sm" variant="danger" loading={deleteMutation.isPending} onClick={() => { if (window.confirm(`停用 ${item.display_name}？`)) deleteMutation.mutate(item.id); }}>刪除</Button></div></div>)}</div></div></div></details>
        </>}
      </div>
    </div>
  </section>;
}

function ResearchList({ sources, selectedKey, search, onSelect }: { sources: MarketChartSource[]; selectedKey: string; search: string; onSelect: (source: MarketChartSource) => void }) {
  const groups = useMemo(() => ["leverage", "recomposition", "perturbation", "other"].flatMap((category) => { const items = sources.filter((source) => marketSourceCategory(source) === category && `${source.display_name} ${source.instrument.symbol}`.toLowerCase().includes(search.trim().toLowerCase())); return items.length ? [{ category, items }] : []; }), [sources, search]);
  if (!groups.length) return <div className="p-5 text-center text-sm text-slate-500">尚無符合條件的研究行情。</div>;
  return <>{groups.map((group) => <div key={group.category} className="pt-2"><div className="mb-1 px-1 text-xs font-semibold text-slate-400">{marketSourceCategoryLabels[group.category as keyof typeof marketSourceCategoryLabels]}</div>{group.items.map((source) => { const key = marketSourceKey(source); return <button key={key} type="button" onClick={() => onSelect(source)} className={cn("mb-1 w-full rounded-lg border p-3 text-left", selectedKey === key ? "border-teal-400/35 bg-teal-400/[0.08]" : "border-white/[0.06] bg-white/[0.02] hover:bg-white/[0.05]")}><div className="text-sm font-semibold text-slate-100">{source.display_name}</div><div className="mt-1 font-mono text-xs text-slate-500">{source.instrument.symbol} · {intervalLabels[source.interval] ?? source.interval} · {source.bar_count.toLocaleString("zh-TW")} 根</div><div className="mt-1 text-xs text-slate-500">{source.can_backtest ? "可用於回測" : "僅供查看"}</div></button>; })}</div>)}</>;
}
