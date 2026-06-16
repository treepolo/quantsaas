import { FormEvent, useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Activity, CheckCircle2, FlaskConical, TerminalSquare } from "lucide-react";
import { useI18n } from "../../i18n/useI18n";
import { formatPercent, relativeTime, shortDateTime } from "../../shared/lib/format";
import { evolutionApi, type EvolutionTask, type GenomeRecord, type TraceMode } from "../../shared/services/evolution";
import { Button } from "../../shared/ui/Button";
import { Card, CardDescription, CardHeader, CardTitle } from "../../shared/ui/Card";
import { StatusBadge } from "../../shared/ui/StatusBadge";
import { cn } from "../../shared/lib/cn";

const intervalOptions = [
  ["1d", "1 天"],
  ["1h", "1 小時"],
  ["15m", "15 分鐘"],
  ["5m", "5 分鐘"],
  ["1m", "1 分鐘"]
];

const traceModeOptions: Array<[TraceMode, string, string]> = [
  ["off", "關閉", "不產生原始追蹤"],
  ["summary", "摘要", "只顯示任務與世代事件"],
  ["detailed", "詳細", "顯示個體、窗口、交叉與變異"],
  ["full", "逐筆", "顯示策略步驟，會拖慢優化"]
];

function roleLabel(t: (key: string) => string, role: GenomeRecord["role"]) {
  if (role === "champion") return "已採用參數";
  if (role === "archived" || role === "retired") return t("evolution.archived");
  return t("evolution.candidate");
}

function windowLabel(key: string) {
  const map: Record<string, string> = { "6m": "6 個月", "2y": "2 年", "5y": "5 年", "10y": "完整歷史" };
  return map[key] ?? key;
}

function monitorValue(value: string | number | undefined) {
  if (value === undefined || value === "") return "等待回報";
  return value;
}

function formatTraceValue(value: unknown): string {
  if (value === null || value === undefined) return "null";
  if (typeof value === "number") return Number.isInteger(value) ? value.toLocaleString("zh-TW") : value.toFixed(6);
  if (typeof value === "string" || typeof value === "boolean") return String(value);
  return JSON.stringify(value);
}

function JsonPreview({ value }: { value?: Record<string, unknown> | null }) {
  if (!value) return <div className="text-sm text-slate-500">等待參數產生</div>;
  return (
    <pre className="max-h-72 overflow-auto rounded-lg border border-white/[0.04] bg-slate-950/70 p-3 text-xs leading-relaxed text-slate-300">
      {JSON.stringify(value, null, 2)}
    </pre>
  );
}

function CurrentBestCard({ task }: { task: EvolutionTask }) {
  return (
    <Card>
      <CardHeader>
        <div>
          <CardTitle>本次任務目前最佳</CardTitle>
          <CardDescription>這是運算中的暫時第一名，任務完成後才會寫入候選參數庫。</CardDescription>
        </div>
      </CardHeader>
      <div className="space-y-4">
        <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-4">
          <div className="rounded-lg border border-white/[0.04] bg-white/[0.02] p-3">
            <div className="text-xs text-slate-500">評分</div>
            <div className="mt-1 font-mono text-lg text-slate-100">{(task.best_score ?? 0).toFixed(4)}</div>
          </div>
          <div className="rounded-lg border border-white/[0.04] bg-white/[0.02] p-3">
            <div className="text-xs text-slate-500">最大回撤</div>
            <div className="mt-1 font-mono text-lg text-[#fecaca]">{formatPercent(task.max_drawdown ?? 0)}</div>
          </div>
          {Object.entries(task.window_score ?? {}).map(([key, value]) => (
            <div key={key} className="rounded-lg border border-white/[0.04] bg-white/[0.02] p-3">
              <div className="text-xs text-slate-500">{windowLabel(key)}</div>
              <div className="mt-1 font-mono text-lg text-slate-100">{value.toFixed(4)}</div>
            </div>
          ))}
        </div>
        <div>
          <div className="mb-2 text-sm font-semibold text-slate-300">參數預覽</div>
          <JsonPreview value={task.best_param_pack} />
        </div>
      </div>
    </Card>
  );
}

