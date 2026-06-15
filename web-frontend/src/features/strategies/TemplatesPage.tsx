import { Link } from "react-router-dom";
import { ArrowRight, CheckCircle2 } from "lucide-react";
import { useI18n } from "../../i18n/useI18n";
import { strategyCatalog } from "../../shared/config/strategyCatalog";
import { Button } from "../../shared/ui/Button";
import { Card, CardDescription, CardHeader, CardTitle } from "../../shared/ui/Card";

export function TemplatesPage() {
  const { t } = useI18n();
  return (
    <section>
      <div className="mb-6">
        <h1 className="text-2xl font-bold text-slate-100">{t("templates.title")}</h1>
        <p className="mt-1 text-sm text-slate-400">{t("templates.subtitle")}</p>
      </div>
      <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
        {strategyCatalog.map((item) => (
          <Card key={item.id} className="relative overflow-hidden">
            <div className="absolute inset-x-0 top-0 h-1" style={{ backgroundColor: item.color }} />
            <CardHeader>
              <div>
                <CardTitle>{t(item.nameKey)}</CardTitle>
                <CardDescription>{t(item.descriptionKey)}</CardDescription>
              </div>
            </CardHeader>
            <div className="space-y-4">
              <div className="rounded-lg border border-white/[0.04] bg-white/[0.02] p-3 text-sm">
                <div className="text-slate-500">{t("instances.pair")}</div>
                <div className="mt-1 font-mono text-slate-200">{item.exchange} · {item.symbols.join(", ")}</div>
              </div>
              {item.supportsOptimization ? (
                <div className="inline-flex items-center gap-2 rounded-full border border-[#2dd4bf]/20 bg-[#2dd4bf]/10 px-3 py-1 text-xs font-medium text-[#99f6e4]">
                  <CheckCircle2 className="h-3.5 w-3.5" />
                  {t("templates.supportsOptimization")}
                </div>
              ) : null}
              <Link to={`/instances/new?template=${item.id}`} className="block">
                <Button className="w-full" icon={ArrowRight}>{t("templates.createInstance")}</Button>
              </Link>
            </div>
          </Card>
        ))}
      </div>
    </section>
  );
}
