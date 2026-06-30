import { FormEvent, useEffect, useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowDown, ArrowUp, CheckCircle2, Database, Plus, RefreshCw, Trash2, TriangleAlert } from "lucide-react";
import { marketDataApi, type DatasetSummary, type InstrumentSummary, type ResearchInstrument, type UpsertInstrumentInput } from "../../shared/services/marketData";
import { Button } from "../../shared/ui/Button";
import { Card, CardDescription, CardHeader, CardTitle } from "../../shared/ui/Card";
import { cn } from "../../shared/lib/cn";

const intervalLabels: Record<string, string> = {
  "1d": "日 K",
  "1h": "1 小時 K",
  "15m": "15 分 K",
  "5m": "5 分 K",
  "1m": "1 分 K",
  "1s": "1 秒 K",
  "1w": "週 K",
  "1M": "月 K"
};

const yahooIntervals = ["1d", "1h", "1m", "1w", "1M"];
const binanceIntervals = ["1d", "1h", "15m", "5m", "1m", "1s", "1w", "1M"];

function dateInputValue(date: Date) {
  return date.toISOString().slice(0, 10);
}

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

function dayStartMs(value: string) {
  return new Date(`${value}T00:00:00.000Z`).getTime();
}

function dayEndMs(value: string) {
  return new Date(`${value}T23:59:59.999Z`).getTime();
}

function formatMs(value?: number) {
  if (!value) return "尚無資料";
  return new Intl.DateTimeFormat("zh-TW", {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    hour12: false
  }).format(new Date(value));
}

function marketName(market?: string) {
  if (market === "tw") return "台股";
  if (market === "us") return "美股";
  if (market === "crypto") return "加密貨幣";
  return market || "其他";
}

function timeLabelPrefix(interval: string) {
  return interval === "1w" || interval === "1M" ? "期間起點" : "開始時間";
}

function DatasetCard({ dataset }: { dataset: DatasetSummary }) {
  const empty = dataset.count === 0;
  const fresh = Boolean(dataset.is_fresh);
  const timeLabel = timeLabelPrefix(dataset.interval);
  return (
    <Card className={cn("p-4", fresh ? "border-[#2dd4bf]/20" : empty ? "border-white/[0.04]" : "border-[#f59e0b]/25")}>
      <div className="flex items-start justify-between gap-3">
        <div>
          <div className="text-sm text-slate-500">資料週期</div>
          <div className="mt-1 font-mono text-xl font-semibold text-slate-100">{intervalLabels[dataset.interval] ?? dataset.interval}</div>
        </div>
        {fresh ? <CheckCircle2 className="h-5 w-5 text-[#99f6e4]" /> : <Database className={cn("h-5 w-5", empty ? "text-slate-600" : "text-[#fbbf24]")} />}
      </div>
      <div className="mt-4 grid gap-3 text-sm">
        <InfoRow label="筆數" value={dataset.count.toLocaleString("zh-TW")} />
        <InfoRow label={`第一筆${timeLabel}`} value={formatMs(dataset.first_open_ms)} />
        <InfoRow label={`最後一筆${timeLabel}`} value={formatMs(dataset.last_open_ms)} />
        <InfoRow label={`理論最新${timeLabel}`} value={formatMs(dataset.expected_latest_open_ms)} />
        <InfoRow label="價格口徑" value={dataset.price_adjustment_label ?? "未記錄"} />
        {dataset.interval === "1d" ? (
          <>
            <InfoRow label="收盤前快照" value={(dataset.preclose_snapshot_count ?? 0).toLocaleString("zh-TW")} />
            <InfoRow label="最新快照" value={formatMs(dataset.last_preclose_ms)} />
          </>
        ) : null}
      </div>
      {dataset.needs_full_reimport ? (
        <div className="mt-3 rounded-md border border-[#f59e0b]/25 bg-[#f59e0b]/10 px-3 py-2 text-xs leading-5 text-[#fde68a]">
          這批資料是舊口徑或未知口徑；若要使用新版 Yahoo 調整後價格，請用完整區間重新匯入。
        </div>
      ) : null}
    </Card>
  );
}

