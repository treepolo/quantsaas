import { FormEvent, useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Database, RefreshCw } from "lucide-react";
import { shortDateTime } from "../../shared/lib/format";
import { marketDataApi, type DatasetSummary, type ResearchInstrument } from "../../shared/services/marketData";
import { Button } from "../../shared/ui/Button";
import { Card, CardDescription, CardHeader, CardTitle } from "../../shared/ui/Card";
import { cn } from "../../shared/lib/cn";

const intervalLabels: Record<string, string> = {
  "1d": "日 K",
  "1h": "1 小時",
  "15m": "15 分鐘",
  "5m": "5 分鐘",
  "1m": "1 分鐘",
  "1s": "1 秒"
};

function dateInputValue(date: Date) {
  return date.toISOString().slice(0, 10);
}

function defaultStart(instrument?: ResearchInstrument, interval = "1d") {
  const now = new Date();
  if (instrument?.data_source === "yahoo") return dateInputValue(new Date(Date.UTC(now.getUTCFullYear() - 10, now.getUTCMonth(), now.getUTCDate())));
  if (interval === "1d") return "2017-08-17";
  if (interval === "1h") return dateInputValue(new Date(Date.UTC(now.getUTCFullYear() - 2, now.getUTCMonth(), now.getUTCDate())));
  if (interval === "1s") return dateInputValue(new Date(Date.now() - 24 * 60 * 60 * 1000));
  return dateInputValue(new Date(Date.now() - 90 * 24 * 60 * 60 * 1000));
}

function dayStartMs(value: string) {
  return new Date(`${value}T00:00:00.000Z`).getTime();
}

function dayEndMs(value: string) {
  return new Date(`${value}T23:59:59.999Z`).getTime();
}

function formatMs(value?: number) {
  if (!value) return "尚無資料";
  return shortDateTime(new Date(value).toISOString());
}

function DatasetCard({ dataset }: { dataset: DatasetSummary }) {
  const empty = dataset.count === 0;
  return (
    <Card className={cn("p-4", empty ? "border-white/[0.04]" : "border-[#2dd4bf]/20")}>
      <div className="flex items-start justify-between gap-3">
        <div>
          <div className="text-sm text-slate-500">資料週期</div>
          <div className="mt-1 font-mono text-xl font-semibold text-slate-100">{intervalLabels[dataset.interval] ?? dataset.interval}</div>
        </div>
        <Database className={cn("h-5 w-5", empty ? "text-slate-600" : "text-[#99f6e4]")} />
      </div>
      <div className="mt-4 grid gap-3 text-sm">
        <div className="flex items-center justify-between gap-3">
          <span className="text-slate-500">筆數</span>
          <span className="font-mono text-slate-200">{dataset.count.toLocaleString("zh-TW")}</span>
        </div>
        <div className="flex items-center justify-between gap-3">
          <span className="text-slate-500">第一筆</span>
          <span className="font-mono text-slate-200">{formatMs(dataset.first_open_ms)}</span>
        </div>
        <div className="flex items-center justify-between gap-3">
          <span className="text-slate-500">最後一筆</span>
          <span className="font-mono text-slate-200">{formatMs(dataset.last_open_ms)}</span>
        </div>
        {dataset.interval === "1d" ? (
          <>
            <div className="flex items-center justify-between gap-3">
              <span className="text-slate-500">收盤前快照</span>
              <span className="font-mono text-slate-200">{(dataset.preclose_snapshot_count ?? 0).toLocaleString("zh-TW")}</span>
            </div>
            <div className="flex items-center justify-between gap-3">
              <span className="text-slate-500">最新快照</span>
              <span className="font-mono text-slate-200">{formatMs(dataset.last_preclose_ms)}</span>
            </div>
          </>
        ) : null}
      </div>
    </Card>
  );
}

