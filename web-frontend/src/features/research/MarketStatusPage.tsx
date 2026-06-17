import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Activity, Gauge } from "lucide-react";
import { formatMoney, formatPercent, shortDateTime } from "../../shared/lib/format";
import { researchApi, type ResearchStatusItem } from "../../shared/services/research";
import { Card, CardDescription, CardHeader, CardTitle } from "../../shared/ui/Card";
import { cn } from "../../shared/lib/cn";

const storageKey = "quantsaas.marketStatus.positionSimulation";

const diagLabels: Record<string, string> = {
  total_equity: "估算總資產",
  reserve_floor: "保留現金",
  spendable_usdt: "可配置資金",
  current_weight: "空倉參考目前權重",
  target_weight: "空倉參考目標權重",
  delta_weight: "空倉參考權重差",
  signal: "綜合訊號",
  volatility_ratio: "波動比",
  market_beta: "市場 Beta 倍率",
  market_trend_slope: "趨勢斜率",
  market_drawdown: "回撤比例",
  macro_regime_multiplier: "定投狀態倍率"
};

const stateLabels: Record<string, string> = {
  BULL_TREND: "牛市趨勢",
  BEAR_TREND: "熊市趨勢",
  QUIET: "平靜",
  SHOCK: "震盪"
};

type SimulationSettings = {
  startDate: string;
  initialCapital: number;
  monthlyDCA: number;
};

function defaultSettings(): SimulationSettings {
  const date = new Date();
  date.setUTCFullYear(date.getUTCFullYear() - 5);
  return {
    startDate: date.toISOString().slice(0, 10),
    initialCapital: 10000,
    monthlyDCA: 1000
  };
}

function loadSettings() {
  try {
    const raw = localStorage.getItem(storageKey);
    if (!raw) return defaultSettings();
    return { ...defaultSettings(), ...JSON.parse(raw) } as SimulationSettings;
  } catch {
    return defaultSettings();
  }
}

function dayStartMs(value: string) {
  return new Date(`${value}T00:00:00.000Z`).getTime();
}

function formatNumber(value: unknown) {
  if (typeof value !== "number") return String(value ?? "-");
  if (Math.abs(value) <= 1) return value.toFixed(4);
  return value.toLocaleString("zh-TW", { maximumFractionDigits: 4 });
}

function signedPercent(value: number | undefined) {
  if (value === undefined) return "-";
  const prefix = value > 0 ? "+" : "";
  return `${prefix}${formatPercent(value)}`;
}

function stateLabel(value?: string) {
  return stateLabels[value ?? ""] ?? value ?? "尚無判斷";
}

function StatusCard({ item }: { item: ResearchStatusItem }) {
  const ready = item.status === "ready";
  const simulation = item.position_simulation;
  return (
    <Card className={cn(ready ? "border-[#2dd4bf]/20" : "border-white/[0.04]")}>
      <CardHeader>
        <div>
          <CardTitle>{item.instrument.display_name}</CardTitle>
          <CardDescription>{item.symbol} · {item.data_source} · {item.interval}</CardDescription>
        </div>
        <Gauge className={cn("h-5 w-5", ready ? "text-[#99f6e4]" : "text-slate-600")} />
      </CardHeader>
      {!ready ? (
        <div className="text-sm text-slate-500">
          {item.status === "missing_champion" ? "尚未有這個標的的已採用參數。" : "尚未有足夠的完成日 K 資料。"}
        </div>
      ) : (
        <div className="space-y-4">
          <div className="grid gap-3 md:grid-cols-3">
            <Metric label="市場狀態" value={stateLabel(item.market_state)} />
            <Metric label="空倉參考目標權重" value={item.target_weight !== undefined ? formatPercent(item.target_weight) : "-"} highlight />
            <Metric label="最新完成日 K" value={item.latest_bar ? `${shortDateTime(item.latest_bar.time)} · ${formatNumber(item.latest_bar.close)}` : "-"} />
          </div>

          {simulation ? (
            <div className="grid gap-3 md:grid-cols-4">
              <Metric label="模擬倉淨值" value={formatMoney(simulation.latest_nav, "USD")} highlight />
              <Metric label="淨值日變化" value={signedPercent(simulation.nav_change_pct)} danger={(simulation.nav_change_pct ?? 0) < 0} />
              <Metric label="現金" value={formatMoney(simulation.cash_balance, "USD")} />
              <Metric label="投入本金" value={formatMoney(simulation.invested_capital, "USD")} />
              <Metric label="昨日實際持倉權重" value={formatPercent(simulation.previous_actual_weight)} />
              <Metric label="今日實際持倉權重" value={formatPercent(simulation.latest_actual_weight)} />
              <Metric label="昨日目標權重" value={formatPercent(simulation.previous_target_weight)} />
              <Metric label="今日目標權重" value={formatPercent(simulation.latest_target_weight)} />
              <Metric label="目標權重變化" value={signedPercent(simulation.target_weight_delta)} />
              <Metric label="當日入金" value={formatMoney(simulation.latest_contribution, "USD")} />
            </div>
          ) : (
            <div className="rounded-lg border border-white/[0.04] bg-white/[0.02] p-3 text-sm text-slate-500">
              模擬倉尚無結果，請確認起始日期落在已匯入資料範圍內，且初始資金大於 0。
            </div>
          )}

          <div className="rounded-lg border border-white/[0.04] bg-white/[0.02] p-3 text-xs leading-relaxed text-slate-500">
            市場狀態只使用已完成的日 K 收盤價計算，一天最多更新一次。空倉參考目標權重是假設帳戶全現金時的參考值；模擬倉權重則依照你的起始資金、定投入金與歷史一路重放後的持倉狀態計算。
          </div>

          <div>
            <div className="mb-2 text-sm font-semibold text-slate-300">診斷資訊</div>
            <div className="grid gap-2 md:grid-cols-2 xl:grid-cols-3">
              {Object.entries(item.diagnostics ?? {}).map(([key, value]) => (
                <div key={key} className="rounded-lg border border-white/[0.04] bg-white/[0.02] p-3">
                  <div className="text-xs text-slate-500">{diagLabels[key] ?? key}</div>
                  <div className="mt-1 font-mono text-sm text-slate-100">{formatNumber(value)}</div>
                </div>
              ))}
            </div>
          </div>

          <details className="rounded-lg border border-white/[0.04] bg-slate-950/40 p-3">
            <summary className="cursor-pointer text-sm font-semibold text-slate-300">採用參數值</summary>
            <pre className="mt-3 max-h-72 overflow-auto text-xs leading-relaxed text-slate-300">{JSON.stringify(item.parameter_values ?? {}, null, 2)}</pre>
          </details>
        </div>
      )}
    </Card>
  );
}

