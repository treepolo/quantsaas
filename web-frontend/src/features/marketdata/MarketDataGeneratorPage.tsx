import { FormEvent, useEffect, useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { CheckCircle2, Database, WandSparkles } from "lucide-react";
import { marketDataApi, type GenerateLeveragedResult, type ResearchInstrument } from "../../shared/services/marketData";
import { Button } from "../../shared/ui/Button";
import { Card, CardDescription, CardHeader, CardTitle } from "../../shared/ui/Card";

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
  if (!value) return "-";
  return new Intl.DateTimeFormat("zh-TW", {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    hour12: false
  }).format(new Date(value));
}

function sourceLabel(source?: string) {
  if (source === "yahoo") return "Yahoo";
  if (source === "binance") return "Binance";
  if (source === "generated") return "產生器";
  return source || "-";
}

function defaultStart(instrument?: ResearchInstrument, interval = "1d") {
  const detected = instrument?.available_start_ms?.[interval];
  if (detected && detected > 0) return dateInputValue(new Date(detected));
  const now = new Date();
  return dateInputValue(new Date(Date.UTC(now.getUTCFullYear() - 1, now.getUTCMonth(), now.getUTCDate())));
}

function targetDefaults(instrument?: ResearchInstrument, multiplier = 2) {
  const base = (instrument?.id || "SOURCE").replace(/[^A-Za-z0-9]/g, "");
  const suffix = `${String(multiplier).replace(/[^0-9A-Za-z]/g, "P")}X`;
  const id = `${base}_${suffix}`.toUpperCase().slice(0, 32);
  return {
    id,
    symbol: id,
    name: `${instrument?.display_name || instrument?.symbol || base} ${multiplier} 倍做多`
  };
}

