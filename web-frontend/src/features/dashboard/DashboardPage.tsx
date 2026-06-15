import { useEffect, useMemo, useState } from "react";
import { Link, useLocation, useNavigate, useSearchParams } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Area, AreaChart, CartesianGrid, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";
import { Pause, Play, Plus, Settings } from "lucide-react";
import { useI18n } from "../../i18n/useI18n";
import { formatAsset, formatMoney, relativeTime, shortDateTime } from "../../shared/lib/format";
import { dashboardApi } from "../../shared/services/dashboard";
import { instancesApi, type StrategyInstance } from "../../shared/services/instances";
import { mockEquity, mockInstances, mockPortfolio } from "../../shared/lib/mockData";
import { Button } from "../../shared/ui/Button";
import { Card, CardDescription, CardHeader, CardTitle } from "../../shared/ui/Card";
import { StatusBadge } from "../../shared/ui/StatusBadge";
import { PnLChartSkeleton } from "../../shared/ui/skeletons";
import { cn } from "../../shared/lib/cn";

function PageHeading() {
  const { t } = useI18n();
  return (
    <div className="mb-6 flex flex-col gap-3 lg:flex-row lg:items-end lg:justify-between">
      <div>
        <h1 className="text-2xl font-bold text-slate-100">{t("dashboard.title")}</h1>
        <p className="mt-1 text-sm text-slate-400">{t("dashboard.subtitle")}</p>
      </div>
      <Link to="/instances/new">
        <Button icon={Plus}>{t("dashboard.newInstance")}</Button>
      </Link>
    </div>
  );
}

function InstanceSelector({
  instances,
  selectedId,
  onSelect,
  onToggle,
  toggling
}: {
  instances: StrategyInstance[];
  selectedId?: number;
  onSelect: (id: number) => void;
  onToggle: (instance: StrategyInstance) => void;
  toggling: boolean;
}) {
  const { t } = useI18n();
  const [configOpen, setConfigOpen] = useState(false);

  return (
    <Card className="lg:col-span-1">
      <CardHeader>
        <div>
          <CardTitle>{t("dashboard.instances")}</CardTitle>
          <CardDescription>{t("common.mockHint")}</CardDescription>
        </div>
        <Button variant="ghost" className="h-9 min-h-9 px-2" icon={Settings} title={t("dashboard.goSettings")} onClick={() => setConfigOpen(true)} />
      </CardHeader>
      <div className="space-y-3">
        {instances.map((instance) => {
          const active = selectedId === instance.id;
          const running = String(instance.status).toLowerCase() === "running";
          return (
            <div
              key={instance.id}
              className={cn(
                "rounded-lg border bg-white/[0.02] p-3 transition",
                active ? "border-l-4 border-l-[#2dd4bf] border-white/10" : "border-white/[0.04] hover:border-white/10"
              )}
            >
              <button className="w-full text-left" onClick={() => onSelect(instance.id)}>
                <div className="flex items-start justify-between gap-3">
                  <div>
                    <div className="font-semibold text-slate-100">{instance.name}</div>
                    <div className="mt-1 font-mono text-xs text-slate-500">{instance.exchange} · {instance.symbol}</div>
                  </div>
                  <StatusBadge status={instance.status} />
                </div>
              </button>
              <div className="mt-3 flex items-center justify-between border-t border-white/[0.04] pt-3">
                <span className="text-xs text-slate-500">{relativeTime(instance.last_tick_at)}</span>
                <Button
                  variant={running ? "secondary" : "primary"}
                  className="h-8 min-h-8 px-3 text-xs"
                  icon={running ? Pause : Play}
                  loading={toggling}
                  onClick={() => onToggle(instance)}
                >
                  {running ? t("common.pause") : t("common.start")}
                </Button>
              </div>
            </div>
          );
        })}
      </div>
      <Link to="/instances/new" className="mt-4 block">
        <Button className="w-full" icon={Plus}>{t("dashboard.newInstance")}</Button>
      </Link>
      {configOpen ? (
        <div className="fixed inset-y-0 right-0 z-40 w-full max-w-md border-l border-white/10 bg-[#020617]/95 p-5 shadow-2xl backdrop-blur-xl">
          <div className="flex items-center justify-between">
            <h2 className="text-lg font-semibold text-slate-100">{t("dashboard.goSettings")}</h2>
            <Button variant="ghost" onClick={() => setConfigOpen(false)}>{t("common.cancel")}</Button>
          </div>
          <p className="mt-4 text-sm leading-6 text-slate-400">
            策略執行、API 狀態與語言偏好集中在設定頁管理。敏感金鑰只存在本機執行端，不會顯示在此介面。
          </p>
          <Link to="/settings" className="mt-6 block">
            <Button className="w-full" icon={Settings}>{t("common.settings")}</Button>
          </Link>
        </div>
      ) : null}
    </Card>
  );
}

