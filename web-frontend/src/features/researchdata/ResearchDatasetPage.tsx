import { FormEvent, useEffect, useMemo, useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { AlertTriangle, Layers3, Plus, Trash2 } from "lucide-react";
import { marketDataApi, type ResearchInstrument } from "../../shared/services/marketData";
import { researchDataApi, type IndicatorSelectionInput, type MissingPolicy, type SeriesPreview } from "../../shared/services/researchData";
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

const missingPolicyLabels: Record<MissingPolicy, string> = {
  empty: "保留空值",
  forward_fill: "延續前值",
  linear: "線性插值"
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

function defaultStart() {
  const now = new Date();
  return dateInputValue(new Date(Date.UTC(now.getUTCFullYear() - 10, now.getUTCMonth(), now.getUTCDate())));
}

function firstInterval(instrument?: ResearchInstrument) {
  return instrument?.supported_intervals?.[0] ?? "1d";
}

function SeriesPreviewRow({ title, series }: { title: string; series: SeriesPreview }) {
  const hasIssue = Boolean(series.error || series.missing_rows > 0);
  return (
    <div className={cn("rounded-lg border bg-white/[0.02] p-3", hasIssue ? "border-[#f59e0b]/25" : "border-[#2dd4bf]/20")}>
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <div className="text-xs text-slate-500">{title}</div>
          <div className="mt-1 font-semibold text-slate-100">{series.display_name || series.instrument_id || "未指定"}</div>
          <div className="mt-1 font-mono text-xs text-slate-500">
            {series.symbol || "-"} · {(intervalLabels[series.interval] ?? series.interval) || "-"} · {series.data_source || "-"}
          </div>
        </div>
        {hasIssue ? <AlertTriangle className="h-5 w-5 text-[#fbbf24]" /> : <Layers3 className="h-5 w-5 text-[#99f6e4]" />}
      </div>
      <div className="mt-4 grid gap-2 text-sm sm:grid-cols-2 lg:grid-cols-4">
        <Metric label="原始筆數" value={series.raw_rows.toLocaleString("zh-TW")} />
        <Metric label="對齊筆數" value={series.aligned_rows.toLocaleString("zh-TW")} />
        <Metric label="缺值筆數" value={series.missing_rows.toLocaleString("zh-TW")} />
        <Metric label="補值筆數" value={series.filled_rows.toLocaleString("zh-TW")} />
        <Metric label="資料起點" value={formatMs(series.first_data_time_ms)} />
        <Metric label="資料終點" value={formatMs(series.last_data_time_ms)} />
        <Metric label="對齊起點" value={formatMs(series.first_aligned_time_ms)} />
        <Metric label="對齊終點" value={formatMs(series.last_aligned_time_ms)} />
      </div>
      {series.error ? <div className="mt-3 text-sm text-[#fecaca]">{series.error}</div> : null}
    </div>
  );
}

function Metric({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <div className="text-xs text-slate-500">{label}</div>
      <div className="mt-1 font-mono text-sm text-slate-200">{value}</div>
    </div>
  );
}

export function ResearchDatasetPage() {
  const instrumentsQuery = useQuery({
    queryKey: ["market-data-instruments"],
    queryFn: () => marketDataApi.instruments()
  });
  const instruments = instrumentsQuery.data?.instruments ?? [];
  const [primaryID, setPrimaryID] = useState("");
  const primary = instruments.find((item) => item.id === primaryID) ?? instruments[0];
  const [primaryInterval, setPrimaryInterval] = useState("1d");
  const [startDate, setStartDate] = useState(defaultStart());
  const [endDate, setEndDate] = useState(dateInputValue(new Date()));
  const [missingPolicy, setMissingPolicy] = useState<MissingPolicy>("empty");
  const [indicators, setIndicators] = useState<IndicatorSelectionInput[]>([]);

  useEffect(() => {
    if (!primaryID && instruments[0]) {
      setPrimaryID(instruments[0].id);
      setPrimaryInterval(firstInterval(instruments[0]));
    }
  }, [instruments, primaryID]);

  const previewMutation = useMutation({
    mutationFn: () =>
      researchDataApi.preview({
        primary_instrument_id: primary?.id ?? primaryID,
        primary_interval: primaryInterval,
        indicators,
        start_time_ms: dayStartMs(startDate),
        end_time_ms: dayEndMs(endDate),
        missing_policy: missingPolicy
      })
  });

  const indicatorOptions = useMemo(() => instruments.filter((item) => item.id !== (primary?.id ?? primaryID)), [instruments, primary?.id, primaryID]);

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    previewMutation.mutate();
  }

  function changePrimary(nextID: string) {
    const next = instruments.find((item) => item.id === nextID);
    setPrimaryID(nextID);
    setPrimaryInterval(firstInterval(next));
    setIndicators((current) => current.filter((item) => item.instrument_id !== nextID));
  }

  function addIndicator() {
    const candidate = indicatorOptions.find((item) => !indicators.some((indicator) => indicator.instrument_id === item.id));
    if (!candidate) return;
    setIndicators((current) => [...current, { instrument_id: candidate.id, interval: firstInterval(candidate) }]);
  }

  function updateIndicator(index: number, patch: Partial<IndicatorSelectionInput>) {
    setIndicators((current) => current.map((item, itemIndex) => (itemIndex === index ? { ...item, ...patch } : item)));
  }

  function removeIndicator(index: number) {
    setIndicators((current) => current.filter((_, itemIndex) => itemIndex !== index));
  }

  return (
    <section className="space-y-4">
      <div>
        <h1 className="text-2xl font-bold text-slate-100">研究資料集</h1>
        <p className="mt-1 text-sm text-slate-400">預覽主商品與參考指標對齊結果；本頁不執行參數搜尋。</p>
      </div>

      <Card>
        <CardHeader>
          <div>
            <CardTitle>資料集設定</CardTitle>
            <CardDescription>參考指標可為空；沒有參考指標時等同既有單商品模式。</CardDescription>
          </div>
        </CardHeader>
        <form className="space-y-4" onSubmit={submit}>
          <div className="grid gap-4 md:grid-cols-5">
            <label>
              <span className="mb-2 block text-sm text-slate-300">主商品</span>
              <select className="h-11 w-full rounded-lg border border-slate-700 bg-slate-900/80 px-3 text-sm text-slate-100 outline-none focus:border-[#2dd4bf]" value={primary?.id ?? primaryID} onChange={(event) => changePrimary(event.target.value)}>
                {instruments.map((item) => (
                  <option key={item.id} value={item.id}>{item.display_name}</option>
                ))}
              </select>
            </label>
            <label>
              <span className="mb-2 block text-sm text-slate-300">主商品週期</span>
              <select className="h-11 w-full rounded-lg border border-slate-700 bg-slate-900/80 px-3 text-sm text-slate-100 outline-none focus:border-[#2dd4bf]" value={primaryInterval} onChange={(event) => setPrimaryInterval(event.target.value)}>
                {(primary?.supported_intervals ?? ["1d"]).map((item) => (
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
            <label>
              <span className="mb-2 block text-sm text-slate-300">缺值策略</span>
              <select className="h-11 w-full rounded-lg border border-slate-700 bg-slate-900/80 px-3 text-sm text-slate-100 outline-none focus:border-[#2dd4bf]" value={missingPolicy} onChange={(event) => setMissingPolicy(event.target.value as MissingPolicy)}>
                {(Object.keys(missingPolicyLabels) as MissingPolicy[]).map((item) => (
                  <option key={item} value={item}>{missingPolicyLabels[item]}</option>
                ))}
              </select>
            </label>
          </div>

          <div className="rounded-lg border border-white/[0.06] bg-white/[0.02] p-3">
            <div className="flex flex-wrap items-center justify-between gap-3">
              <div>
                <div className="text-sm font-semibold text-slate-100">參考指標</div>
                <div className="mt-1 text-xs text-slate-500">可加入多個，也可保持空白。</div>
              </div>
              <Button type="button" variant="secondary" icon={Plus} onClick={addIndicator} disabled={!indicatorOptions.length}>
                新增參考指標
              </Button>
            </div>
            <div className="mt-3 grid gap-3">
              {indicators.map((indicator, index) => {
                const selected = instruments.find((item) => item.id === indicator.instrument_id);
                return (
                  <div key={`${indicator.instrument_id}-${index}`} className="grid gap-3 rounded-lg border border-white/[0.05] p-3 md:grid-cols-[1fr_220px_auto]">
                    <label>
                      <span className="mb-2 block text-xs text-slate-500">序列</span>
                      <select className="h-10 w-full rounded-lg border border-slate-700 bg-slate-900/80 px-3 text-sm text-slate-100 outline-none focus:border-[#2dd4bf]" value={indicator.instrument_id} onChange={(event) => updateIndicator(index, { instrument_id: event.target.value, interval: firstInterval(instruments.find((item) => item.id === event.target.value)) })}>
                        {indicatorOptions.concat(selected && !indicatorOptions.some((item) => item.id === selected.id) ? [selected] : []).map((item) => (
                          <option key={item.id} value={item.id}>{item.display_name}</option>
                        ))}
                      </select>
                    </label>
                    <label>
                      <span className="mb-2 block text-xs text-slate-500">週期</span>
                      <select className="h-10 w-full rounded-lg border border-slate-700 bg-slate-900/80 px-3 text-sm text-slate-100 outline-none focus:border-[#2dd4bf]" value={indicator.interval} onChange={(event) => updateIndicator(index, { interval: event.target.value })}>
                        {(selected?.supported_intervals ?? ["1d"]).map((item) => (
                          <option key={item} value={item}>{intervalLabels[item] ?? item}</option>
                        ))}
                      </select>
                    </label>
                    <div className="flex items-end">
                      <Button type="button" variant="danger" icon={Trash2} onClick={() => removeIndicator(index)}>
                        移除
                      </Button>
                    </div>
                  </div>
                );
              })}
              {!indicators.length ? <div className="rounded-lg border border-white/[0.04] px-3 py-4 text-sm text-slate-500">目前沒有參考指標，資料集會維持單商品模式。</div> : null}
            </div>
          </div>

          <Button className="w-full" icon={Layers3} loading={previewMutation.isPending} type="submit">
            預覽資料集
          </Button>
        </form>
        {previewMutation.error ? <div className="mt-4 text-sm text-[#fecaca]">{String(previewMutation.error.message)}</div> : null}
      </Card>

      {previewMutation.data ? (
        <Card>
          <CardHeader>
            <div>
              <CardTitle>預覽結果</CardTitle>
              <CardDescription>本結果只代表資料對齊狀態，不代表參考指標已參與參數搜尋。</CardDescription>
            </div>
            <div className={cn("rounded-full border px-3 py-1 text-xs", previewMutation.data.can_search ? "border-[#2dd4bf]/30 bg-[#2dd4bf]/10 text-[#99f6e4]" : "border-[#f59e0b]/30 bg-[#f59e0b]/10 text-[#fde68a]")}>
              {previewMutation.data.can_search ? "可搜尋" : "禁止搜尋"}
            </div>
          </CardHeader>
          <div className="grid gap-3 text-sm md:grid-cols-4">
            <Metric label="對齊時間點" value={previewMutation.data.aligned_rows.toLocaleString("zh-TW")} />
            <Metric label="參考指標數" value={previewMutation.data.reference_count.toLocaleString("zh-TW")} />
            <Metric label="缺值策略" value={missingPolicyLabels[previewMutation.data.missing_policy]} />
            <Metric label="資料區間" value={`${formatMs(previewMutation.data.start_time_ms)} - ${formatMs(previewMutation.data.end_time_ms)}`} />
          </div>
          {previewMutation.data.search_blocked_reason ? (
            <div className="mt-4 rounded-lg border border-[#f59e0b]/25 bg-[#f59e0b]/10 px-4 py-3 text-sm leading-6 text-[#fde68a]">
              {previewMutation.data.search_blocked_reason}
            </div>
          ) : null}
          {previewMutation.data.warnings?.length ? (
            <div className="mt-4 space-y-2">
              {previewMutation.data.warnings.map((warning) => (
                <div key={warning} className="rounded-lg border border-white/[0.06] bg-white/[0.02] px-3 py-2 text-sm text-slate-400">{warning}</div>
              ))}
            </div>
          ) : null}
          <div className="mt-4 space-y-3">
            <SeriesPreviewRow title="主商品" series={previewMutation.data.primary} />
            {previewMutation.data.indicators.map((series, index) => (
              <SeriesPreviewRow key={`${series.instrument_id}-${series.interval}-${index}`} title={`參考指標 ${index + 1}`} series={series} />
            ))}
          </div>
        </Card>
      ) : null}
    </section>
  );
}