export function MarketDataPage() {
  const queryClient = useQueryClient();
  const instrumentsQuery = useQuery({
    queryKey: ["market-data-instruments"],
    queryFn: () => marketDataApi.instruments()
  });
  const instruments = instrumentsQuery.data?.instruments ?? [];
  const [instrumentId, setInstrumentId] = useState("BTCUSDT");
  const selected = instruments.find((item) => item.id === instrumentId);
  const [interval, setInterval] = useState("1d");
  const [startDate, setStartDate] = useState(defaultStart(undefined, "1d"));
  const [endDate, setEndDate] = useState(dateInputValue(new Date()));
  const [includePreclose, setIncludePreclose] = useState(false);

  const statusQuery = useQuery({
    queryKey: ["market-data", instrumentId],
    queryFn: () => marketDataApi.status(instrumentId)
  });
  const intervals = statusQuery.data?.supported_intervals ?? selected?.supported_intervals ?? ["1d"];
  const datasets = useMemo(() => statusQuery.data?.datasets ?? [], [statusQuery.data]);
  const importMutation = useMutation({
    mutationFn: () =>
      marketDataApi.importKLines({
        instrument_id: instrumentId,
        data_source: selected?.data_source,
        symbol: selected?.symbol ?? instrumentId,
        interval,
        start_time_ms: dayStartMs(startDate),
        end_time_ms: dayEndMs(endDate),
        include_preclose_snapshots: includePreclose && interval === "1d"
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["market-data", instrumentId] });
    }
  });

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    importMutation.mutate();
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

  return (
    <section className="space-y-4">
      <div>
        <h1 className="text-2xl font-bold text-slate-100">研究資料</h1>
        <p className="mt-1 text-sm text-slate-400">匯入並檢查研究用歷史 K 線；股指與 ETF 目前只使用日 K。</p>
      </div>
      <Card>
        <CardHeader>
          <div>
            <CardTitle>資料匯入</CardTitle>
            <CardDescription>BTC 使用 Binance 公開行情；股指與 SOXL 使用 Yahoo Finance 日線資料。</CardDescription>
          </div>
        </CardHeader>
        <form className="grid gap-4 md:grid-cols-5" onSubmit={submit}>
          <label>
            <span className="mb-2 block text-sm text-slate-300">研究標的</span>
            <select
              className="h-11 w-full rounded-lg border border-slate-700 bg-slate-900/80 px-3 text-sm text-slate-100 outline-none focus:border-[#2dd4bf]"
              value={instrumentId}
              onChange={(event) => changeInstrument(event.target.value)}
            >
              {instruments.map((item) => (
                <option key={item.id} value={item.id}>
                  {item.display_name}
                </option>
              ))}
            </select>
          </label>
          <label>
            <span className="mb-2 block text-sm text-slate-300">資料週期</span>
            <select
              className="h-11 w-full rounded-lg border border-slate-700 bg-slate-900/80 px-3 text-sm text-slate-100 outline-none focus:border-[#2dd4bf]"
              value={interval}
              onChange={(event) => changeInterval(event.target.value)}
            >
              {intervals.map((item) => (
                <option key={item} value={item}>
                  {intervalLabels[item] ?? item}
                </option>
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
            <Button className="w-full" icon={RefreshCw} loading={importMutation.isPending} type="submit">
              匯入資料
            </Button>
          </div>
          <label className="md:col-span-5 flex items-start gap-3 rounded-lg border border-white/[0.04] bg-white/[0.02] p-3">
            <input
              className="mt-1 h-4 w-4 accent-[#2dd4bf]"
              type="checkbox"
              checked={includePreclose}
              disabled={interval !== "1d"}
              onChange={(event) => setIncludePreclose(event.target.checked)}
            />
            <span>
              <span className="block text-sm font-semibold text-slate-200">同時匯入收盤前 10 分鐘快照</span>
              <span className="mt-1 block text-xs text-slate-500">快照會寫入獨立資料表，不會混進日 K。Yahoo 盤中歷史有限，只能匯入它目前可提供的近期快照。</span>
            </span>
          </label>
        </form>
        <div className="mt-3 text-xs text-slate-500">
          目前來源：<span className="font-mono text-slate-300">{selected?.data_source ?? statusQuery.data?.data_source ?? "-"}</span>，代碼：
          <span className="font-mono text-slate-300">{selected?.symbol ?? statusQuery.data?.symbol ?? "-"}</span>
        </div>
        {importMutation.data ? (
          <div className="mt-4 rounded-lg border border-[#2dd4bf]/20 bg-[#2dd4bf]/10 px-4 py-3 text-sm text-[#99f6e4]">
            已匯入 {importMutation.data.fetched_bars.toLocaleString("zh-TW")} 筆，寫入 {importMutation.data.stored_bars.toLocaleString("zh-TW")} 筆。
            {includePreclose ? ` 收盤前快照 ${Number(importMutation.data.preclose_snapshot_count ?? 0).toLocaleString("zh-TW")} 筆。` : ""}
          </div>
        ) : null}
        {importMutation.error ? <div className="mt-4 text-sm text-[#fecaca]">{String(importMutation.error.message)}</div> : null}
      </Card>
      <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
        {statusQuery.isLoading ? <Card className="p-4 text-sm text-slate-500">載入中...</Card> : null}
        {datasets.map((dataset) => (
          <DatasetCard key={`${dataset.instrument_id}-${dataset.interval}`} dataset={dataset} />
        ))}
      </div>
    </section>
  );
}