export function MarketDataGeneratorPage() {
  const queryClient = useQueryClient();
  const instrumentsQuery = useQuery({ queryKey: ["market-data-instruments"], queryFn: () => marketDataApi.instruments() });
  const instruments = instrumentsQuery.data?.instruments ?? [];
  const sourceInstruments = useMemo(() => instruments.filter((item) => item.data_source !== "fred" && item.data_source !== "generated"), [instruments]);
  const [sourceId, setSourceId] = useState("");
  const selectedSource = sourceInstruments.find((item) => item.id === sourceId) ?? sourceInstruments[0];
  const intervals = selectedSource?.supported_intervals?.length ? selectedSource.supported_intervals : ["1d"];
  const [interval, setInterval] = useState("1d");
  const [startDate, setStartDate] = useState("");
  const [endDate, setEndDate] = useState(dateInputValue(new Date()));
  const [multiplier, setMultiplier] = useState(2);
  const [targetId, setTargetId] = useState("");
  const [targetSymbol, setTargetSymbol] = useState("");
  const [targetName, setTargetName] = useState("");
  const [lastResult, setLastResult] = useState<GenerateLeveragedResult | null>(null);

  useEffect(() => {
    if (!sourceId && selectedSource) setSourceId(selectedSource.id);
  }, [selectedSource, sourceId]);

  useEffect(() => {
    if (!selectedSource) return;
    const nextInterval = selectedSource.supported_intervals?.includes(interval) ? interval : selectedSource.supported_intervals?.[0] || "1d";
    if (nextInterval !== interval) setInterval(nextInterval);
    setStartDate(defaultStart(selectedSource, nextInterval));
    setEndDate(dateInputValue(new Date()));
    const defaults = targetDefaults(selectedSource, multiplier);
    setTargetId(defaults.id);
    setTargetSymbol(defaults.symbol);
    setTargetName(defaults.name);
  }, [selectedSource?.id]);

  useEffect(() => {
    if (!selectedSource) return;
    const defaults = targetDefaults(selectedSource, multiplier);
    setTargetId(defaults.id);
    setTargetSymbol(defaults.symbol);
    setTargetName(defaults.name);
  }, [multiplier]);

  const generateMutation = useMutation({
    mutationFn: () =>
      marketDataApi.generateLeveraged({
        source_instrument_id: sourceId,
        source_interval: interval,
        start_time_ms: dayStartMs(startDate),
        end_time_ms: dayEndMs(endDate),
        multiplier,
        target_instrument_id: targetId,
        target_symbol: targetSymbol,
        target_display_name: targetName
      }),
    onSuccess: (result) => {
      setLastResult(result);
      queryClient.invalidateQueries({ queryKey: ["market-data-instruments"] });
      queryClient.invalidateQueries({ queryKey: ["market-data-overview"] });
      queryClient.invalidateQueries({ queryKey: ["research-datasets"] });
    }
  });

  const errorMessage = generateMutation.error instanceof Error ? generateMutation.error.message : "";

  function handleSubmit(event: FormEvent) {
    event.preventDefault();
    generateMutation.mutate();
  }

  return (
    <div className="mx-auto flex w-full max-w-6xl flex-col gap-5 p-6">
      <div>
        <div className="flex items-center gap-3 text-sm font-medium text-[#99f6e4]">
          <WandSparkles className="h-4 w-4" />
          行情資料產生器
        </div>
        <h1 className="mt-2 text-2xl font-semibold text-slate-100">用真實行情產生新的研究資料</h1>
        <p className="mt-2 max-w-3xl text-sm leading-6 text-slate-400">
          選擇已匯入的母行情、區間與倍率，產生一份獨立的新行情資料。新資料會保存開盤價與收盤價，並可在研究資料集使用。
        </p>
      </div>

      <form onSubmit={handleSubmit} className="grid gap-5 lg:grid-cols-[minmax(0,1.15fr)_minmax(320px,0.85fr)]">
        <Card className="p-5">
          <CardHeader>
            <div>
              <CardTitle>母資料與調整方法</CardTitle>
              <CardDescription>目前提供每日 n 倍做多。</CardDescription>
            </div>
            <Database className="h-5 w-5 text-slate-500" />
          </CardHeader>
          <div className="grid gap-4 md:grid-cols-2">
            <label className="grid gap-2 text-sm">
              <span className="text-slate-400">母行情</span>
              <select
                value={sourceId}
                onChange={(event) => setSourceId(event.target.value)}
                className="rounded-lg border border-white/10 bg-slate-950 px-3 py-2 text-slate-100 outline-none focus:border-[#2dd4bf]/50"
              >
                {sourceInstruments.map((instrument) => (
                  <option key={instrument.id} value={instrument.id}>
                    {instrument.display_name} / {instrument.symbol} / {sourceLabel(instrument.data_source)}
                  </option>
                ))}
              </select>
            </label>
            <label className="grid gap-2 text-sm">
              <span className="text-slate-400">資料週期</span>
              <select
                value={interval}
                onChange={(event) => {
                  setInterval(event.target.value);
                  setStartDate(defaultStart(selectedSource, event.target.value));
                }}
                className="rounded-lg border border-white/10 bg-slate-950 px-3 py-2 text-slate-100 outline-none focus:border-[#2dd4bf]/50"
              >
                {intervals.map((item) => (
                  <option key={item} value={item}>
                    {intervalLabels[item] ?? item}
                  </option>
                ))}
              </select>
            </label>
            <label className="grid gap-2 text-sm">
              <span className="text-slate-400">起始日期</span>
              <input
                type="date"
                value={startDate}
                onChange={(event) => setStartDate(event.target.value)}
                className="rounded-lg border border-white/10 bg-slate-950 px-3 py-2 text-slate-100 outline-none focus:border-[#2dd4bf]/50"
              />
            </label>
            <label className="grid gap-2 text-sm">
              <span className="text-slate-400">結束日期</span>
              <input
                type="date"
                value={endDate}
                onChange={(event) => setEndDate(event.target.value)}
                className="rounded-lg border border-white/10 bg-slate-950 px-3 py-2 text-slate-100 outline-none focus:border-[#2dd4bf]/50"
              />
            </label>
            <label className="grid gap-2 text-sm md:col-span-2">
              <span className="text-slate-400">倍率 n</span>
              <input
                type="number"
                min="0.01"
                step="0.01"
                value={multiplier}
                onChange={(event) => setMultiplier(Number(event.target.value))}
                className="rounded-lg border border-white/10 bg-slate-950 px-3 py-2 text-slate-100 outline-none focus:border-[#2dd4bf]/50"
              />
            </label>
          </div>
        </Card>

        <Card className="p-5">
          <CardHeader>
            <div>
              <CardTitle>新資料</CardTitle>
              <CardDescription>產生後會覆蓋同代號、同週期的既有產生資料。</CardDescription>
            </div>
            <WandSparkles className="h-5 w-5 text-slate-500" />
          </CardHeader>
          <div className="grid gap-4">
            <label className="grid gap-2 text-sm">
              <span className="text-slate-400">新研究商品 ID</span>
              <input
                value={targetId}
                maxLength={32}
                onChange={(event) => setTargetId(event.target.value.toUpperCase())}
                className="rounded-lg border border-white/10 bg-slate-950 px-3 py-2 font-mono text-slate-100 outline-none focus:border-[#2dd4bf]/50"
              />
            </label>
            <label className="grid gap-2 text-sm">
              <span className="text-slate-400">新資料代號</span>
              <input
                value={targetSymbol}
                maxLength={32}
                onChange={(event) => setTargetSymbol(event.target.value.toUpperCase())}
                className="rounded-lg border border-white/10 bg-slate-950 px-3 py-2 font-mono text-slate-100 outline-none focus:border-[#2dd4bf]/50"
              />
            </label>
            <label className="grid gap-2 text-sm">
              <span className="text-slate-400">顯示名稱</span>
              <input
                value={targetName}
                onChange={(event) => setTargetName(event.target.value)}
                className="rounded-lg border border-white/10 bg-slate-950 px-3 py-2 text-slate-100 outline-none focus:border-[#2dd4bf]/50"
              />
            </label>
            <Button
              type="submit"
              icon={WandSparkles}
              loading={generateMutation.isPending}
              disabled={!sourceId || !startDate || !endDate || !targetId || !targetSymbol || !Number.isFinite(multiplier) || multiplier <= 0}
              className="w-full"
            >
              產生行情資料
            </Button>
            {errorMessage ? <div className="rounded-md border border-[#f87171]/25 bg-[#f87171]/10 px-3 py-2 text-sm text-[#fecaca]">{errorMessage}</div> : null}
          </div>
        </Card>
      </form>

      {lastResult ? (
        <Card className="p-5">
          <div className="flex flex-col gap-4 md:flex-row md:items-start md:justify-between">
            <div>
              <div className="flex items-center gap-2 text-sm font-medium text-[#99f6e4]">
                <CheckCircle2 className="h-4 w-4" />
                已產生
              </div>
              <h2 className="mt-2 text-xl font-semibold text-slate-100">{lastResult.instrument.display_name}</h2>
              <p className="mt-2 text-sm text-slate-400">
                {lastResult.instrument.symbol} / {intervalLabels[lastResult.interval] ?? lastResult.interval} / {lastResult.price_adjustment_label}
              </p>
            </div>
            <div className="grid gap-2 text-sm md:min-w-80">
              <InfoRow label="產生筆數" value={lastResult.generated_bars.toLocaleString("zh-TW")} />
              <InfoRow label="寫入筆數" value={lastResult.stored_bars.toLocaleString("zh-TW")} />
              <InfoRow label="第一筆" value={formatMs(lastResult.first_open_ms)} />
              <InfoRow label="最後一筆" value={formatMs(lastResult.last_open_ms)} />
              <InfoRow label="首筆基準" value={lastResult.used_fallback_baseline ? "使用母資料首筆開盤價" : "使用母資料前一筆收盤價"} />
            </div>
          </div>
        </Card>
      ) : null}
    </div>
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
