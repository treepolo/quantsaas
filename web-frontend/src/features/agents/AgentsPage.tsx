import { useQuery } from "@tanstack/react-query";
import { Download, FileText, SquareTerminal } from "lucide-react";
import { useI18n } from "../../i18n/useI18n";
import { shortDateTime } from "../../shared/lib/format";
import { systemApi } from "../../shared/services/system";
import { Button } from "../../shared/ui/Button";
import { Card, CardDescription, CardHeader, CardTitle } from "../../shared/ui/Card";
import { defaultSystemStatus, useSystemStatusStore } from "../../stores/systemStatusStore";
import { cn } from "../../shared/lib/cn";

export function AgentsPage() {
  const { t } = useI18n();
  const setStatus = useSystemStatusStore((state) => state.setStatus);
  const status = useSystemStatusStore((state) => state.status);
  useQuery({
    queryKey: ["system-status"],
    queryFn: async () => {
      const result = await systemApi.status().catch(() => defaultSystemStatus);
      setStatus(result);
      return result;
    },
    refetchInterval: 30_000
  });

  const guide = [
    { icon: Download, title: t("agents.step1"), body: "取得與 SaaS 版本相容的本機執行端檔案。" },
    { icon: FileText, title: t("agents.step2"), body: t("agents.localOnly") },
    { icon: SquareTerminal, title: t("agents.step3"), body: "啟動後回到本頁確認狀態燈轉為在線。" }
  ];

  return (
    <section className="space-y-4">
      <div>
        <h1 className="text-2xl font-bold text-slate-100">{t("agents.title")}</h1>
        <p className="mt-1 text-sm text-slate-400">{t("agents.subtitle")}</p>
      </div>
      <Card>
        <div className="flex flex-col gap-6 md:flex-row md:items-center md:justify-between">
          <div className="flex items-center gap-4">
            <span
              className={cn(
                "h-5 w-5 rounded-full",
                status.api_connected ? "bg-[#2dd4bf] shadow-[0_0_25px_rgb(45_212_191/0.7)]" : "bg-slate-500"
              )}
            />
            <div>
              <h2 className="text-xl font-semibold text-slate-100">
                {status.api_connected ? t("agents.connected") : t("agents.disconnected")}
              </h2>
              <p className="mt-1 text-sm text-slate-500">
                {t("agents.lastHeartbeat")}：
                <span className="font-mono text-slate-300">{shortDateTime(status.last_heartbeat_at)}</span>
              </p>
            </div>
          </div>
          <div className="rounded-lg border border-white/[0.04] bg-white/[0.02] p-4">
            <div className="text-sm text-slate-500">{t("agents.version")}</div>
            <div className="mt-1 font-mono text-lg text-slate-100">{status.agent_version ?? "local-dev"}</div>
          </div>
        </div>
      </Card>
      <Card>
        <CardHeader>
          <div>
            <CardTitle>{t("agents.guide")}</CardTitle>
            <CardDescription>{t("agents.localOnly")}</CardDescription>
          </div>
        </CardHeader>
        <div className="grid gap-4 md:grid-cols-3">
          {guide.map((step, index) => (
            <div key={step.title} className="rounded-lg border border-white/[0.04] bg-white/[0.02] p-4">
              <div className="mb-4 flex items-center gap-3">
                <div className="flex h-9 w-9 items-center justify-center rounded-lg border border-[#2dd4bf]/20 bg-[#2dd4bf]/10 text-[#2dd4bf]">
                  <step.icon className="h-4 w-4" />
                </div>
                <span className="font-mono text-xs text-slate-500">Step {index + 1}</span>
              </div>
              <h3 className="font-semibold text-slate-100">{step.title}</h3>
              <p className="mt-2 text-sm leading-6 text-slate-400">{step.body}</p>
            </div>
          ))}
        </div>
      </Card>
      <Card>
        <CardHeader>
          <div>
            <CardTitle>{t("agents.apiCheck")}</CardTitle>
            <CardDescription>{t("agents.localOnly")}</CardDescription>
          </div>
          <Button variant={status.api_configured ? "secondary" : "primary"}>
            {status.api_configured ? t("common.configured") : t("common.notConfigured")}
          </Button>
        </CardHeader>
      </Card>
    </section>
  );
}
