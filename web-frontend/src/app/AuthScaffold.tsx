import type { ReactNode } from "react";
import { Activity } from "lucide-react";
import { AppBackground } from "./AppBackground";
import { useI18n } from "../i18n/useI18n";

export function AuthScaffold({ children }: { children: ReactNode }) {
  const { t } = useI18n();
  return (
    <main className="relative flex min-h-screen items-center justify-center overflow-hidden px-4 py-8">
      <AppBackground />
      <section className="w-full max-w-[400px] rounded-lg border border-white/10 bg-slate-900/60 p-6 shadow-2xl shadow-black/30 backdrop-blur-xl">
        <div className="mb-8 text-center">
          <div className="mx-auto mb-4 flex h-12 w-12 items-center justify-center rounded-lg border border-[#ff8c6b]/30 bg-[#ff8c6b]/10 shadow-[0_0_35px_rgb(255_140_107/0.25)]">
            <Activity className="h-6 w-6 text-[#ff8c6b]" />
          </div>
          <h1 className="text-2xl font-bold text-slate-100">{t("app.name")}</h1>
          <p className="mt-2 text-sm text-slate-400">{t("app.slogan")}</p>
        </div>
        {children}
      </section>
    </main>
  );
}
