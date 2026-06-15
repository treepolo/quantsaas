import { FormEvent, useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Database, RefreshCw } from "lucide-react";
import { useI18n } from "../../i18n/useI18n";
import { shortDateTime } from "../../shared/lib/format";
import { marketDataApi, type DatasetSummary } from "../../shared/services/marketData";
import { Button } from "../../shared/ui/Button";
import { Card, CardDescription, CardHeader, CardTitle } from "../../shared/ui/Card";
import { cn } from "../../shared/lib/cn";

const intervalLabels: Record<string, string> = {
  "1d": "1 天",
  "1h": "1 小時",
  "15m": "15 分鐘",
  "5m": "5 分鐘",
  "1m": "1 分鐘",
  "1s": "1 秒"
};

function defaultStart(interval: string) {
  const now = new Date();
  if (interval === "1d") return "2017-08-17";
  if (interval === "1h") return dateInputValue(new Date(Date.UTC(now.getUTCFullYear() - 2, now.getUTCMonth(), now.getUTCDate())));
  if (interval === "1s") return dateInputValue(new Date(Date.now() - 24 * 60 * 60 * 1000));
  return dateInputValue(new Date(Date.now() - 90 * 24 * 60 * 60 * 1000));
}

function dateInputValue(date: Date) {
  return date.toISOString().slice(0, 10);
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
  const { t } = useI18n();
  const empty = dataset.count === 0;
  return (
    <Card className={cn("p-4", empty ? "border-white/[0.04]" : "border-[#2dd4bf]/20")}>
      <div className="flex items-start justify-between gap-3">
        <div>
          <div className="text-sm text-slate-500">{t("marketData.interval")}</div>
          <div className="mt-1 font-mono text-xl font-semibold text-slate-100">{intervalLabels[dataset.interval] ?? dataset.interval}</div>
        </div>
        <Database className={cn("h-5 w-5", empty ? "text-slate-600" : "text-[#99f6e4]")} />
      </div>
      <div className="mt-4 grid gap-3 text-sm">
        <div className="flex items-center justify-between gap-3">
          <span className="text-slate-500">{t("marketData.rows")}</span>
          <span className="font-mono text-slate-200">{dataset.count.toLocaleString("zh-TW")}</span>
        </div>
        <div className="flex items-center justify-between gap-3">
          <span className="text-slate-500">{t("marketData.firstBar")}</span>
          <span className="font-mono text-slate-200">{formatMs(dataset.first_open_ms)}</span>
        </div>
        <div className="flex items-center justify-between gap-3">
          <span className="text-slate-500">{t("marketData.lastBar")}</span>
          <span className="font-mono text-slate-200">{formatMs(dataset.last_open_ms)}</span>
        </div>
      </div>
    </Card>
  );
}

export function MarketDataPage() {
  const { t } = useI18n();
  const queryClient = useQueryClient();
  const [symbol, setSymbol] = useState("BTCUSDT");
  const [interval, setInterval] = useState("1d");
  const [startDate, setStartDate] = useState(defaultStart("1d"));
  const [endDate, setEndDate] = useState(dateInputValue(new Date()));
  const { data, isLoading } = useQuery({
    queryKey: ["market-data", symbol],
    queryFn: () => marketDataApi.status(symbol)
  });
  const intervals = data?.supported_intervals ?? ["1d", "1h", "15m", "5m", "1m", "1s"];
  const datasets = useMemo(() => data?.datasets ?? [], [data]);
  const importMutation = useMutation({
    mutationFn: () =>
      marketDataApi.importKLines({
        symbol,
        interval,
        start_time_ms: dayStartMs(startDate),
        end_time_ms: dayEndMs(endDate)
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["market-data", symbol] });
    }
  });

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    importMutation.mutate();
  }

  function changeInterval(next: string) {
    setInterval(next);
    setStartDate(defaultStart(next));
  }

  return (
    <section className="space-y-4">
      <div>
        <h1 className="text-2xl font-bold text-slate-100">{t("marketData.title")}</h1>
        <p className="mt-1 text-sm text-slate-400">{t("marketData.subtitle")}</p>
      </div>
      <Card>
        <CardHeader>
          <div>
            <CardTitle>{t("marketData.importTitle")}</CardTitle>
            <CardDescription>{t("marketData.importSubtitle")}</CardDescription>
          </div>
        </CardHeader>
        <form className="grid gap-4 md:grid-cols-5" onSubmit={submit}>
          <label>
            <span className="mb-2 block text-sm text-slate-300">{t("marketData.symbol")}</span>
            <input
              className="h-11 w-full rounded-lg border border-slate-700 bg-slate-900/80 px-3 font-mono text-sm uppercase text-slate-100 outline-none focus:border-[#2dd4bf]"
              value={symbol}
              onChange={(event) => setSymbol(event.target.value.toUpperCase())}
            />
          </label>
          <label>
            <span className="mb-2 block text-sm text-slate-300">{t("marketData.interval")}</span>
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
            <span className="mb-2 block text-sm text-slate-300">{t("marketData.startDate")}</span>
            <input
              className="h-11 w-full rounded-lg border border-slate-700 bg-slate-900/80 px-3 text-sm text-slate-100 outline-none focus:border-[#2dd4bf]"
              type="date"
              value={startDate}
              onChange={(event) => setStartDate(event.target.value)}
            />
          </label>
          <label>
            <span className="mb-2 block text-sm text-slate-300">{t("marketData.endDate")}</span>
            <input
              className="h-11 w-full rounded-lg border border-slate-700 bg-slate-900/80 px-3 text-sm text-slate-100 outline-none focus:border-[#2dd4bf]"
              type="date"
              value={endDate}
              onChange={(event) => setEndDate(event.target.value)}
            />
          </label>
          <div className="flex items-end">
            <Button className="w-full" icon={RefreshCw} loading={importMutation.isPending} type="submit">
              {t("marketData.import")}
            </Button>
          </div>
        </form>
        {importMutation.data ? (
          <div className="mt-4 rounded-lg border border-[#2dd4bf]/20 bg-[#2dd4bf]/10 px-4 py-3 text-sm text-[#99f6e4]">
            {t("marketData.imported")}：{importMutation.data.fetched_bars.toLocaleString("zh-TW")}
          </div>
        ) : null}
        {importMutation.error ? <div className="mt-4 text-sm text-[#fecaca]">{String(importMutation.error.message)}</div> : null}
      </Card>
      <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
        {isLoading ? <Card className="p-4 text-sm text-slate-500">{t("common.loading")}</Card> : null}
        {datasets.map((dataset) => (
          <DatasetCard key={`${dataset.symbol}-${dataset.interval}`} dataset={dataset} />
        ))}
      </div>
    </section>
  );
}
