import { BrowserRouter, Navigate, Outlet, Route, Routes, useLocation } from "react-router-dom";
import { AppShell } from "./AppShell";
import { AuthProvider, useAuth } from "./AuthProvider";
import { AuthScaffold } from "./AuthScaffold";
import { hasFeature, type AppFeature } from "../shared/config/features";
import { LoginPage } from "../features/auth/LoginPage";
import { RegisterPage } from "../features/auth/RegisterPage";
import { DashboardPage } from "../features/dashboard/DashboardPage";
import { TemplatesPage } from "../features/strategies/TemplatesPage";
import { InstanceListPage } from "../features/strategies/InstanceListPage";
import { InstanceCreatePage } from "../features/strategies/InstanceCreatePage";
import { EvolutionPage } from "../features/strategies/EvolutionPage";
import { AgentsPage } from "../features/agents/AgentsPage";
import { BacktestingPage } from "../features/backtesting/BacktestingPage";
import { SettingsPage } from "../features/settings/SettingsPage";

function AuthGate() {
  const { user, loading } = useAuth();
  const location = useLocation();
  if (loading) {
    return <div className="flex min-h-screen items-center justify-center bg-[#020617] text-slate-300">載入中...</div>;
  }
  if (!user) {
    return <Navigate to="/login" replace state={{ from: location }} />;
  }
  return <Outlet />;
}

function FeatureGate({ feature }: { feature: AppFeature }) {
  return hasFeature(feature) ? <Outlet /> : <Navigate to="/" replace />;
}

export function AppRouter() {
  return (
    <BrowserRouter>
      <AuthProvider>
        <Routes>
          <Route
            path="/login"
            element={
              <AuthScaffold>
                <LoginPage />
              </AuthScaffold>
            }
          />
          <Route
            path="/register"
            element={
              <AuthScaffold>
                <RegisterPage />
              </AuthScaffold>
            }
          />
          <Route element={<AuthGate />}>
            <Route path="/" element={<AppShell />}>
              <Route index element={<DashboardPage />} />
              <Route element={<FeatureGate feature="strategies" />}>
                <Route path="templates" element={<TemplatesPage />} />
                <Route path="instances" element={<InstanceListPage />} />
                <Route path="instances/new" element={<InstanceCreatePage />} />
                <Route path="evolution" element={<EvolutionPage />} />
              </Route>
              <Route element={<FeatureGate feature="agents" />}>
                <Route path="agents" element={<AgentsPage />} />
              </Route>
              <Route element={<FeatureGate feature="backtesting" />}>
                <Route path="backtesting" element={<BacktestingPage />} />
              </Route>
              <Route element={<FeatureGate feature="settings" />}>
                <Route path="settings" element={<SettingsPage />} />
              </Route>
            </Route>
          </Route>
          <Route path="*" element={<Navigate to="/" replace />} />
        </Routes>
      </AuthProvider>
    </BrowserRouter>
  );
}