function StrategyOverviewCard({ instance }: { instance?: StrategyInstance }) {
  const { t } = useI18n();
  const { data: portfolio } = useQuery({
    queryKey: ["portfolio", instance?.id],
    enabled: Boolean(instance),
    queryFn: () => dashboardApi.portfolio(instance!.id).catch(() => mockPortfolio(instance!.id)),
    refetchInterval: 60_000
  });
  const summary = portfolio ?? (instance ? mockPortfolio(instance.id) : undefined);
  const rows = summary
    ? [
        { label: t("dashboard.longTerm"), value: summary.long_term, asset: true, color: "bg-[#2dd4bf]" },
        { label: t("dashboard.activePosition"), value: summary.active_position, asset: true, color: "bg-[#0ea5e9]" },
        { label: t("dashboard.availableFunds"), value: summary.available_funds, asset: false, color: "bg-[#34d399]" },
        { label: t("dashboard.sealedAssets"), value: summary.sealed_assets, asset: true, color: "bg-[#ff8c6b]" }
      ]
    : [];
  const total = summary?.total_assets ?? 1;

  return (
    <Card>
      <CardHeader>
        <div>
          <CardTitle>{t("dashboard.overview")}</CardTitle>
          <CardDescription>{instance?.name ?? t("dashboard.noInstance")}</CardDescription>
        </div>
        {instance ? <StatusBadge status={instance.status} /> : null}
      </CardHeader>
      {summary ? (
        <div className="space-y-5">
          <div>
            <div className="text-sm text-slate-500">{t("dashboard.totalAssets")}</div>
            <div className="mt-1 font-mono text-3xl font-semibold text-slate-100">{formatMoney(summary.total_assets)}</div>
          </div>
          <div className="space-y-3">
            <div className="text-sm font-medium text-slate-300">{t("dashboard.assetMix")}</div>
            {rows.map((row) => (
              <div key={row.label}>
                <div className="mb-1 flex items-center justify-between gap-3 text-xs">
                  <span className="text-slate-400">{row.label}</span>
                  <span className="font-mono text-slate-200">{row.asset ? formatAsset(row.value / 62_000) : formatMoney(row.value)}</span>
                </div>
                <div className="h-1.5 overflow-hidden rounded-full bg-slate-800">
                  <div className={cn("h-full rounded-full", row.color)} style={{ width: `${Math.max(4, (row.value / total) * 100)}%` }} />
                </div>
              </div>
            ))}
          </div>
          <div className="grid grid-cols-2 gap-3 border-t border-white/[0.04] pt-4 text-sm">
            <div>
              <div className="text-slate-500">{t("dashboard.lastDecision")}</div>
              <div className="mt-1 font-mono text-slate-200">{shortDateTime(summary.last_decision_at)}</div>
            </div>
            <div>
              <div className="text-slate-500">{t("common.status")}</div>
              <div className="mt-1 text-slate-200">{instance ? <StatusBadge status={instance.status} /> : null}</div>
            </div>
          </div>
        </div>
      ) : (
        <p className="text-sm text-slate-500">{t("dashboard.noInstance")}</p>
      )}
    </Card>
  );
}

function PnLChart({ instanceId }: { instanceId?: number }) {
  const { t } = useI18n();
  const [range, setRange] = useState("30d");
  const days = range === "7d" ? 7 : range === "90d" ? 90 : 30;
  const { data, isLoading } = useQuery({
    queryKey: ["equity", instanceId, range],
    enabled: Boolean(instanceId),
    queryFn: () => dashboardApi.snapshots(instanceId!, range).catch(() => mockEquity(days)),
    refetchInterval: 60_000
  });
  const chartData = (data ?? mockEquity(days)).map((item) => ({
    ...item,
    label: new Intl.DateTimeFormat("zh-TW", { month: "2-digit", day: "2-digit" }).format(new Date(item.time))
  }));

  return (
    <Card>
      <CardHeader>
        <div>
          <CardTitle>{t("dashboard.equityCurve")}</CardTitle>
          <CardDescription>{instanceId ? `#${instanceId}` : t("dashboard.noInstance")}</CardDescription>
        </div>
        <div className="flex rounded-lg border border-white/[0.06] bg-white/[0.03] p-1">
          {[
            ["7d", t("common.days7")],
            ["30d", t("common.days30")],
            ["90d", t("common.days90")]
          ].map(([value, label]) => (
            <button
              key={value}
              className={cn(
                "rounded-md px-3 py-1.5 text-xs font-semibold transition",
                range === value ? "bg-[#2dd4bf]/10 text-[#2dd4bf]" : "text-slate-500 hover:text-slate-200"
              )}
              onClick={() => setRange(value)}
            >
              {label}
            </button>
          ))}
        </div>
      </CardHeader>
      {isLoading ? (
        <PnLChartSkeleton />
      ) : (
        <div className="h-72">
          <ResponsiveContainer width="100%" height="100%">
            <AreaChart data={chartData} margin={{ left: 0, right: 10, top: 10, bottom: 0 }}>
              <defs>
                <linearGradient id="equityFill" x1="0" x2="0" y1="0" y2="1">
                  <stop offset="5%" stopColor="#2dd4bf" stopOpacity={0.34} />
                  <stop offset="95%" stopColor="#2dd4bf" stopOpacity={0.02} />
                </linearGradient>
              </defs>
              <CartesianGrid stroke="rgba(148,163,184,0.08)" vertical={false} />
              <XAxis dataKey="label" stroke="#64748b" tickLine={false} axisLine={false} fontSize={12} />
              <YAxis stroke="#64748b" tickLine={false} axisLine={false} fontSize={12} tickFormatter={(value) => `${Math.round(value / 1000)}k`} />
              <Tooltip
                contentStyle={{ background: "#020617", border: "1px solid rgba(255,255,255,0.08)", borderRadius: 8 }}
                labelStyle={{ color: "#e2e8f0" }}
                formatter={(value: number) => [formatMoney(value), t("dashboard.totalAssets")]}
              />
              <Area type="monotone" dataKey="total_assets" stroke="#2dd4bf" strokeWidth={2} fill="url(#equityFill)" />
            </AreaChart>
          </ResponsiveContainer>
        </div>
      )}
    </Card>
  );
}

