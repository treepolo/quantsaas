import { useAuth } from "../../app/AuthProvider";
import { useI18n } from "../../i18n/useI18n";
import { getEnabledFeatures } from "../../shared/config/features";
import { navItems } from "../../shared/config/navigation";
import { Button } from "../../shared/ui/Button";
import { Card, CardDescription, CardHeader, CardTitle } from "../../shared/ui/Card";
import { useSystemStatusStore } from "../../stores/systemStatusStore";
import { cn } from "../../shared/lib/cn";

export function SettingsPage() {
  const { t, locale, setLocale } = useI18n();
  const { user } = useAuth();
  const status = useSystemStatusStore((state) => state.status);
  const enabled = getEnabledFeatures();
  return (
    <section className="space-y-4">
      <div>
        <h1 className="text-2xl font-bold text-slate-100">{t("settings.title")}</h1>
        <p className="mt-1 text-sm text-slate-400">{t("settings.subtitle")}</p>
      </div>
      <div className="grid gap-4 lg:grid-cols-2">
        <Card>
          <CardHeader>
            <div>
              <CardTitle>{t("settings.account")}</CardTitle>
              <CardDescription>{user?.email ?? t("common.unknown")}</CardDescription>
            </div>
          </CardHeader>
          <div className="grid gap-3 md:grid-cols-2">
            <div className="rounded-lg border border-white/[0.04] bg-white/[0.02] p-4">
              <div className="text-sm text-slate-500">{t("settings.role")}</div>
              <div className="mt-2 font-mono text-slate-100">{user?.role ?? "user"}</div>
            </div>
            <div className="rounded-lg border border-white/[0.04] bg-white/[0.02] p-4">
              <div className="text-sm text-slate-500">應用模式</div>
              <div className="mt-2 font-mono text-slate-100">{status.app_role ?? "dev"}</div>
            </div>
          </div>
        </Card>
        <Card>
          <CardHeader>
            <div>
              <CardTitle>{t("settings.language")}</CardTitle>
              <CardDescription>{t("settings.subtitle")}</CardDescription>
            </div>
          </CardHeader>
          <div className="flex flex-wrap gap-2">
            {[
              ["zh", t("settings.traditionalChinese")],
              ["en", t("settings.english")]
            ].map(([value, label]) => (
              <Button
                key={value}
                variant={locale === value ? "primary" : "secondary"}
                onClick={() => setLocale(value as "zh" | "en")}
              >
                {label}
              </Button>
            ))}
          </div>
        </Card>
      </div>
      <Card>
        <CardHeader>
          <div>
            <CardTitle>{t("settings.featureFlags")}</CardTitle>
            <CardDescription>{t("settings.featureFlagsDescription")}</CardDescription>
          </div>
        </CardHeader>
        <div className="grid gap-3 md:grid-cols-3">
          {navItems
            .filter((item) => item.feature)
            .map((item) => {
              const active = enabled.includes(item.feature!);
              return (
                <div
                  key={item.to}
                  className={cn(
                    "cursor-default rounded-lg border p-4",
                    active ? "border-[#2dd4bf]/20 bg-[#2dd4bf]/10 text-[#99f6e4]" : "border-white/[0.04] bg-white/[0.02] text-slate-500"
                  )}
                >
                  <div className="flex items-center justify-between gap-3">
                    <div className="flex items-center gap-2">
                      <item.icon className="h-4 w-4" />
                      <span>{t(item.labelKey)}</span>
                    </div>
                    <span className={cn("rounded-full px-2 py-1 text-xs", active ? "bg-[#2dd4bf]/10 text-[#99f6e4]" : "bg-white/[0.03] text-slate-500")}>
                      {active ? t("settings.enabled") : t("settings.disabled")}
                    </span>
                  </div>
                  <div className="mt-2 text-xs text-slate-500">
                    {t("settings.readOnlyFeature")}
                  </div>
                </div>
              );
            })}
        </div>
      </Card>
    </section>
  );
}
