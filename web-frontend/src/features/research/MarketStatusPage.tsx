import { useQuery } from "@tanstack/react-query";
import { Activity, Gauge } from "lucide-react";
import { formatPercent, shortDateTime } from "../../shared/lib/format";
import { researchApi, type ResearchStatusItem } from "../../shared/services/research";
import { Card, CardDescription, CardHeader, CardTitle } from "../../shared/ui/Card";
import { cn } from "../../shared/lib/cn";

const diagLabels: Record<string, string> = {
  total_equity: "估算總權益",
  reserve_floor: "保留現金",
  spendable_usdt: "可部署資金",
  current_weight: "目前權重",
  target_weight: "目標權重",
  delta_weight: "權重差",
  signal: "綜合訊號",
  volatility_ratio: "波動比",
  market_beta: "市場 Beta 倍率",
  market_trend_slope: "趨勢斜率",
  market_drawdown: "回撤比例",
  macro_regime_multiplier: "宏觀狀態倍率"
};

const stateLabels: Record<string, string> = {
  BULL_TREND: "牛市趨勢",
  BEAR_TREND: "熊市趨勢",
  QUIET: "平靜",
  SHOCK: "震盪"
};

function formatNumber(value: unknown) {
  if (typeof value !== "number") return String(value ?? "-");
  if (Math.abs(value) <= 1) return value.toFixed(4);
  return value.toLocaleString("zh-TW", { maximumFractionDigits: 4 });
}

function stateLabel(value?: string) {
  return stateLabels[value ?? ""] ?? value ?? "尚無判斷";
}

function StatusCard({ item }: { item: ResearchStatusItem }) {
  const ready = item.status === "ready";
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
          {item.status === "missing_champion" ? "尚未有此標的的採用參數。" : "尚未有足夠的完成日 K 資料。"}
        </div>
      ) : (
        <div className="space-y-4">
          <div className="grid gap-3 md:grid-cols-3">
            <Metric label="市場狀態" value={stateLabel(item.market_state)} />
            <Metric label="目標權重" value={item.target_weight !== undefined ? formatPercent(item.target_weight) : "-"} highlight />
            <Metric label="最新完成日 K" value={item.latest_bar ? `${shortDateTime(item.latest_bar.time)} · ${formatNumber(item.latest_bar.close)}` : "-"} />
          </div>
          <div className="rounded-lg border border-white/[0.04] bg-white/[0.02] p-3 text-xs leading-relaxed text-slate-500">
            市場狀態只使用已完成的日 K 收盤價計算，一天最多更新一次。即時價格若未來顯示，會獨立列出，不會參與此判斷。
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
            <summary className="cursor-pointer text-sm font-semibold text-slate-300">泛用參數值</summary>
            <pre className="mt-3 max-h-72 overflow-auto text-xs leading-relaxed text-slate-300">{JSON.stringify(item.parameter_values ?? {}, null, 2)}</pre>
          </details>
        </div>
      )}
    </Card>
  );
}

function Metric({ label, value, highlight = false }: { label: string; value: string; highlight?: boolean }) {
  return (
    <div className="rounded-lg border border-white/[0.04] bg-white/[0.02] p-3">
      <div className="text-xs text-slate-500">{label}</div>
      <div className={cn("mt-1 font-mono text-sm", highlight ? "text-[#99f6e4]" : "text-slate-100")}>{value}</div>
    </div>
  );
}

export function MarketStatusPage() {
  const query = useQuery({ queryKey: ["research-status"], queryFn: () => researchApi.status(), refetchInterval: 60_000 });
  const items = query.data?.items ?? [];
  return (
    <section className="space-y-4">
      <div>
        <h1 className="text-2xl font-bold text-slate-100">市場狀態</h1>
        <p className="mt-1 text-sm text-slate-400">套用各標的目前採用參數，查看最新完成日 K 下的狀態判斷與目標權重。</p>
      </div>
      {query.isLoading ? <Card className="p-4 text-sm text-slate-500">載入中...</Card> : null}
      {query.error ? <Card className="p-4 text-sm text-[#fecaca]">{String(query.error.message)}</Card> : null}
      <div className="grid gap-4">
        {items.map((item) => <StatusCard key={item.instrument_id} item={item} />)}
      </div>
      <div className="flex items-center gap-2 text-xs text-slate-500">
        <Activity className="h-4 w-4" />
        此頁只做研究判讀，不會送出任何交易指令。
      </div>
    </section>
  );
}