function TraceConsole({ task }: { task: EvolutionTask }) {
  const [open, setOpen] = useState(true);
  const [mode, setMode] = useState<TraceMode>(task.trace_mode ?? "detailed");
  const queryClient = useQueryClient();
  const traceQuery = useQuery({
    queryKey: ["evolution-trace", task.id, open],
    queryFn: () => evolutionApi.trace(task.id, mode === "full" ? 1200 : 600),
    enabled: open,
    refetchInterval: open ? 1000 : false
  });
  const modeMutation = useMutation({
    mutationFn: (nextMode: TraceMode) => evolutionApi.setTraceMode(task.id, nextMode),
    onSuccess: (result) => {
      setMode(result.mode);
      queryClient.invalidateQueries({ queryKey: ["evolution-trace", task.id] });
    }
  });
  const events = traceQuery.data?.events ?? [];
  const visibleEvents = events.slice(-500);
  return (
    <Card>
      <CardHeader>
        <div>
          <CardTitle>原始追蹤</CardTitle>
          <CardDescription>泛用 trace viewer；逐筆模式會拖慢優化，只保留最近事件。</CardDescription>
        </div>
        <Button icon={TerminalSquare} variant="secondary" onClick={() => setOpen((value) => !value)}>
          {open ? "收合" : "展開"}
        </Button>
      </CardHeader>
      <div className="space-y-3">
        <div className="flex flex-wrap gap-2">
          {traceModeOptions.map(([value, label, description]) => (
            <button
              key={value}
              type="button"
              title={description}
              className={cn(
                "rounded-lg border px-3 py-2 text-sm transition",
                mode === value ? "border-[#2dd4bf]/40 bg-[#2dd4bf]/10 text-[#99f6e4]" : "border-white/[0.04] text-slate-400 hover:text-slate-200"
              )}
              onClick={() => modeMutation.mutate(value)}
            >
              {label}
            </button>
          ))}
        </div>
        {mode === "full" ? <div className="text-xs text-[#fde68a]">逐筆追蹤會產生大量事件，適合短任務或臨時觀察。</div> : null}
        {open ? (
          <div className="h-[28rem] overflow-auto rounded-lg border border-white/[0.06] bg-slate-950 p-3 font-mono text-xs leading-relaxed text-slate-300">
            {traceQuery.isLoading ? <div className="text-slate-500">等待追蹤資料...</div> : null}
            {!traceQuery.isLoading && visibleEvents.length === 0 ? <div className="text-slate-500">尚無追蹤事件，或目前追蹤模式為關閉。</div> : null}
            {visibleEvents.map((event) => (
              <div key={event.id} className="border-b border-white/[0.03] py-1">
                <span className="text-slate-500">#{event.id}</span>{" "}
                <span className="text-[#99f6e4]">{shortDateTime(event.time)}</span>{" "}
                <span className="text-[#fde68a]">{event.source}</span>{" "}
                <span className="text-[#c4b5fd]">{event.scope}</span>{" "}
                <span>{event.message}</span>
                {event.fields ? (
                  <div className="mt-1 break-words pl-4 text-slate-400">
                    {Object.entries(event.fields).map(([key, value]) => (
                      <span key={key} className="mr-3 inline-block">
                        <span className="text-slate-500">{key}=</span>{formatTraceValue(value)}
                      </span>
                    ))}
                  </div>
                ) : null}
              </div>
            ))}
          </div>
        ) : null}
      </div>
    </Card>
  );
}

