import { FormEvent, useState } from "react";
import { useSearchParams } from "react-router-dom";
import { useMutation, useQuery } from "@tanstack/react-query";
import { Area, AreaChart, CartesianGrid, Legend, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";
import { PlayCircle } from "lucide-react";
import { useI18n } from "../../i18n/useI18n";
import { formatMoney, formatPercent } from "../../shared/lib/format";
import { backtestsApi, type BacktestResult } from "../../shared/services/backtests";
import { evolutionApi } from "../../shared/services/evolution";
import { Button } from "../../shared/ui/Button";
import { Card, CardDescription, CardHeader, CardTitle } from "../../shared/ui/Card";
import { cn } from "../../shared/lib/cn";

function windowLabel(key: string) {
  const map: Record<string, string> = { "6m": "6 個月", "2y": "2 年", "5y": "5 年", "10y": "完整歷史" };
  return map[key] ?? key;
}

export function BacktestingPage() {
  const { t } = useI18n();
  const [params] = useSearchParams();
  const [source, setSource] = useState<"champion" | "candidate" | "custom">("champion");
  const [candidateId, setCandidateId] = useState(Number(params.get("genome")) || 0);
  const [customJson, setCustomJson] = useState("{\n  \n}");
  const [result, setResult] = useState<BacktestResult | null>(null);
  const { data: genomes = [] } = useQuery({
    queryKey: ["genomes"],
    queryFn: () => evolutionApi.listGenomes()
  });
  const candidates = genomes.filter((genome) => genome.role === "candidate" || genome.role === "challenger");
  const startMutation = useMutation({
    mutationFn: async () => {
      const selectedCandidateId = candidateId || candidates[0]?.id;
      const payload = {
        symbol: "BTCUSDT",
        source,
        candidate_id: source === "candidate" ? selectedCandidateId : undefined,
        custom_params: source === "custom" ? JSON.parse(customJson || "{}") : undefined
      };
      return backtestsApi.create(payload);
    },
    onSuccess: setResult
  });

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    startMutation.mutate();
  }

  const chartData = (result?.nav ?? []).map((item) => ({
    ...item,
    label: new Intl.DateTimeFormat("zh-TW", { month: "2-digit", day: "2-digit" }).format(new Date(item.time))
  }));

  return (
    <section className="space-y-4">
      <div>
        <h1 className="text-2xl font-bold text-slate-100">{t("backtesting.title")}</h1>
        <p className="mt-1 text-sm text-slate-400">{t("backtesting.subtitle")}</p>
      </div>
      <Card>
        <CardHeader>
          <div>
            <CardTitle>BTCUSDT</CardTitle>
            <CardDescription>{t("backtesting.source")}</CardDescription>
          </div>
        </CardHeader>
        <form className="space-y-4" onSubmit={submit}>
          <div className="grid gap-2 md:grid-cols-3">
            {[
              ["champion", t("backtesting.useChampion")],
              ["candidate", t("backtesting.useCandidate")],
              ["custom", t("backtesting.customJson")]
            ].map(([value, label]) => (
              <button
                key={value}
                type="button"
                className={cn(
                  "rounded-lg border px-3 py-2 text-sm transition",
                  source === value ? "border-[#2dd4bf]/40 bg-[#2dd4bf]/10 text-[#99f6e4]" : "border-white/[0.04] text-slate-400"
                )}
                onClick={() => setSource(value as typeof source)}
              >
                {label}
              </button>
            ))}
          </div>
          {source === "candidate" ? (
            <select
              className="h-11 w-full rounded-lg border border-slate-700 bg-slate-900/80 px-3 font-mono text-sm text-slate-100 outline-none focus:border-[#2dd4bf]"
              value={candidateId || candidates[0]?.id || ""}
              onChange={(event) => setCandidateId(Number(event.target.value))}
            >
              {candidates.map((genome) => (
                <option key={genome.id} value={genome.id}>
                  #{genome.id} · {genome.score_total.toFixed(3)}
                </option>
              ))}
            </select>
          ) : null}
          {source === "custom" ? (
            <textarea
              className="h-40 w-full rounded-lg border border-slate-700 bg-slate-950/80 p-3 font-mono text-sm text-slate-100 outline-none focus:border-[#2dd4bf]"
              value={customJson}
              onChange={(event) => setCustomJson(event.target.value)}
            />
          ) : null}
          <Button icon={PlayCircle} loading={startMutation.isPending} type="submit" disabled={source === "candidate" && candidates.length === 0}>
            {t("backtesting.start")}
          </Button>
          {source === "candidate" && candidates.length === 0 ? <div className="text-sm text-slate-500">{t("backtesting.noCandidate")}</div> : null}
          {startMutation.error ? <div className="text-sm text-[#fecaca]">{String(startMutation.error.message)}</div> : null}
        </form>
      </Card>
      {result ? (
        <>
          <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-4">
            {[
              [t("backtesting.totalReturn"), formatPercent(result.total_return), "text-[#bbf7d0]"],
              [t("backtesting.alpha"), formatPercent(result.alpha), "text-[#99f6e4]"],
              [t("backtesting.maxDrawdown"), formatPercent(result.max_drawdown), "text-[#fecaca]"],
              [t("backtesting.finalEquity"), formatMoney(result.final_equity), "text-slate-100"]
            ].map(([label, value, color]) => (
              <Card key={label} className="p-4">
                <div className="text-sm text-slate-500">{label}</div>
                <div className={cn("mt-2 font-mono text-2xl font-semibold", color)}>{value}</div>
              </Card>
            ))}
          </div>
          <Card>
            <CardHeader>
              <div>
                <CardTitle>{t("backtesting.nav")}</CardTitle>
                <CardDescription>{t("backtesting.benchmark")}</CardDescription>
              </div>
            </CardHeader>
            <div className="h-80">
              <ResponsiveContainer width="100%" height="100%">
                <AreaChart data={chartData} margin={{ left: 0, right: 10, top: 10, bottom: 0 }}>
                  <defs>
                    <linearGradient id="backtestFill" x1="0" x2="0" y1="0" y2="1">
                      <stop offset="5%" stopColor="#2dd4bf" stopOpacity={0.3} />
                      <stop offset="95%" stopColor="#2dd4bf" stopOpacity={0.02} />
                    </linearGradient>
                  </defs>
                  <CartesianGrid stroke="rgba(148,163,184,0.08)" vertical={false} />
                  <XAxis dataKey="label" stroke="#64748b" tickLine={false} axisLine={false} fontSize={12} />
                  <YAxis stroke="#64748b" tickLine={false} axisLine={false} fontSize={12} tickFormatter={(value) => `${Math.round(value / 1000)}k`} />
                  <Tooltip
                    contentStyle={{ background: "#020617", border: "1px solid rgba(255,255,255,0.08)", borderRadius: 8 }}
                    formatter={(value: number) => [formatMoney(value), "USDT"]}
                  />
                  <Legend />
                  <Area name={t("backtesting.result")} type="monotone" dataKey="total_assets" stroke="#2dd4bf" strokeWidth={2} fill="url(#backtestFill)" />
                  <Area name={t("backtesting.benchmark")} type="monotone" dataKey="benchmark" stroke="#64748b" strokeDasharray="5 5" fill="transparent" />
                </AreaChart>
              </ResponsiveContainer>
            </div>
          </Card>
          <Card>
            <CardHeader>
              <div>
                <CardTitle>{t("backtesting.windows")}</CardTitle>
                <CardDescription>{t("backtesting.subtitle")}</CardDescription>
              </div>
            </CardHeader>
            <div className="grid gap-3 md:grid-cols-4">
              {Object.entries(result.windows).map(([label, value]) => (
                <div key={label} className="rounded-lg border border-white/[0.04] bg-white/[0.02] p-4">
                  <div className="text-sm text-slate-500">{windowLabel(label)}</div>
                  <div className="mt-2 font-mono text-xl text-slate-100">{value.toFixed(2)}</div>
                </div>
              ))}
            </div>
          </Card>
        </>
      ) : (
        <Card className="p-4 text-sm text-slate-500">{t("backtesting.noResult")}</Card>
      )}
    </section>
  );
}
