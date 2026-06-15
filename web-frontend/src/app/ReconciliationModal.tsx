import { AlertTriangle } from "lucide-react";
import { Button } from "../shared/ui/Button";
import { Card } from "../shared/ui/Card";
import { useI18n } from "../i18n/useI18n";

export function ReconciliationModal({ onClose }: { onClose: () => void }) {
  const { t } = useI18n();
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 px-4 backdrop-blur-sm">
      <Card className="max-w-md border-[#fbbf24]/20 bg-slate-950/80">
        <div className="mb-4 flex items-center gap-3">
          <div className="rounded-lg border border-[#fbbf24]/30 bg-[#fbbf24]/10 p-2 text-[#fbbf24]">
            <AlertTriangle className="h-5 w-5" />
          </div>
          <h2 className="text-lg font-semibold text-slate-100">{t("topbar.reconcileTitle")}</h2>
        </div>
        <p className="text-sm leading-6 text-slate-300">{t("topbar.reconcileBody")}</p>
        <div className="mt-6 flex justify-end">
          <Button onClick={onClose}>{t("common.confirm")}</Button>
        </div>
      </Card>
    </div>
  );
}
