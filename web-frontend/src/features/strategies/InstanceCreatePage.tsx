import { FormEvent, useMemo, useState } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { ArrowLeft, ArrowRight, Check, Rocket } from "lucide-react";
import { useMutation } from "@tanstack/react-query";
import { useI18n } from "../../i18n/useI18n";
import { strategyCatalog } from "../../shared/config/strategyCatalog";
import { instancesApi } from "../../shared/services/instances";
import { Button } from "../../shared/ui/Button";
import { Card, CardDescription, CardHeader, CardTitle } from "../../shared/ui/Card";
import { cn } from "../../shared/lib/cn";

function NumberField({
  label,
  value,
  onChange,
  placeholder,
  required
}: {
  label: string;
  value: number | "";
  onChange: (value: number | "") => void;
  placeholder?: string;
  required?: boolean;
}) {
  return (
    <label className="block">
      <span className="mb-2 block text-sm font-medium text-slate-300">{label}</span>
      <input
        className="h-11 w-full rounded-lg border border-slate-700 bg-slate-900/80 px-3 font-mono text-sm text-slate-100 outline-none transition focus:border-[#2dd4bf]"
        type="number"
        min="0"
        step="0.000001"
        value={value}
        placeholder={placeholder}
        required={required}
        onChange={(event) => onChange(event.target.value === "" ? "" : Number(event.target.value))}
      />
    </label>
  );
}

export function InstanceCreatePage() {
  const { t } = useI18n();
  const [params] = useSearchParams();
  const navigate = useNavigate();
  const initialTemplate = params.get("template") ?? strategyCatalog[0]?.id;
  const [step, setStep] = useState(1);
  const [templateId, setTemplateId] = useState(initialTemplate);
  const [name, setName] = useState("");
  const [initialFunds, setInitialFunds] = useState<number | "">(10_000);
  const [monthlyFunds, setMonthlyFunds] = useState<number | "">("");
  const [sealedAmount, setSealedAmount] = useState<number | "">("");
  const [risk, setRisk] = useState(0.2);
  const selectedTemplate = useMemo(() => strategyCatalog.find((item) => item.id === templateId) ?? strategyCatalog[0], [templateId]);
  const createMutation = useMutation({
    mutationFn: () =>
      instancesApi
        .create({
          name: name || t("templates.dynamicName"),
          template_id: selectedTemplate.id,
          symbol: selectedTemplate.symbols[0],
          exchange: selectedTemplate.exchange.toLowerCase(),
          initial_usdt: Number(initialFunds || 0),
          monthly_inject_usdt: Number(monthlyFunds || 0),
          cold_sealed_btc: Number(sealedAmount || 0),
          risk_limit: risk
        })
        .catch(() => ({
          id: Math.floor(Date.now() / 1000),
          name,
          template_id: selectedTemplate.id,
          symbol: selectedTemplate.symbols[0],
          exchange: selectedTemplate.exchange,
          status: "STOPPED" as const,
          created_at: new Date().toISOString()
        })),
    onSuccess: (instance) => {
      navigate(`/?instance=${instance.id}`, { state: { notice: "created" } });
    }
  });

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    createMutation.mutate();
  }

  return (
    <section className="mx-auto max-w-5xl">
      <div className="mb-6">
        <h1 className="text-2xl font-bold text-slate-100">{t("instances.newTitle")}</h1>
        <p className="mt-1 text-sm text-slate-400">{t("templates.subtitle")}</p>
      </div>
      <div className="mb-6 grid grid-cols-2 gap-3 text-sm">
        {[t("instances.stepTemplate"), t("instances.stepConfig")].map((label, index) => {
          const active = step === index + 1;
          return (
            <div
              key={label}
              className={cn(
                "rounded-lg border px-4 py-3",
                active ? "border-[#2dd4bf]/30 bg-[#2dd4bf]/10 text-[#99f6e4]" : "border-white/[0.04] bg-white/[0.02] text-slate-500"
              )}
            >
              {index + 1}. {label}
            </div>
          );
        })}
      </div>
      {step === 1 ? (
        <div className="grid gap-4 md:grid-cols-2">
          {strategyCatalog.map((item) => (
            <button
              key={item.id}
              className={cn(
                "rounded-lg border bg-slate-900/20 p-4 text-left backdrop-blur transition",
                templateId === item.id ? "border-[#2dd4bf]/40 shadow-[0_0_40px_rgb(45_212_191/0.08)]" : "border-white/[0.04] hover:border-white/10"
              )}
              onClick={() => setTemplateId(item.id)}
            >
              <div className="mb-3 flex items-center justify-between">
                <h2 className="text-lg font-semibold text-slate-100">{t(item.nameKey)}</h2>
                {templateId === item.id ? <Check className="h-5 w-5 text-[#2dd4bf]" /> : null}
              </div>
              <p className="text-sm leading-6 text-slate-400">{t(item.descriptionKey)}</p>
              <div className="mt-4 font-mono text-xs text-slate-500">{item.exchange} · {item.symbols.join(", ")}</div>
            </button>
          ))}
          <div className="md:col-span-2 flex justify-end">
            <Button icon={ArrowRight} onClick={() => setStep(2)}>{t("instances.next")}</Button>
          </div>
        </div>
      ) : (
        <Card>
          <CardHeader>
            <div>
              <CardTitle>{t(selectedTemplate.nameKey)}</CardTitle>
              <CardDescription>{t(selectedTemplate.descriptionKey)}</CardDescription>
            </div>
          </CardHeader>
          <form className="grid gap-4 md:grid-cols-2" onSubmit={submit}>
            <label className="block md:col-span-2">
              <span className="mb-2 block text-sm font-medium text-slate-300">{t("instances.name")}</span>
              <input
                className="h-11 w-full rounded-lg border border-slate-700 bg-slate-900/80 px-3 text-sm text-slate-100 outline-none transition focus:border-[#2dd4bf]"
                value={name}
                required
                onChange={(event) => setName(event.target.value)}
              />
            </label>
            <NumberField label={t("instances.initialFunds")} value={initialFunds} onChange={setInitialFunds} required />
            <NumberField label={t("instances.monthlyFunds")} value={monthlyFunds} onChange={setMonthlyFunds} />
            <NumberField label={t("instances.sealedAmount")} value={sealedAmount} onChange={setSealedAmount} />
            <label className="block">
              <span className="mb-2 block text-sm font-medium text-slate-300">{t("instances.riskPreference")}</span>
              <div className="rounded-lg border border-slate-700 bg-slate-900/80 px-3 py-3">
                <input
                  className="w-full accent-[#2dd4bf]"
                  type="range"
                  min="0.05"
                  max="0.45"
                  step="0.01"
                  value={risk}
                  onChange={(event) => setRisk(Number(event.target.value))}
                />
                <div className="mt-2 font-mono text-sm text-slate-200">{Math.round(risk * 100)}%</div>
              </div>
            </label>
            <div className="flex justify-between md:col-span-2">
              <Button variant="secondary" icon={ArrowLeft} type="button" onClick={() => setStep(1)}>
                {t("instances.back")}
              </Button>
              <Button icon={Rocket} loading={createMutation.isPending} type="submit">
                {t("instances.submit")}
              </Button>
            </div>
          </form>
        </Card>
      )}
    </section>
  );
}
