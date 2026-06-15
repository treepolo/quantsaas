import { useState } from "react";
import { Link } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { BarChart3, FlaskConical, Plus, Trash2 } from "lucide-react";
import { useI18n } from "../../i18n/useI18n";
import { formatMoney, relativeTime } from "../../shared/lib/format";
import { mockInstances, mockPortfolio } from "../../shared/lib/mockData";
import { instancesApi } from "../../shared/services/instances";
import { Button } from "../../shared/ui/Button";
import { Card } from "../../shared/ui/Card";
import { StatusBadge } from "../../shared/ui/StatusBadge";
import { TableSkeleton } from "../../shared/ui/skeletons";

export function InstanceListPage() {
  const { t } = useI18n();
  const queryClient = useQueryClient();
  const [confirming, setConfirming] = useState<number | null>(null);
  const { data: instances = mockInstances, isLoading } = useQuery({
    queryKey: ["instances"],
    queryFn: () => instancesApi.list().catch(() => mockInstances),
    refetchInterval: 60_000
  });
  const deleteMutation = useMutation({
    mutationFn: (id: number) => instancesApi.remove(id),
    onSettled: () => {
      setConfirming(null);
      queryClient.invalidateQueries({ queryKey: ["instances"] });
    }
  });

  return (
    <section>
      <div className="mb-6 flex flex-col gap-3 lg:flex-row lg:items-end lg:justify-between">
        <div>
          <h1 className="text-2xl font-bold text-slate-100">{t("instances.title")}</h1>
          <p className="mt-1 text-sm text-slate-400">{t("instances.subtitle")}</p>
        </div>
        <Link to="/instances/new">
          <Button icon={Plus}>{t("instances.create")}</Button>
        </Link>
      </div>
      <Card className="overflow-hidden p-0">
        {isLoading ? (
          <div className="p-4"><TableSkeleton /></div>
        ) : (
          <div className="custom-scrollbar overflow-x-auto">
            <table className="w-full min-w-[860px] text-left text-sm">
              <thead className="border-b border-white/[0.04] text-xs uppercase tracking-wider text-slate-500">
                <tr>
                  <th className="px-4 py-3">{t("instances.name")}</th>
                  <th className="px-4 py-3">{t("instances.strategy")}</th>
                  <th className="px-4 py-3">{t("instances.pair")}</th>
                  <th className="px-4 py-3">{t("common.status")}</th>
                  <th className="px-4 py-3">{t("instances.totalAssets")}</th>
                  <th className="px-4 py-3">{t("instances.createdAt")}</th>
                  <th className="px-4 py-3 text-right">{t("instances.actions")}</th>
                </tr>
              </thead>
              <tbody>
                {instances.map((instance) => (
                  <tr key={instance.id} className="border-b border-white/[0.03] last:border-0">
                    <td className="px-4 py-4 font-medium text-slate-100">{instance.name}</td>
                    <td className="px-4 py-4 text-slate-300">{t("templates.dynamicName")}</td>
                    <td className="px-4 py-4 font-mono text-slate-400">{instance.symbol}</td>
                    <td className="px-4 py-4"><StatusBadge status={instance.status} /></td>
                    <td className="px-4 py-4 font-mono text-slate-200">{formatMoney(instance.total_assets ?? mockPortfolio(instance.id).total_assets)}</td>
                    <td className="px-4 py-4 text-slate-400">{relativeTime(instance.created_at)}</td>
                    <td className="px-4 py-4">
                      <div className="flex justify-end gap-2">
                        <Link to={`/?instance=${instance.id}`}>
                          <Button variant="secondary" className="h-9 min-h-9 px-3" icon={BarChart3}>{t("instances.dashboard")}</Button>
                        </Link>
                        <Link to={`/evolution?instance=${instance.id}`}>
                          <Button variant="secondary" className="h-9 min-h-9 px-3" icon={FlaskConical}>{t("instances.optimize")}</Button>
                        </Link>
                        {confirming === instance.id ? (
                          <Button
                            variant="danger"
                            className="h-9 min-h-9 px-3"
                            loading={deleteMutation.isPending}
                            onClick={() => deleteMutation.mutate(instance.id)}
                          >
                            {t("instances.confirmDelete")}
                          </Button>
                        ) : (
                          <Button variant="ghost" className="h-9 min-h-9 px-3" icon={Trash2} onClick={() => setConfirming(instance.id)}>
                            {t("common.delete")}
                          </Button>
                        )}
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Card>
    </section>
  );
}
