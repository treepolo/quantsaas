import { FormEvent, useEffect, useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { AlertTriangle, CheckCircle2, Edit3, Layers3, Plus, Save, Trash2, X } from "lucide-react";
import { marketDataApi, type ResearchInstrument } from "../../shared/services/marketData";
import { researchDataApi, type IndicatorSelectionInput, type MissingPolicy, type ResearchDataset, type ResearchDatasetInput, type SeriesPreview } from "../../shared/services/researchData";
import { Button } from "../../shared/ui/Button";
import { Card, CardDescription, CardHeader, CardTitle } from "../../shared/ui/Card";
import { cn } from "../../shared/lib/cn";
import { SearchablePicker } from "../../shared/ui/SearchablePicker";

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
  forward_fill: "延續前值"
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

function msToDateInput(value?: number) {
  return value ? new Date(value).toISOString().slice(0, 10) : "";
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

function formatAvailabilityDelay(value?: { enabled: boolean; days: number }) {
  if (!value?.enabled) return "發布日當日";
  return `發布日 + ${Math.max(0, value.days || 0)} 天`;
}

function firstInterval(instrument?: ResearchInstrument) {
  return instrument?.supported_intervals?.[0] ?? "1d";
}

function datasetStart(instrument?: ResearchInstrument, interval = "1d") {
  const detected = instrument?.available_start_ms?.[interval];
  if (detected && detected > 0) return dateInputValue(new Date(detected));
  const starts = Object.values(instrument?.available_start_ms ?? {}).filter((value) => value > 0);
  if (starts.length > 0) return dateInputValue(new Date(Math.min(...starts)));
  return dateInputValue(new Date());
}

function datasetToInput(dataset: ResearchDataset): ResearchDatasetInput {
  return {
    name: dataset.name,
    notes: dataset.notes ?? "",
    primary_instrument_id: dataset.primary.instrument_id,
    primary_interval: dataset.primary.interval,
    indicators: dataset.indicators.map((item) => ({ instrument_id: item.instrument_id, interval: item.interval })),
    start_time_ms: dataset.start_time_ms,
    end_time_ms: dataset.end_time_ms,
    missing_policy: dataset.missing_policy,
    indicator_algorithm: dataset.indicator_algorithm ?? "",
    availability_delay: dataset.availability_delay ?? { enabled: false, days: 0 }
  };
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
  const queryClient = useQueryClient();
  const instrumentsQuery = useQuery({
    queryKey: ["market-data-instruments"],
    queryFn: () => marketDataApi.instruments()
  });
  const datasetsQuery = useQuery({
    queryKey: ["research-datasets"],
    queryFn: () => researchDataApi.list()
  });
  const instruments = instrumentsQuery.data?.instruments ?? [];
  const datasets = datasetsQuery.data?.datasets ?? [];
  const [editingID, setEditingID] = useState<number | null>(null);
  const [name, setName] = useState("");
  const [notes, setNotes] = useState("");
  const [primaryID, setPrimaryID] = useState("");
  const primary = instruments.find((item) => item.id === primaryID) ?? instruments[0];
  const [primaryInterval, setPrimaryInterval] = useState("1d");
  const [startDate, setStartDate] = useState(dateInputValue(new Date()));
  const [endDate, setEndDate] = useState(dateInputValue(new Date()));
  const [missingPolicy, setMissingPolicy] = useState<MissingPolicy>("forward_fill");
  const [availabilityDelayEnabled, setAvailabilityDelayEnabled] = useState(false);
  const [availabilityDelayDays, setAvailabilityDelayDays] = useState(0);
  const [indicators, setIndicators] = useState<IndicatorSelectionInput[]>([]);

  useEffect(() => {
    if (!primaryID && instruments[0]) {
      setPrimaryID(instruments[0].id);
      const nextInterval = firstInterval(instruments[0]);
      setPrimaryInterval(nextInterval);
      setStartDate(datasetStart(instruments[0], nextInterval));
    }
  }, [instruments, primaryID]);

  const input = useMemo<ResearchDatasetInput>(() => ({
    name,
    notes,
    primary_instrument_id: primary?.id ?? primaryID,
    primary_interval: primaryInterval,
    indicators,
    start_time_ms: dayStartMs(startDate),
    end_time_ms: dayEndMs(endDate),
    missing_policy: missingPolicy,
    availability_delay: { enabled: availabilityDelayEnabled, days: availabilityDelayDays }
  }), [availabilityDelayDays, availabilityDelayEnabled, endDate, indicators, missingPolicy, name, notes, primary?.id, primaryID, primaryInterval, startDate]);

  const previewMutation = useMutation({ mutationFn: () => researchDataApi.preview(input) });
  const saveMutation = useMutation({
    mutationFn: () => (editingID ? researchDataApi.update(editingID, input) : researchDataApi.create(input)),
    onSuccess: () => {
      resetForm();
      queryClient.invalidateQueries({ queryKey: ["research-datasets"] });
    }
  });
  const deleteMutation = useMutation({
    mutationFn: (id: number) => researchDataApi.delete(id),
    onSuccess: () => {
      if (editingID) resetForm();
      queryClient.invalidateQueries({ queryKey: ["research-datasets"] });
    }
  });

  const indicatorOptions = useMemo(() => instruments.filter((item) => item.id !== (primary?.id ?? primaryID)), [instruments, primary?.id, primaryID]);

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    saveMutation.mutate();
  }

  function resetForm() {
    setEditingID(null);
    setName("");
    setNotes("");
    const first = instruments[0];
    const nextInterval = firstInterval(first);
    setPrimaryID(first?.id ?? "");
    setPrimaryInterval(nextInterval);
    setStartDate(datasetStart(first, nextInterval));
    setEndDate(dateInputValue(new Date()));
    setMissingPolicy("forward_fill");
    setAvailabilityDelayEnabled(false);
    setAvailabilityDelayDays(0);
    setIndicators([]);
    previewMutation.reset();
  }

  function editDataset(dataset: ResearchDataset) {
    const next = datasetToInput(dataset);
    setEditingID(dataset.id);
    setName(next.name ?? "");
    setNotes(next.notes ?? "");
    setPrimaryID(next.primary_instrument_id);
    setPrimaryInterval(next.primary_interval);
    setStartDate(msToDateInput(next.start_time_ms));
    setEndDate(msToDateInput(next.end_time_ms));
    setMissingPolicy(next.missing_policy);
    setAvailabilityDelayEnabled(next.availability_delay?.enabled ?? false);
    setAvailabilityDelayDays(next.availability_delay?.days ?? 0);
    setIndicators(next.indicators);
    previewMutation.reset();
  }

  function changePrimary(nextID: string) {
    const next = instruments.find((item) => item.id === nextID);
    const nextInterval = firstInterval(next);
    setPrimaryID(nextID);
    setPrimaryInterval(nextInterval);
    setStartDate(datasetStart(next, nextInterval));
    setIndicators((current) => current.filter((item) => item.instrument_id !== nextID));
  }

  function changePrimaryInterval(nextInterval: string) {
    setPrimaryInterval(nextInterval);
    setStartDate(datasetStart(primary, nextInterval));
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
        <p className="mt-1 text-sm text-slate-400">建立主商品與參考指標的研究資料設定；參數搜尋頁會引用這裡保存的資料集。</p>
      </div>

      <Card>
        <CardHeader>
          <div>
            <CardTitle>{editingID ? `編輯資料集 #${editingID}` : "建立資料集"}</CardTitle>
            <CardDescription>參考指標可以留空；只要含參考指標，在正式指標演算法確認前會禁止參數搜尋。</CardDescription>
          </div>
          {editingID ? <Button type="button" variant="secondary" icon={X} onClick={resetForm}>取消編輯</Button> : null}
        </CardHeader>
        <form className="space-y-4" onSubmit={submit}>
          <div className="grid gap-4 md:grid-cols-2">
            <label>
              <span className="mb-2 block text-sm text-slate-300">資料集名稱</span>
              <input className="h-11 w-full rounded-lg border border-slate-700 bg-slate-900/80 px-3 text-sm text-slate-100 outline-none focus:border-[#2dd4bf]" value={name} onChange={(event) => setName(event.target.value)} placeholder="留空會自動命名" />
            </label>
            <label>
              <span className="mb-2 block text-sm text-slate-300">備註</span>
              <input className="h-11 w-full rounded-lg border border-slate-700 bg-slate-900/80 px-3 text-sm text-slate-100 outline-none focus:border-[#2dd4bf]" value={notes} onChange={(event) => setNotes(event.target.value)} placeholder="選填" />
            </label>
          </div>
          <div className="grid gap-4 md:grid-cols-5">
            <SearchablePicker label="主商品" value={primary?.id ?? primaryID} onChange={changePrimary} options={instruments.map((item) => ({ value: item.id, label: item.display_name, detail: `${item.symbol} · ${item.market ?? "研究標的"}` }))}/>
            <label>
              <span className="mb-2 block text-sm text-slate-300">主商品週期</span>
              <select className="h-11 w-full rounded-lg border border-slate-700 bg-slate-900/80 px-3 text-sm text-slate-100 outline-none focus:border-[#2dd4bf]" value={primaryInterval} onChange={(event) => changePrimaryInterval(event.target.value)}>
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

          <div className="grid gap-3 rounded-lg border border-white/[0.06] bg-white/[0.02] p-3 md:grid-cols-[auto_180px_1fr]">
            <label className="flex items-center gap-2 text-sm text-slate-300">
              <input className="h-4 w-4 accent-[#2dd4bf]" type="checkbox" checked={availabilityDelayEnabled} onChange={(event) => setAvailabilityDelayEnabled(event.target.checked)} />
              發布日期延後可用
            </label>
            <label>
              <span className="mb-2 block text-xs text-slate-500">延後天數</span>
              <input className="h-10 w-full rounded-lg border border-slate-700 bg-slate-900/80 px-3 text-sm text-slate-100 outline-none focus:border-[#2dd4bf]" type="number" min={0} max={3650} step={1} value={availabilityDelayDays} onChange={(event) => setAvailabilityDelayDays(Math.max(0, Number(event.target.value) || 0))} disabled={!availabilityDelayEnabled} />
            </label>
            <div className="flex items-end text-xs leading-5 text-slate-500">
              未勾選時，參考指標於發布日期當日視為可用；勾選後會改成發布日期加上指定天數後才可用。
            </div>
          </div>

          <div className="rounded-lg border border-white/[0.06] bg-white/[0.02] p-3">
            <div className="flex flex-wrap items-center justify-between gap-3">
              <div>
                <div className="text-sm font-semibold text-slate-100">參考指標</div>
                <div className="mt-1 text-xs text-slate-500">可加入多個，也可以完全不加。</div>
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
                    <SearchablePicker label="序列" value={indicator.instrument_id} onChange={(value) => updateIndicator(index, { instrument_id: value, interval: firstInterval(instruments.find((item) => item.id === value)) })} options={indicatorOptions.concat(selected && !indicatorOptions.some((item) => item.id === selected.id) ? [selected] : []).map((item) => ({ value: item.id, label: item.display_name, detail: `${item.symbol} · ${item.market ?? "參考指標"}` }))}/>
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
              {!indicators.length ? <div className="rounded-lg border border-white/[0.04] px-3 py-4 text-sm text-slate-500">目前沒有參考指標；此資料集會維持既有單商品模式。</div> : null}
            </div>
          </div>

          <div className="flex flex-wrap gap-2">
            <Button icon={Layers3} loading={previewMutation.isPending} type="button" variant="secondary" onClick={() => previewMutation.mutate()}>
              預覽對齊
            </Button>
            <Button icon={Save} loading={saveMutation.isPending} type="submit">
              {editingID ? "儲存修改" : "建立資料集"}
            </Button>
          </div>
        </form>
        {previewMutation.error ? <div className="mt-4 text-sm text-[#fecaca]">{String(previewMutation.error.message)}</div> : null}
        {saveMutation.error ? <div className="mt-4 text-sm text-[#fecaca]">{String(saveMutation.error.message)}</div> : null}
      </Card>

      {previewMutation.data ? (
        <Card>
          <CardHeader>
            <div>
              <CardTitle>對齊預覽</CardTitle>
              <CardDescription>預覽只檢查資料品質；儲存後才會出現在參數搜尋頁。</CardDescription>
            </div>
            <div className={cn("rounded-full border px-3 py-1 text-xs", previewMutation.data.can_search ? "border-[#2dd4bf]/30 bg-[#2dd4bf]/10 text-[#99f6e4]" : "border-[#f59e0b]/30 bg-[#f59e0b]/10 text-[#fde68a]")}>
              {previewMutation.data.can_search ? "可搜尋" : "禁止搜尋"}
            </div>
          </CardHeader>
          <div className="grid gap-3 text-sm md:grid-cols-4">
            <Metric label="對齊時間點" value={previewMutation.data.aligned_rows.toLocaleString("zh-TW")} />
            <Metric label="參考指標數" value={previewMutation.data.reference_count.toLocaleString("zh-TW")} />
            <Metric label="缺值策略" value={missingPolicyLabels[previewMutation.data.missing_policy]} />
            <Metric label="可用時間" value={formatAvailabilityDelay(previewMutation.data.availability_delay)} />
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

      <Card>
        <CardHeader>
          <div>
            <CardTitle>已建立資料集</CardTitle>
            <CardDescription>參數搜尋頁會從這份清單選擇資料來源。</CardDescription>
          </div>
        </CardHeader>
        <div className="grid gap-3">
          {datasets.map((dataset) => (
            <div key={dataset.id} className="rounded-lg border border-white/[0.05] bg-white/[0.02] p-4">
              <div className="flex flex-wrap items-start justify-between gap-3">
                <div>
                  <div className="text-base font-semibold text-slate-100">#{dataset.id} · {dataset.name}</div>
                  <div className="mt-1 text-sm text-slate-400">
                    {dataset.primary.display_name} · {intervalLabels[dataset.primary.interval] ?? dataset.primary.interval} · {formatMs(dataset.start_time_ms)} - {formatMs(dataset.end_time_ms)}
                  </div>
                  <div className="mt-1 text-xs text-slate-500">
                    參考指標 {dataset.indicators.length} 個 · 缺值策略 {missingPolicyLabels[dataset.missing_policy]} · 可用時間 {formatAvailabilityDelay(dataset.availability_delay)}
                  </div>
                  {dataset.notes ? <div className="mt-2 text-sm text-slate-300">{dataset.notes}</div> : null}
                  {!dataset.can_search ? <div className="mt-2 text-sm text-[#fde68a]">{dataset.search_blocked_reason}</div> : null}
                </div>
                <div className="flex flex-wrap gap-2">
                  {dataset.can_search ? <CheckCircle2 className="h-5 w-5 text-[#2dd4bf]" /> : <AlertTriangle className="h-5 w-5 text-[#fbbf24]" />}
                  <Button type="button" variant="secondary" icon={Edit3} onClick={() => editDataset(dataset)}>編輯</Button>
                  <Button type="button" variant="danger" icon={Trash2} loading={deleteMutation.isPending} onClick={() => deleteMutation.mutate(dataset.id)}>刪除</Button>
                </div>
              </div>
            </div>
          ))}
          {!datasets.length ? <div className="rounded-lg border border-white/[0.04] px-4 py-6 text-sm text-slate-500">尚未建立資料集。</div> : null}
        </div>
      </Card>
    </section>
  );
}