function InfoRow({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-center justify-between gap-3">
      <span className="text-slate-500">{label}</span>
      <span className="text-right font-mono text-slate-200">{value}</span>
    </div>
  );
}

export function MarketDataPage() {
  const queryClient = useQueryClient();
  const instrumentsQuery = useQuery({
    queryKey: ["market-data-instruments"],
    queryFn: () => marketDataApi.instruments()
  });
  const overviewQuery = useQuery({
    queryKey: ["market-data-overview"],
    queryFn: () => marketDataApi.overview()
  });
  const instruments = instrumentsQuery.data?.instruments ?? [];
  const [instrumentId, setInstrumentId] = useState("BTCUSDT");
  const selected = instruments.find((item) => item.id === instrumentId) ?? instruments[0];
  const [interval, setInterval] = useState("1d");
  const [startDate, setStartDate] = useState(defaultStart(undefined, "1d"));
  const [endDate, setEndDate] = useState(dateInputValue(new Date()));
  const [includePreclose, setIncludePreclose] = useState(false);
  const [newInstrument, setNewInstrument] = useState<UpsertInstrumentInput>({
    symbol: "",
    display_name: "",
    data_source: "yahoo",
    market: "us",
    supported_intervals: yahooIntervals,
    sort_order: 1000
  });

  useEffect(() => {
    if (!selected && instruments[0]) {
      setInstrumentId(instruments[0].id);
    }
  }, [instruments, selected]);

  const statusQuery = useQuery({
    queryKey: ["market-data", selected?.id ?? instrumentId],
    queryFn: () => marketDataApi.status(selected?.id ?? instrumentId),
    enabled: Boolean(selected?.id ?? instrumentId)
  });
  const intervals = statusQuery.data?.supported_intervals ?? selected?.supported_intervals ?? ["1d"];
  const datasets = useMemo(() => statusQuery.data?.datasets ?? [], [statusQuery.data]);
  const selectedDataset = datasets.find((dataset) => dataset.interval === interval);

  const importMutation = useMutation({
    mutationFn: () =>
      marketDataApi.importKLines({
        instrument_id: selected?.id ?? instrumentId,
        data_source: selected?.data_source,
        symbol: selected?.symbol ?? instrumentId,
        interval,
        start_time_ms: dayStartMs(startDate),
        end_time_ms: dayEndMs(endDate),
        include_preclose_snapshots: includePreclose && interval === "1d"
      }),
    onSuccess: refreshMarketQueries
  });

  const upsertMutation = useMutation({
    mutationFn: () => marketDataApi.upsertInstrument(newInstrument),
    onSuccess: (instrument) => {
      setInstrumentId(instrument.id);
      setNewInstrument({ symbol: "", display_name: "", data_source: "yahoo", market: "us", supported_intervals: yahooIntervals, sort_order: 1000 });
      refreshMarketQueries();
    }
  });

  const deleteMutation = useMutation({
    mutationFn: (id: string) => marketDataApi.deleteInstrument(id),
    onSuccess: refreshMarketQueries
  });

  const reorderMutation = useMutation({
    mutationFn: (ids: string[]) => marketDataApi.reorderInstruments(ids),
    onSuccess: refreshMarketQueries
  });

  const refreshStartsMutation = useMutation({
    mutationFn: () => marketDataApi.refreshInstrumentStarts(selected?.id ?? instrumentId),
    onSuccess: (result) => {
      refreshMarketQueries();
      const detected = result.starts?.[interval];
      if (detected && detected > 0) setStartDate(dateInputValue(new Date(detected)));
    }
  });

  const updateLatestMutation = useMutation({
    mutationFn: () => marketDataApi.updateLatest(),
    onSuccess: refreshMarketQueries
  });

  function refreshMarketQueries() {
    queryClient.invalidateQueries({ queryKey: ["market-data-instruments"] });
    queryClient.invalidateQueries({ queryKey: ["market-data-overview"] });
    queryClient.invalidateQueries({ queryKey: ["market-data"] });
    queryClient.invalidateQueries({ queryKey: ["research-status"] });
  }

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (selectedDataset?.needs_full_reimport) {
      const ok = window.confirm(
        `目前 ${selected?.display_name ?? selected?.symbol ?? instrumentId} 的 ${intervalLabels[interval] ?? interval} 是舊口徑或未知口徑。\n\n完整重匯會改變這個標的既有回測結果。確定要繼續匯入並改成新版價格口徑嗎？`
      );
      if (!ok) return;
    }
    importMutation.mutate();
  }

  function submitNewInstrument(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    upsertMutation.mutate();
  }

  function changeInstrument(nextId: string) {
    const next = instruments.find((item) => item.id === nextId);
    const nextInterval = next?.supported_intervals[0] ?? "1d";
    setInstrumentId(nextId);
    setInterval(nextInterval);
    setStartDate(defaultStart(next, nextInterval));
  }

  function changeInterval(next: string) {
    setInterval(next);
    setStartDate(defaultStart(selected, next));
  }

  function changeSource(source: string) {
    setNewInstrument((current) => ({
      ...current,
      data_source: source,
      market: source === "binance" ? "crypto" : current.market || "us",
      supported_intervals: source === "binance" ? binanceIntervals : yahooIntervals
    }));
  }

  function toggleNewInterval(value: string) {
    setNewInstrument((current) => {
      const currentIntervals = current.supported_intervals ?? [];
      const next = currentIntervals.includes(value) ? currentIntervals.filter((item) => item !== value) : [...currentIntervals, value];
      return { ...current, supported_intervals: next };
    });
  }

  function moveInstrument(index: number, delta: number) {
    const nextIndex = index + delta;
    if (nextIndex < 0 || nextIndex >= instruments.length) return;
    const ids = instruments.map((item) => item.id);
    const [moved] = ids.splice(index, 1);
    ids.splice(nextIndex, 0, moved);
    reorderMutation.mutate(ids);
  }

  const overviewGroups = useMemo(() => {
    const groups = new Map<string, InstrumentSummary[]>();
    for (const item of overviewQuery.data?.items ?? []) {
      const key = item.instrument.market ?? "other";
      groups.set(key, [...(groups.get(key) ?? []), item]);
    }
    return ["tw", "us", "crypto", "other"].flatMap((key) => {
      const items = groups.get(key);
      return items?.length ? [{ key, items }] : [];
    });
  }, [overviewQuery.data]);

  return (
    <section className="space-y-4">
      <div>
        <h1 className="text-2xl font-bold text-slate-100">研究資料</h1>
        <p className="mt-1 text-sm text-slate-400">管理研究商品、匯入歷史 K 線，並檢查資料是否更新到最新。</p>
      </div>

      <Card>
        <CardHeader>
          <div>
            <CardTitle>資料更新總覽</CardTitle>
            <CardDescription>依市場分組檢查每個商品與週期的最新狀態。</CardDescription>
          </div>
          <Button icon={RefreshCw} loading={updateLatestMutation.isPending} onClick={() => updateLatestMutation.mutate()}>
            更新全部
          </Button>
        </CardHeader>
        <div className="space-y-4">
          {overviewGroups.map((group) => (
            <div key={group.key}>
              <div className="mb-2 text-sm font-semibold text-slate-200">{marketName(group.key)}</div>
              <div className="grid gap-2">
                {group.items.map((item) => (
                  <div key={item.instrument.id} className="rounded-lg border border-white/[0.06] bg-white/[0.02] p-3">
                    <div className="flex flex-wrap items-center justify-between gap-2">
                      <div>
                        <div className="font-semibold text-slate-100">{item.instrument.display_name}</div>
                        <div className="font-mono text-xs text-slate-500">{item.instrument.symbol} · {item.instrument.data_source}</div>
                      </div>
                      {item.instrument.last_auto_update_error ? (
                        <span className="inline-flex items-center gap-1 text-xs text-[#fecaca]"><TriangleAlert className="h-3.5 w-3.5" />更新異常</span>
                      ) : null}
                    </div>
                    <div className="mt-3 flex flex-wrap gap-2">
                      {item.datasets.map((dataset) => (
                        <span
                          key={`${item.instrument.id}-${dataset.interval}`}
                          className={cn(
                            "rounded-full border px-2.5 py-1 text-xs",
                            dataset.is_fresh ? "border-[#2dd4bf]/30 bg-[#2dd4bf]/10 text-[#99f6e4]" : "border-[#f59e0b]/30 bg-[#f59e0b]/10 text-[#fde68a]"
                          )}
                          title={`最新：${formatMs(dataset.last_open_ms)} / 理論：${formatMs(dataset.expected_latest_open_ms)}`}
                        >
                          {intervalLabels[dataset.interval] ?? dataset.interval} · {dataset.is_fresh ? "已更新" : "待更新"}
                        </span>
                      ))}
                    </div>
                  </div>
                ))}
              </div>
            </div>
          ))}
          {overviewQuery.isLoading ? <div className="text-sm text-slate-500">載入中...</div> : null}
        </div>
        {updateLatestMutation.error ? <div className="mt-4 text-sm text-[#fecaca]">{String(updateLatestMutation.error.message)}</div> : null}
      </Card>

      <Card>
        <CardHeader>
          <div>
            <CardTitle>資料匯入</CardTitle>
            <CardDescription>手動補資料時使用；日 K 可另外匯入收盤前快照。</CardDescription>
          </div>
        </CardHeader>
        {selectedDataset?.needs_full_reimport ? (
          <div className="mb-4 rounded-lg border border-[#f59e0b]/25 bg-[#f59e0b]/10 px-4 py-3 text-sm leading-6 text-[#fde68a]">
            目前選取的資料集是「{selectedDataset.price_adjustment_label ?? "舊口徑或未知"}」。若要切換成新版 Yahoo 調整後價格，請把開始日期設到資料源能提供的最早日期再重新匯入；重匯後，使用這批資料的回測結果會改變。
          </div>
        ) : null}
        {selected?.available_start_ms?.[interval] ? (
          <div className="mb-4 rounded-lg border border-white/[0.06] bg-white/[0.02] px-4 py-3 text-sm text-slate-400">
            此週期可匯入起始日：<span className="font-mono text-slate-200">{dateInputValue(new Date(selected.available_start_ms[interval]))}</span>
          </div>
        ) : (
          <div className="mb-4 rounded-lg border border-[#f59e0b]/25 bg-[#f59e0b]/10 px-4 py-3 text-sm leading-6 text-[#fde68a]">
            此週期尚未偵測到資料源起始日，會使用預設區間。可先按「重新偵測起始日」。
          </div>
        )}
        <form className="grid gap-4 md:grid-cols-5" onSubmit={submit}>
          <label>
            <span className="mb-2 block text-sm text-slate-300">研究商品</span>
            <select className="h-11 w-full rounded-lg border border-slate-700 bg-slate-900/80 px-3 text-sm text-slate-100 outline-none focus:border-[#2dd4bf]" value={selected?.id ?? instrumentId} onChange={(event) => changeInstrument(event.target.value)}>
              {instruments.map((item) => (
                <option key={item.id} value={item.id}>{item.display_name}</option>
              ))}
            </select>
          </label>
          <label>
            <span className="mb-2 block text-sm text-slate-300">資料週期</span>
            <select className="h-11 w-full rounded-lg border border-slate-700 bg-slate-900/80 px-3 text-sm text-slate-100 outline-none focus:border-[#2dd4bf]" value={interval} onChange={(event) => changeInterval(event.target.value)}>
              {intervals.map((item) => (
                <option key={item} value={item}>{intervalLabels[item] ?? item}</option>
              ))}
            </select>
          </label>
          <label>
            <span className="mb-2 block text-sm text-slate-300">開始日期</span>
            <input className="h-11 w-full rounded-lg border border-slate-700 bg-slate-900/80 px-3 text-sm text-slate-100 outline-none focus:border-[#2dd4bf]" type="date" value={startDate} onChange={(event) => setStartDate(event.target.value)} />
          </label>
          <label>
            <span className="mb-2 block text-sm text-slate-300">結束日期</span>
            <input className="h-11 w-full rounded-lg border border-slate-700 bg-slate-900/80 px-3 text-sm text-slate-100 outline-none focus:border-[#2dd4bf]" type="date" value={endDate} onChange={(event) => setEndDate(event.target.value)} />
          </label>
          <div className="flex items-end">
            <Button className="w-full" icon={RefreshCw} loading={importMutation.isPending} type="submit">匯入資料</Button>
          </div>
          <div className="flex items-end md:col-span-5">
            <Button className="w-full" icon={RefreshCw} loading={refreshStartsMutation.isPending} type="button" variant="secondary" onClick={() => refreshStartsMutation.mutate()}>
              重新偵測起始日
            </Button>
          </div>
          <label className="flex items-start gap-3 rounded-lg border border-white/[0.04] bg-white/[0.02] p-3 md:col-span-5">
            <input className="mt-1 h-4 w-4 accent-[#2dd4bf]" type="checkbox" checked={includePreclose} disabled={interval !== "1d"} onChange={(event) => setIncludePreclose(event.target.checked)} />
            <span>
              <span className="block text-sm font-semibold text-slate-200">同步匯入收盤前 10 分鐘快照</span>
              <span className="mt-1 block text-xs text-slate-500">只適用日 K，資料會存成獨立快照，不混入標準 K 線。</span>
            </span>
          </label>
        </form>
        {refreshStartsMutation.error ? <div className="mt-4 text-sm text-[#fecaca]">{String(refreshStartsMutation.error.message)}</div> : null}
        <div className="mt-3 text-xs text-slate-500">
          目前來源：<span className="font-mono text-slate-300">{selected?.data_source ?? statusQuery.data?.data_source ?? "-"}</span> · 代碼：
          <span className="font-mono text-slate-300">{selected?.symbol ?? statusQuery.data?.symbol ?? "-"}</span>
        </div>
        {importMutation.data ? (
          <div className="mt-4 rounded-lg border border-[#2dd4bf]/20 bg-[#2dd4bf]/10 px-4 py-3 text-sm text-[#99f6e4]">
            已匯入 {importMutation.data.fetched_bars.toLocaleString("zh-TW")} 筆，寫入 {importMutation.data.stored_bars.toLocaleString("zh-TW")} 筆。
            {importMutation.data.price_adjustment_label ? ` 價格口徑：${importMutation.data.price_adjustment_label}。` : ""}
            {includePreclose ? ` 收盤前快照 ${Number(importMutation.data.preclose_snapshot_count ?? 0).toLocaleString("zh-TW")} 筆。` : ""}
          </div>
        ) : null}
        {importMutation.error ? <div className="mt-4 text-sm text-[#fecaca]">{String(importMutation.error.message)}</div> : null}
      </Card>

      <Card>
        <CardHeader>
          <div>
            <CardTitle>已匯入資料</CardTitle>
            <CardDescription>目前選取商品的資料範圍。</CardDescription>
          </div>
        </CardHeader>
        <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
          {statusQuery.isLoading ? <div className="rounded-lg border border-white/[0.06] p-4 text-sm text-slate-500">載入中...</div> : null}
          {datasets.map((dataset) => (
            <DatasetCard key={`${dataset.instrument_id}-${dataset.interval}`} dataset={dataset} />
          ))}
        </div>
      </Card>

      <Card>
        <CardHeader>
          <div>
            <CardTitle>研究商品管理</CardTitle>
            <CardDescription>新增或停用商品後，所有研究頁面會共用同一份清單。</CardDescription>
          </div>
        </CardHeader>
        <form className="grid gap-4 lg:grid-cols-6" onSubmit={submitNewInstrument}>
          <label>
            <span className="mb-2 block text-sm text-slate-300">代碼</span>
            <input className="h-11 w-full rounded-lg border border-slate-700 bg-slate-900/80 px-3 text-sm text-slate-100 outline-none focus:border-[#2dd4bf]" placeholder="TQQQ" value={newInstrument.symbol} onChange={(event) => setNewInstrument((current) => ({ ...current, symbol: event.target.value }))} />
          </label>
          <label>
            <span className="mb-2 block text-sm text-slate-300">名稱</span>
            <input className="h-11 w-full rounded-lg border border-slate-700 bg-slate-900/80 px-3 text-sm text-slate-100 outline-none focus:border-[#2dd4bf]" placeholder="顯示名稱" value={newInstrument.display_name ?? ""} onChange={(event) => setNewInstrument((current) => ({ ...current, display_name: event.target.value }))} />
          </label>
          <label>
            <span className="mb-2 block text-sm text-slate-300">來源</span>
            <select className="h-11 w-full rounded-lg border border-slate-700 bg-slate-900/80 px-3 text-sm text-slate-100 outline-none focus:border-[#2dd4bf]" value={newInstrument.data_source} onChange={(event) => changeSource(event.target.value)}>
              <option value="yahoo">Yahoo Finance</option>
              <option value="binance">Binance</option>
            </select>
          </label>
          <label>
            <span className="mb-2 block text-sm text-slate-300">市場</span>
            <select className="h-11 w-full rounded-lg border border-slate-700 bg-slate-900/80 px-3 text-sm text-slate-100 outline-none focus:border-[#2dd4bf]" value={newInstrument.market} onChange={(event) => setNewInstrument((current) => ({ ...current, market: event.target.value }))}>
              <option value="us">美股</option>
              <option value="tw">台股</option>
              <option value="crypto">加密貨幣</option>
              <option value="other">其他</option>
            </select>
          </label>
          <label>
            <span className="mb-2 block text-sm text-slate-300">排序</span>
            <input className="h-11 w-full rounded-lg border border-slate-700 bg-slate-900/80 px-3 text-sm text-slate-100 outline-none focus:border-[#2dd4bf]" type="number" value={newInstrument.sort_order ?? 1000} onChange={(event) => setNewInstrument((current) => ({ ...current, sort_order: Number(event.target.value) }))} />
          </label>
          <div className="flex items-end">
            <Button className="w-full" icon={Plus} loading={upsertMutation.isPending} type="submit">新增</Button>
          </div>
          <div className="flex flex-wrap gap-2 lg:col-span-6">
            {((newInstrument.data_source === "binance" ? binanceIntervals : yahooIntervals)).map((item) => (
              <label key={item} className="inline-flex items-center gap-2 rounded-full border border-white/[0.06] bg-white/[0.03] px-3 py-1.5 text-xs text-slate-300">
                <input className="h-3.5 w-3.5 accent-[#2dd4bf]" type="checkbox" checked={(newInstrument.supported_intervals ?? []).includes(item)} onChange={() => toggleNewInterval(item)} />
                {intervalLabels[item] ?? item}
              </label>
            ))}
          </div>
        </form>
        {upsertMutation.error ? <div className="mt-3 text-sm text-[#fecaca]">{String(upsertMutation.error.message)}</div> : null}
        <div className="mt-5 grid gap-2">
          {instruments.map((item, index) => (
            <div key={item.id} className="flex flex-wrap items-center justify-between gap-3 rounded-lg border border-white/[0.06] bg-white/[0.02] p-3">
              <div>
                <div className="font-semibold text-slate-100">{item.display_name}</div>
                <div className="font-mono text-xs text-slate-500">{item.id} · {item.symbol} · {marketName(item.market)} · {item.supported_intervals.map((value) => intervalLabels[value] ?? value).join(" / ")}</div>
              </div>
              <div className="flex flex-wrap gap-2">
                <Button icon={ArrowUp} variant="secondary" disabled={index === 0 || reorderMutation.isPending} onClick={() => moveInstrument(index, -1)}>
                  上移
                </Button>
                <Button icon={ArrowDown} variant="secondary" disabled={index === instruments.length - 1 || reorderMutation.isPending} onClick={() => moveInstrument(index, 1)}>
                  下移
                </Button>
                <Button
                  icon={Trash2}
                  variant="danger"
                  loading={deleteMutation.isPending}
                  onClick={() => {
                    if (window.confirm(`停用 ${item.display_name}？`)) deleteMutation.mutate(item.id);
                  }}
                >
                  刪除
                </Button>
              </div>
            </div>
          ))}
        </div>
      </Card>
    </section>
  );
}