function EvolutionPanel() {
  const { t } = useI18n();
  const queryClient = useQueryClient();
  const [expanded, setExpanded] = useState(false);
  const [interval, setInterval] = useState("1d");
  const [population, setPopulation] = useState(300);
  const [generations, setGenerations] = useState(25);
  const [mode, setMode] = useState<"inherit" | "random_once" | "manual">("inherit");
  const [traceMode, setTraceMode] = useState<TraceMode>("detailed");
  const [manualJson, setManualJson] = useState(
    '{\n  "policy": {\n    "initial_usdt": 1000,\n    "monthly_inject_usdt": 100,\n    "cold_sealed_btc": 0\n  },\n  "risk": {\n    "max_drawdown_pct": 0.88,\n    "fee_rate": 0.001,\n    "lot_step": 0.000001,\n    "lot_min": 0.00001\n  }\n}'
  );
  const { data: overview } = useQuery({
    queryKey: ["evolution-tasks"],
    queryFn: () => evolutionApi.listTasks(),
    refetchInterval: 2_000
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
        spawn_point,
        trace_mode: traceMode
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
    const progressPct = Math.min(100, Math.round((running.progress || 0) * 100));
    const evaluated = running.evaluated_individuals ?? current * (running.pop_size ?? 0);
    const planned = running.planned_evaluations ?? (running.pop_size ?? 0) * max;
    return (
      <div className="space-y-4">
        <Card>
          <CardHeader>
            <div>
              <CardTitle>{t("evolution.runningTask")}</CardTitle>
              <CardDescription>{relativeTime(running.created_at)}</CardDescription>
            </div>
            <StatusBadge status="running" />
          </CardHeader>
          <div className="space-y-4">
            <div className="rounded-lg border border-[#2dd4bf]/20 bg-[#2dd4bf]/[0.06] p-4">
              <div className="mb-3 flex items-center gap-2 text-sm font-semibold text-[#99f6e4]">
                <Activity className="h-4 w-4" />
                {t("evolution.monitor")}
              </div>
              <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-4">
                {[
                  [t("evolution.taskId"), `#${running.id}`],
                  [t("evolution.dataset"), `${running.pair ?? "BTCUSDT"} · ${running.interval ?? "1d"}`],
                  [t("evolution.population"), running.pop_size?.toLocaleString("zh-TW")],
                  [t("evolution.progress"), `${progressPct}%`],
                  [t("evolution.evaluated"), planned ? `${evaluated.toLocaleString("zh-TW")} / ${planned.toLocaleString("zh-TW")}` : evaluated.toLocaleString("zh-TW")],
                  [t("evolution.mutationProbability"), running.mutation_probability !== undefined ? formatPercent(running.mutation_probability) : undefined],
                  [t("evolution.mutationScale"), running.mutation_scale?.toFixed(2)],
                  [t("evolution.maxDrawdown"), running.max_drawdown !== undefined ? formatPercent(running.max_drawdown) : undefined],
                  [t("evolution.lastMonitorUpdate"), running.monitor_updated_at ? shortDateTime(running.monitor_updated_at) : undefined]
                ].map(([label, value]) => (
                  <div key={label} className="rounded-lg border border-white/[0.04] bg-slate-950/30 p-3">
                    <div className="text-xs text-slate-500">{label}</div>
                    <div className="mt-1 font-mono text-sm text-slate-100">{monitorValue(value)}</div>
                  </div>
                ))}
              </div>
            </div>
            <div>
              <div className="mb-2 flex justify-between text-sm">
                <span className="text-slate-400">{t("evolution.currentGeneration")}</span>
                <span className="font-mono text-slate-200">{current} / {max}</span>
              </div>
              <div className="h-2 overflow-hidden rounded-full bg-slate-800">
                <div className="h-full rounded-full bg-[#2dd4bf]" style={{ width: `${progressPct}%` }} />
              </div>
            </div>
          </div>
        </Card>
        <CurrentBestCard task={running} />
        <TraceConsole task={running} />
      </div>
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
            <div className="mb-2 text-sm text-slate-300">原始追蹤模式</div>
            <div className="grid gap-2 md:grid-cols-4">
              {traceModeOptions.map(([value, label, description]) => (
                <button
                  key={value}
                  type="button"
                  className={cn(
                    "rounded-lg border px-3 py-2 text-left text-sm transition",
                    traceMode === value ? "border-[#2dd4bf]/40 bg-[#2dd4bf]/10 text-[#99f6e4]" : "border-white/[0.04] text-slate-400"
                  )}
                  onClick={() => setTraceMode(value)}
                >
                  <span className="block font-semibold">{label}</span>
                  <span className="mt-1 block text-xs text-slate-500">{description}</span>
                </button>
              ))}
            </div>
          </div>
          {traceMode === "full" ? <div className="md:col-span-2 text-xs text-[#fde68a]">逐筆追蹤會拖慢優化，建議先用較小族群或較少代數觀察。</div> : null}
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
          <CardDescription>已建立的優化任務與完成狀態。</CardDescription>
        </div>
      </CardHeader>
      <div className="space-y-3">
        {isLoading ? <div className="text-sm text-slate-500">{t("common.loading")}</div> : null}
        {!isLoading && tasks.length === 0 ? <div className="text-sm text-slate-500">{t("evolution.noTasks")}</div> : null}
        {tasks.map((task) => (
          <div key={task.id} className="rounded-lg border border-white/[0.04] bg-white/[0.02] p-3">
            <div className="flex items-center justify-between gap-3">
              <div>
                <div className="font-mono text-sm text-slate-200">#{task.id}</div>
                <div className="mt-1 text-xs text-slate-500">{relativeTime(task.created_at)}</div>
              </div>
              <StatusBadge status={task.status} />
              <div className="text-right font-mono text-sm text-slate-300">{(task.best_score ?? 0).toFixed(3)}</div>
            </div>
            {task.error ? <div className="mt-2 text-xs text-[#fecaca]">{task.error}</div> : null}
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
          <CardTitle>已採用參數</CardTitle>
          <CardDescription>{champion ? relativeTime(champion.created_at) : t("common.unknown")}</CardDescription>
        </div>
        <CheckCircle2 className="h-5 w-5 text-[#2dd4bf]" />
      </CardHeader>
      {champion ? (
        <div className="space-y-4">
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
          <JsonPreview value={champion.param_pack} />
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
            <div className="space-y-4">
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
              <details className="rounded-lg border border-white/[0.04] bg-slate-950/40 p-3">
                <summary className="cursor-pointer text-sm font-semibold text-slate-300">參數 JSON</summary>
                <div className="mt-3">
                  <JsonPreview value={genome.param_pack} />
                </div>
              </details>
              <div className="flex flex-wrap gap-2">
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
          ["library", "候選參數庫"]
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
