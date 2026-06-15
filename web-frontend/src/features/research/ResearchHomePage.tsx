import { Link } from "react-router-dom";
import { AreaChart, Database, FlaskConical } from "lucide-react";
import { useI18n } from "../../i18n/useI18n";
import { Card, CardDescription, CardHeader, CardTitle } from "../../shared/ui/Card";

const items = [
  { to: "/evolution", icon: FlaskConical, titleKey: "research.evolutionTitle", bodyKey: "research.evolutionBody" },
  { to: "/backtesting", icon: AreaChart, titleKey: "research.backtestingTitle", bodyKey: "research.backtestingBody" },
  { to: "", icon: Database, titleKey: "research.dataTitle", bodyKey: "research.dataBody" }
];

export function ResearchHomePage() {
  const { t } = useI18n();

  return (
    <section className="space-y-4">
      <div>
        <h1 className="text-2xl font-bold text-slate-100">{t("research.title")}</h1>
        <p className="mt-1 text-sm text-slate-400">{t("research.subtitle")}</p>
      </div>
      <div className="grid gap-4 lg:grid-cols-3">
        {items.map((item) => {
          const Icon = item.icon;
          const content = (
            <Card className="h-full transition hover:border-white/10">
              <CardHeader>
                <div>
                  <CardTitle>{t(item.titleKey)}</CardTitle>
                  <CardDescription>{t(item.bodyKey)}</CardDescription>
                </div>
                <div className="flex h-10 w-10 items-center justify-center rounded-lg border border-[#2dd4bf]/20 bg-[#2dd4bf]/10">
                  <Icon className="h-5 w-5 text-[#99f6e4]" />
                </div>
              </CardHeader>
            </Card>
          );
          return item.to ? (
            <Link key={item.titleKey} to={item.to} className="block">
              {content}
            </Link>
          ) : (
            <div key={item.titleKey} className="opacity-70">
              {content}
            </div>
          );
        })}
      </div>
    </section>
  );
}