function StrategyJourneyCard({ instanceId }: { instanceId?: number }) {
  const { t } = useI18n();
  const summary = instanceId ? mockPortfolio(instanceId) : undefined;
  const items = summary
    ? [
        { label: t("dashboard.firstRun"), value: shortDateTime(summary.first_run_at) },
        { label: t("dashboard.decisionCount"), value: summary.decisions_count.toLocaleString("zh-TW") },
        { label: t("dashboard.monthlyTrades"), value: summary.monthly_trades.toLocaleString("zh-TW") }
      ]
    : [];
  return (
    <Card>
      <CardHeader>
        <div>
          <CardTitle>{t("dashboard.journey")}</CardTitle>
          <CardDescription>{t("dashboard.subtitle")}</CardDescription>
        </div>
      </CardHeader>
      <div className="grid gap-3 md:grid-cols-3">
        {items.map((item) => (
          <div key={item.label} className="rounded-lg border border-white/[0.04] bg-white/[0.02] p-4">
            <div className="text-sm text-slate-500">{item.label}</div>
            <div className="mt-2 font-mono text-lg font-semibold text-slate-100">{item.value}</div>
          </div>
        ))}
      </div>
    </Card>
  );
}

export function DashboardPage() {
  const { t } = useI18n();
  const [params, setParams] = useSearchParams();
  const location = useLocation();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const notice = (location.state as { notice?: string } | null)?.notice;
  const { data: instances = mockInstances } = useQuery({
    queryKey: ["instances"],
    queryFn: () => instancesApi.list().catch(() => mockInstances),
    refetchInterval: 60_000
  });
  const selectedParam = Number(params.get("instance"));
  const selected = useMemo(() => instances.find((item) => item.id === selectedParam) ?? instances[0], [instances, selectedParam]);

  useEffect(() => {
    if (selected && selected.id !== selectedParam) {
      setParams({ instance: String(selected.id) }, { replace: true });
    }
  }, [selected, selectedParam, setParams]);

  const toggleMutation = useMutation({
    mutationFn: async (instance: StrategyInstance) => {
      const running = String(instance.status).toLowerCase() === "running";
      return running ? instancesApi.stop(instance.id) : instancesApi.start(instance.id);
    },
    onSettled: () => queryClient.invalidateQueries({ queryKey: ["instances"] })
  });

  return (
    <section>
      <PageHeading />
      {notice ? (
        <div className="mb-4 rounded-lg border border-[#2dd4bf]/20 bg-[#2dd4bf]/10 px-4 py-3 text-sm text-[#99f6e4]">{t("instances.createdNotice")}</div>
      ) : null}
      <div className="qs-bento-grid">
        <InstanceSelector
          instances={instances}
          selectedId={selected?.id}
          onSelect={(id) => {
            setParams({ instance: String(id) });
            navigate(`/?instance=${id}`);
          }}
          onToggle={(instance) => toggleMutation.mutate(instance)}
          toggling={toggleMutation.isPending}
        />
        <div className="space-y-4 lg:col-span-3">
          <StrategyOverviewCard instance={selected} />
          <PnLChart instanceId={selected?.id} />
          <StrategyJourneyCard instanceId={selected?.id} />
        </div>
      </div>
    </section>
  );
}
