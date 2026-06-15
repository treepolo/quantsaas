import { useEffect } from "react";
import { NavLink, Outlet, useNavigate } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { ChevronDown, LogOut } from "lucide-react";
import { AppBackground } from "./AppBackground";
import { useAuth } from "./AuthProvider";
import { useI18n } from "../i18n/useI18n";
import { hasFeature } from "../shared/config/features";
import { brandIcon as BrandIcon, navItems } from "../shared/config/navigation";
import { cn } from "../shared/lib/cn";
import { systemApi } from "../shared/services/system";
import { Button } from "../shared/ui/Button";
import { defaultSystemStatus, useSystemStatusStore } from "../stores/systemStatusStore";

function Sidebar() {
  const { t } = useI18n();
  const visibleItems = navItems.filter((item) => !item.feature || hasFeature(item.feature));
  const mainItems = visibleItems.filter((item) => item.placement === "main");
  const footerItems = visibleItems.filter((item) => item.placement === "footer");

  const renderItem = (item: (typeof navItems)[number]) => (
    <NavLink
      key={item.to}
      to={item.to}
      end={item.end}
      className={({ isActive }) =>
        cn(
          "group flex h-11 items-center gap-3 rounded-lg border px-3 text-sm font-medium transition duration-150",
          isActive
            ? "border-[#2dd4bf]/10 bg-[#2dd4bf]/[0.06] text-[#2dd4bf] shadow-[inset_0_0_20px_rgb(45_212_191/0.06)]"
            : "border-transparent text-slate-500 hover:bg-white/[0.04] hover:text-slate-200"
        )
      }
      title={t(item.labelKey)}
    >
      <item.icon className="h-5 w-5 shrink-0" />
      <span className="hidden truncate lg:inline">{t(item.labelKey)}</span>
    </NavLink>
  );

  return (
    <aside className="fixed inset-y-0 left-0 z-30 flex h-screen w-16 flex-col border-r-2 border-[#0a0f1c] bg-[#020617]/40 px-2 backdrop-blur-xl lg:w-64 lg:px-3">
      <div className="flex h-16 items-center justify-center gap-3 lg:justify-start">
        <div className="flex h-10 w-10 items-center justify-center rounded-lg border border-[#ff8c6b]/25 bg-[#ff8c6b]/10 shadow-[0_0_30px_rgb(255_140_107/0.25)]">
          <BrandIcon className="h-5 w-5 text-[#ff8c6b]" />
        </div>
        <div className="hidden lg:block">
          <div className="text-sm font-bold uppercase tracking-wider text-slate-100">{t("app.name")}</div>
          <div className="text-xs text-slate-500">{t("app.tagline")}</div>
        </div>
      </div>
      <nav className="mt-3 flex flex-1 flex-col gap-1">{mainItems.map(renderItem)}</nav>
      <div className="mb-3 space-y-1">{footerItems.map(renderItem)}</div>
    </aside>
  );
}

function Topbar() {
  const { t } = useI18n();
  const { user, logout } = useAuth();
  const navigate = useNavigate();

  return (
    <header className="sticky top-0 z-20 flex h-16 items-center justify-end gap-3 border-b border-white/[0.04] bg-[#020617]/50 px-4 backdrop-blur-xl lg:px-6">
      <div className="flex items-center gap-2 rounded-lg border border-white/5 bg-white/[0.03] px-2 py-1.5">
        <button
          className="flex max-w-[180px] items-center gap-2 truncate px-1 text-sm text-slate-300"
          onClick={() => navigate("/settings")}
        >
          <span className="truncate">{user?.email ?? t("common.unknown")}</span>
          <ChevronDown className="h-4 w-4 text-slate-500" />
        </button>
        <Button variant="ghost" className="h-8 min-h-8 px-2" icon={LogOut} onClick={logout} title={t("common.logout")} />
      </div>
    </header>
  );
}

export function AppShell() {
  const setStatus = useSystemStatusStore((state) => state.setStatus);

  const { data } = useQuery({
    queryKey: ["system-status"],
    queryFn: () => systemApi.status().catch(() => defaultSystemStatus),
    refetchInterval: 30_000
  });

  useEffect(() => {
    if (data) setStatus(data);
  }, [data, setStatus]);

  return (
    <div className="relative h-screen overflow-hidden text-slate-200">
      <AppBackground />
      <Sidebar />
      <div className="flex h-screen min-h-0 flex-col pl-16 lg:pl-64">
        <Topbar />
        <main className="custom-scrollbar min-h-0 flex-1 overflow-y-auto p-4 lg:p-6">
          <div className="mx-auto max-w-[1800px]">
            <Outlet />
          </div>
        </main>
      </div>
    </div>
  );
}