function Metric({ label, value, highlight = false, danger = false }: { label: string; value: string; highlight?: boolean; danger?: boolean }) {
  return (
    <div className="rounded-lg border border-white/[0.04] bg-white/[0.02] p-3">
      <div className="text-xs text-slate-500">{label}</div>
      <div className={cn("mt-1 font-mono text-sm", danger ? "text-[#fecaca]" : highlight ? "text-[#99f6e4]" : "text-slate-100")}>{value}</div>
    </div>
  );
}

function NumberInput({ label, value, min, onChange }: { label: string; value: number; min: number; onChange: (value: number) => void }) {
  return (
    <label>
      <span className="mb-2 block text-sm text-slate-300">{label}</span>
      <input
        className="h-11 w-full rounded-lg border border-slate-700 bg-slate-900/80 px-3 text-sm text-slate-100 outline-none focus:border-[#2dd4bf]"
        type="number"
        min={min}
        step="100"
        value={value}
        onChange={(event) => onChange(Number(event.target.value))}
      />
    </label>
  );
}

export function MarketStatusPage() {
  const [settings, setSettings] = useState<SimulationSettings>(() => loadSettings());
  useEffect(() => {
    localStorage.setItem(storageKey, JSON.stringify(settings));
  }, [settings]);

  const query = useQuery({
    queryKey: ["research-status", settings],
    queryFn: () =>
      researchApi.status({
        simulation_start_ms: dayStartMs(settings.startDate),
        simulation_initial_capital: settings.initialCapital,
        simulation_monthly_dca: settings.monthlyDCA
      }),
    refetchInterval: 60_000
  });
  const items = query.data?.items ?? [];

  return (
    <section className="space-y-4">
      <div>
        <h1 className="text-2xl font-bold text-slate-100">市場狀態</h1>
        <p className="mt-1 text-sm text-slate-400">套用各標的目前已採用參數，觀察最新完成日 K 的狀態判斷、空倉參考與模擬倉變化。</p>
      </div>

      <Card className="p-4">
        <div className="grid gap-3 md:grid-cols-3">
          <label>
            <span className="mb-2 block text-sm text-slate-300">模擬起始日</span>
            <input
              className="h-11 w-full rounded-lg border border-slate-700 bg-slate-900/80 px-3 text-sm text-slate-100 outline-none focus:border-[#2dd4bf]"
              type="date"
              value={settings.startDate}
              onChange={(event) => setSettings((prev) => ({ ...prev, startDate: event.target.value }))}
            />
          </label>
          <NumberInput label="初始資金" min={1} value={settings.initialCapital} onChange={(value) => setSettings((prev) => ({ ...prev, initialCapital: value }))} />
          <NumberInput label="每月定投金額" min={0} value={settings.monthlyDCA} onChange={(value) => setSettings((prev) => ({ ...prev, monthlyDCA: value }))} />
        </div>
      </Card>

      {query.isLoading ? <Card className="p-4 text-sm text-slate-500">載入中...</Card> : null}
      {query.error ? <Card className="p-4 text-sm text-[#fecaca]">{String(query.error.message)}</Card> : null}
      <div className="grid gap-4">
        {items.map((item) => <StatusCard key={item.instrument_id} item={item} />)}
      </div>
      <div className="flex items-center gap-2 text-xs text-slate-500">
        <Activity className="h-4 w-4" />
        此頁僅供研究判讀，不會送出任何交易指令。
      </div>
    </section>
  );
}
