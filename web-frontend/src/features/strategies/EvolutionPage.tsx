import { FormEvent, useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { CheckCircle2, FlaskConical } from "lucide-react";
import { useI18n } from "../../i18n/useI18n";
import { formatPercent, relativeTime } from "../../shared/lib/format";
import { evolutionApi, type GenomeRecord } from "../../shared/services/evolution";
import { Button } from "../../shared/ui/Button";
import { Card, CardDescription, CardHeader, CardTitle } from "../../shared/ui/Card";
import { StatusBadge } from "../../shared/ui/StatusBadge";
import { cn } from "../../shared/lib/cn";

function roleLabel(t: (key: string) => string, role: GenomeRecord["role"]) {
  if (role === "champion") return t("evolution.champion");
  if (role === "archived" || role === "retired") return t("evolution.archived");
  return t("evolution.candidate");
}

function windowLabel(key: string) {
  const map: Record<string, string> = { "6m": "6 個月", "2y": "2 年", "5y": "5 年", "10y": "完整歷史" };
  return map[key] ?? key;
}

const intervalOptions = [
  ["1d", "1 天"],
  ["1h", "1 小時"],
  ["15m", "15 分鐘"],
  ["5m", "5 分鐘"],
  ["1m", "1 分鐘"]
];

function EvolutionPanel() {
  const { t } = useI18n();
  const queryClient = useQueryClient();
  const [expanded, setExpanded] = useState(false);
  const [interval, setInterval] = useState("1d");
  const [population, setPopulation] = useState(300);
  const [generations, setGenerations] = useState(25);
  const [mode, setMode] = useState<"inherit" | "random_once" | "manual">("inherit");
  const [manualJson, setManualJson] = useState(
    '{\n  "policy": {\n    "initial_usdt": 1000,\n    "monthly_inject_usdt": 100,\n    "cold_sealed_btc": 0\n  },\n  "risk": {\n    "max_drawdown_pct": 0.88,\n    "fee_rate": 0.001,\n    "lot_step": 0.000001,\n    "lot_min": 0.00001\n  }\n}'
  );
  const { data: overview } = useQuery({
    queryKey: ["evolution-tasks"],
    queryFn: () => evolutionApi.listTasks(),
    refetchInterval: 5_000
  });
  const running = overview?.current_task ?? overview?.tasks.find((task) => task.status === "running");
  const createMutation = useMutation({
    mutationFn: () => {
      const spawn_point = mode === "manual" ? JSON.parse(manualJson) : undefined;
      return evolutionApi.createTask({
        strategy_id: "sigmoid-dca-btc",
        interval,
        pop_size: population,
        max_generations: generations,
        spawn_mode: mode,
        spawn_point
      });
    },
    onSuccess: () => {
      setExpanded(false);
      queryClient.invalidateQueries({ queryKey: ["evolution-tasks"] });
    }
  });

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    createMutation.mutate();
  }

  if (running) {
    const current = running.current_generation ?? Math.round((running.progress || 0) * (running.max_generations ?? 25));
    const max = running.max_generations ?? 25;
    return (
      <Card>
        <CardHeader>
          <div>
            <CardTitle>{t("evolution.runningTask")}</CardTitle>
            <CardDescription>{relativeTime(running.created_at)}</CardDescription>
          </div>
          <StatusBadge status="running" />
        </CardHeader>
        <div className="space-y-4">
          <div>
            <div className="mb-2 flex justify-between text-sm">
              <span className="text-slate-400">{t("evolution.currentGeneration")}</span>
              <span className="font-mono text-slate-200">{current} / {max}</span>
            </div>
            <div className="h-2 overflow-hidden rounded-full bg-slate-800">
              <div className="h-full rounded-full bg-[#2dd4bf]" style={{ width: `${Math.min(100, running.progress * 100)}%` }} />
            </div>
          </div>
          <div className="grid gap-3 md:grid-cols-2">
            <div className="rounded-lg border border-white/[0.04] bg-white/[0.02] p-4">
              <div className="text-sm text-slate-500">{t("evolution.bestScore")}</div>
              <div className="mt-2 font-mono text-2xl text-slate-100">{(running.best_score ?? 0).toFixed(3)}</div>
            </div>
            <div className="rounded-lg border border-[#f87171]/10 bg-[#f87171]/5 p-4">
              <div className="text-sm text-slate-500">{t("evolution.maxDrawdown")}</div>
              <div className="mt-2 font-mono text-2xl text-[#fecaca]">{formatPercent(running.max_drawdown ?? 0)}</div>
            </div>
          </div>
        </div>
      </Card>
    );
  }

  return (
    <Card>
      <CardHeader>
        <div>
          <CardTitle>{t("evolution.optimize")}</CardTitle>
          <CardDescription>{t("evolution.subtitle")}</CardDescription>
        </div>
        <Button icon={FlaskConical} onClick={() => setExpanded((value) => !value)}>{t("evolution.startNew")}</Button>
      </CardHeader>
      {expanded ? (
        <form className="grid gap-4 md:grid-cols-2" onSubmit={submit}>
          <label>
            <span className="mb-2 block text-sm text-slate-300">{t("evolution.interval")}</span>
            <select
              className="h-11 w-full rounded-lg border border-slate-700 bg-slate-900/80 px-3 text-sm text-slate-100 outline-none focus:border-[#2dd4bf]"
              value={interval}
              onChange={(event) => setInterval(event.target.value)}
            >
              {intervalOptions.map(([value, label]) => (
                <option key={value} value={value}>
                  {label}
                </option>
              ))}
            </select>
          </label>
          <label>
            <span className="mb-2 block text-sm text-slate-300">{t("evolution.population")}</span>
            <input
              className="h-11 w-full rounded-lg border border-slate-700 bg-slate-900/80 px-3 font-mono text-sm text-slate-100 outline-none focus:border-[#2dd4bf]"
              type="number"
              min="10"
              max="500"
              value={population}
              onChange={(event) => setPopulation(Number(event.target.value))}
            />
          </label>
          <label>
            <span className="mb-2 block text-sm text-slate-300">{t("evolution.generations")}</span>
            <input
              className="h-11 w-full rounded-lg border border-slate-700 bg-slate-900/80 px-3 font-mono text-sm text-slate-100 outline-none focus:border-[#2dd4bf]"
              type="number"
              min="5"
              max="50"
              value={generations}
              onChange={(event) => setGenerations(Number(event.target.value))}
            />
          </label>
          <div className="md:col-span-2">
            <div className="mb-2 text-sm text-slate-300">{t("evolution.inheritMode")}</div>
            <div className="grid gap-2 md:grid-cols-3">
              {[
                ["inherit", t("evolution.inheritChampion")],
                ["random_once", t("evolution.randomExplore")],
                ["manual", t("evolution.manual")]
              ].map(([value, label]) => (
                <button
                  key={value}
                  type="button"
                  className={cn(
                    "rounded-lg border px-3 py-2 text-sm transition",
                    mode === value ? "border-[#2dd4bf]/40 bg-[#2dd4bf]/10 text-[#99f6e4]" : "border-white/[0.04] text-slate-400"
                  )}
                  onClick={() => setMode(value as typeof mode)}
                >
                  {label}
                </button>
              ))}
            </div>
          </div>
          {mode === "manual" ? (
            <label className="md:col-span-2">
              <span className="mb-2 block text-sm text-slate-300">{t("evolution.manualJson")}</span>
              <textarea
                className="h-40 w-full rounded-lg border border-slate-700 bg-slate-950/80 p-3 font-mono text-sm text-slate-100 outline-none focus:border-[#2dd4bf]"
                value={manualJson}
                onChange={(event) => setManualJson(event.target.value)}
              />
            </label>
          ) : null}
          <div className="md:col-span-2">
            <Button type="submit" loading={createMutation.isPending}>{t("evolution.submitTask")}</Button>
            {createMutation.error ? <div className="mt-2 text-sm text-[#fecaca]">{String(createMutation.error.message)}</div> : null}
          </div>
        </form>
      ) : null}
    </Card>
  );
}

