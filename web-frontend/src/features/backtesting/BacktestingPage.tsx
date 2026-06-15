import { FormEvent, useMemo, useState } from "react";
import { useSearchParams } from "react-router-dom";
import { useMutation, useQuery } from "@tanstack/react-query";
import { Area, AreaChart, CartesianGrid, Legend, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";
import { PlayCircle } from "lucide-react";
import { useI18n } from "../../i18n/useI18n";
import { formatMoney, formatPercent } from "../../shared/lib/format";
import { mockBacktest, mockGenomes, mockInstances } from "../../shared/lib/mockData";
import { backtestsApi, type BacktestResult } from "../../shared/services/backtests";
import { evolutionApi } from "../../shared/services/evolution";
import { instancesApi } from "../../shared/services/instances";
import { Button } from "../../shared/ui/Button";
import { Card, CardDescription, CardHeader, CardTitle } from "../../shared/ui/Card";
import { cn } from "../../shared/lib/cn";

export function BacktestingPage() {
  const { t } = useI18n();
  const [params] = useSearchParams();
  const [source, setSource] = useState<"champion" | "candidate" | "custom">("champion");
  const [candidateId, setCandidateId] = useState(Number(params.get("genome")) || mockGenomes[1].id);
  const [customJson, setCustomJson] = useState("{\n  \n}");
  const [result, setResult] = useState<BacktestResult | null>(mockBacktest);
  const { data: instances = mockInstances } = useQuery({
    queryKey: ["instances"],
    queryFn: () => instancesApi.list().catch(() => mockInstances)
  });
  const { data: genomes = mockGenomes } = useQuery({
    queryKey: ["genomes"],
    queryFn: () => evolutionApi.listGenomes().catch(() => mockGenomes)
  });
  const candidates = genomes.filter((genome) => genome.role !== "archived");
  const selectedInstance = useMemo(() => instances[0], [instances]);
  const startMutation = useMutation({
    mutationFn: async () => {
      const payload = {
        instance_id: selectedInstance?.id,
        source,
        candidate_id: candidateId,
        custom_params: source === "custom" ? JSON.parse(customJson || "{}") : undefined
      };
      await backtestsApi.create(payload).catch(() => ({ id: mockBacktest.id }));
      return mockBacktest;
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
            <CardTitle>{selectedInstance?.name ?? t("dashboard.noInstance")}</CardTitle>
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
              value={candidateId}
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
          <Button icon={PlayCircle} loading={startMutation.isPending} type="submit">{t("backtesting.start")}</Button>
        </form>
      </Card>
      {result ? (
        <>
          <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-4">
            {[
              [t("backtesting.totalReturn"), formatPercent(result.total_return), "text-[#bbf7d0]"],
              [t("backtesting.alpha"), formatPercent(result.alpha), "text-[#99f6e4]"],
              [t("backtesting.maxDrawdown"), formatPercent(result.max_drawdown), "text-[#fecaca]"],
              [t("backtesting.sharpe"), (result.sharpe ?? 0).toFixed(2), "text-slate-100"]
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
                  <div className="text-sm text-slate-500">{label}</div>
                  <div className="mt-2 font-mono text-xl text-slate-100">{value.toFixed(2)}</div>
                </div>
              ))}
            </div>
          </Card>
        </>
      ) : null}
    </section>
  );
}