function TaskQueueView() {
  const { t } = useI18n();
  const { data: overview, isLoading } = useQuery({
    queryKey: ["evolution-tasks"],
    queryFn: () => evolutionApi.listTasks(),
    refetchInterval: 5_000
  });
  const tasks = overview?.tasks ?? [];
  return (
    <Card>
      <CardHeader>
        <div>
          <CardTitle>{t("evolution.queue")}</CardTitle>
          <CardDescription>{t("evolution.subtitle")}</CardDescription>
        </div>
      </CardHeader>
      <div className="space-y-3">
        {isLoading ? <div className="text-sm text-slate-500">{t("common.loading")}</div> : null}
        {!isLoading && tasks.length === 0 ? <div className="text-sm text-slate-500">{t("evolution.noTasks")}</div> : null}
        {tasks.map((task) => (
          <div key={task.id} className="flex items-center justify-between gap-3 rounded-lg border border-white/[0.04] bg-white/[0.02] p-3">
            <div>
              <div className="font-mono text-sm text-slate-200">#{task.id}</div>
              <div className="mt-1 text-xs text-slate-500">{relativeTime(task.created_at)}</div>
            </div>
            <StatusBadge status={task.status} />
            <div className="text-right font-mono text-sm text-slate-300">{((task.best_score ?? 0) * 100).toFixed(1)}</div>
          </div>
        ))}
      </div>
    </Card>
  );
}

function ChampionCard({ champion }: { champion?: GenomeRecord }) {
  const { t } = useI18n();
  return (
    <Card className="border-[#2dd4bf]/20">
      <CardHeader>
        <div>
          <CardTitle>{t("evolution.champion")}</CardTitle>
          <CardDescription>{champion ? relativeTime(champion.created_at) : t("common.unknown")}</CardDescription>
        </div>
        <CheckCircle2 className="h-5 w-5 text-[#2dd4bf]" />
      </CardHeader>
      {champion ? (
        <div className="grid gap-3 md:grid-cols-2">
          <div className="rounded-lg border border-white/[0.04] bg-white/[0.02] p-4">
            <div className="text-sm text-slate-500">{t("evolution.bestScore")}</div>
            <div className="mt-2 font-mono text-2xl text-slate-100">{champion.score_total.toFixed(3)}</div>
          </div>
          <div className="rounded-lg border border-white/[0.04] bg-white/[0.02] p-4">
            <div className="text-sm text-slate-500">{t("evolution.maxDrawdown")}</div>
            <div className="mt-2 font-mono text-2xl text-[#fecaca]">{formatPercent(champion.max_drawdown)}</div>
          </div>
        </div>
      ) : (
        <div className="text-sm text-slate-500">{t("evolution.noChampion")}</div>
      )}
    </Card>
  );
}

function GenomeLibrary({ genomes }: { genomes: GenomeRecord[] }) {
  const { t } = useI18n();
  const [confirmPromote, setConfirmPromote] = useState<number | null>(null);
  const queryClient = useQueryClient();
  const promoteMutation = useMutation({
    mutationFn: (id: number) => evolutionApi.promote(id),
    onSuccess: () => {
      setConfirmPromote(null);
      queryClient.invalidateQueries({ queryKey: ["genomes"] });
      queryClient.invalidateQueries({ queryKey: ["evolution-tasks"] });
    }
  });
  return (
    <div className="grid gap-4 lg:grid-cols-2">
      {genomes.map((genome) => {
        const isChampion = genome.role === "champion";
        const canPromote = genome.role === "candidate" || genome.role === "challenger";
        return (
          <Card key={genome.id} className={cn(isChampion ? "border-[#2dd4bf]/30" : "")}>
            <CardHeader>
              <div>
                <CardTitle>{roleLabel(t, genome.role)}</CardTitle>
                <CardDescription>{relativeTime(genome.created_at)}</CardDescription>
              </div>
              <span className="font-mono text-lg font-semibold text-slate-100">{genome.score_total.toFixed(3)}</span>
            </CardHeader>
            <div className="grid grid-cols-2 gap-3">
              <div className="rounded-lg border border-white/[0.04] bg-white/[0.02] p-3">
                <div className="text-xs text-slate-500">{t("evolution.maxDrawdown")}</div>
                <div className="mt-1 font-mono text-sm text-[#fecaca]">{formatPercent(genome.max_drawdown)}</div>
              </div>
              {Object.entries(genome.window_score).map(([key, value]) => (
                <div key={key} className="rounded-lg border border-white/[0.04] bg-white/[0.02] p-3">
                  <div className="text-xs text-slate-500">{windowLabel(key)}</div>
                  <div className="mt-1 font-mono text-sm text-slate-200">{value.toFixed(2)}</div>
                </div>
              ))}
            </div>
            <div className="mt-4 flex flex-wrap gap-2">
              {canPromote ? (
                confirmPromote === genome.id ? (
                  <Button loading={promoteMutation.isPending} onClick={() => promoteMutation.mutate(genome.id)}>
                    {t("common.confirm")}
                  </Button>
                ) : (
                  <Button onClick={() => setConfirmPromote(genome.id)}>{t("evolution.promote")}</Button>
                )
              ) : null}
              {promoteMutation.error && confirmPromote === genome.id ? <div className="text-sm text-[#fecaca]">{String(promoteMutation.error.message)}</div> : null}
              <Link to={`/backtesting?genome=${genome.id}`}>
                <Button variant="secondary">{t("evolution.viewBacktest")}</Button>
              </Link>
            </div>
          </Card>
        );
      })}
    </div>
  );
}

export function EvolutionPage() {
  const { t } = useI18n();
  const [tab, setTab] = useState<"optimize" | "library">("optimize");
  const { data: genomes = [] } = useQuery({
    queryKey: ["genomes"],
    queryFn: () => evolutionApi.listGenomes()
  });
  const champion = useMemo(() => genomes.find((item) => item.role === "champion"), [genomes]);

  return (
    <section className="space-y-4">
      <div>
        <h1 className="text-2xl font-bold text-slate-100">{t("evolution.title")}</h1>
        <p className="mt-1 text-sm text-slate-400">{t("evolution.subtitle")}</p>
      </div>
      <div className="flex w-fit rounded-lg border border-white/[0.06] bg-white/[0.03] p-1">
        {[
          ["optimize", t("evolution.optimize")],
          ["library", t("evolution.library")]
        ].map(([value, label]) => (
          <button
            key={value}
            className={cn("rounded-md px-4 py-2 text-sm font-semibold transition", tab === value ? "bg-[#2dd4bf]/10 text-[#2dd4bf]" : "text-slate-500")}
            onClick={() => setTab(value as typeof tab)}
          >
            {label}
          </button>
        ))}
      </div>
      {tab === "optimize" ? (
        <div className="space-y-4">
          <EvolutionPanel />
          <TaskQueueView />
          <ChampionCard champion={champion} />
        </div>
      ) : (
        genomes.length > 0 ? <GenomeLibrary genomes={genomes} /> : <Card className="p-4 text-sm text-slate-500">{t("evolution.noGenomes")}</Card>
      )}
    </section>
  );
}
