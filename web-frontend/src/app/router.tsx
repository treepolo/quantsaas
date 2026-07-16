import { BrowserRouter, Navigate, Outlet, Route, Routes, useLocation } from "react-router-dom";
import { AppShell } from "./AppShell";
import { AuthProvider, useAuth } from "./AuthProvider";
import { AuthScaffold } from "./AuthScaffold";
import { hasFeature, type AppFeature } from "../shared/config/features";
import { LoginPage } from "../features/auth/LoginPage";
import { RegisterPage } from "../features/auth/RegisterPage";
import { ResearchHomePage } from "../features/research/ResearchHomePage";
import { MarketStatusPage } from "../features/research/MarketStatusPage";
import { EvolutionPage } from "../features/strategies/EvolutionPage";
import { BacktestingPage } from "../features/backtesting/BacktestingPage";
import { MarketDataPage } from "../features/marketdata/MarketDataPage";
import { MarketDataGeneratorPage } from "../features/marketdata/MarketDataGeneratorPage";
import { ResearchDatasetPage } from "../features/researchdata/ResearchDatasetPage";
import { SettingsPage } from "../features/settings/SettingsPage";
import { ComputeTasksPage } from "../features/computetasks/ComputeTasksPage";
import { RobustnessPage } from "../features/robustness/RobustnessPage";
import { DynamicParametersPage } from "../features/dynamicparameters/DynamicParametersPage";
import { ParameterResearchPage } from "../features/parameterresearch/ParameterResearchPage";
import { ControlResearchPage } from "../features/controlresearch/ControlResearchPage";
import { KlineInversePage } from "../features/klineinverse/KlineInversePage";

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
              <Route index element={<ResearchHomePage />} />
              <Route element={<FeatureGate feature="strategies" />}>
                <Route path="evolution" element={<EvolutionPage />} />
              </Route>
              <Route element={<FeatureGate feature="backtesting" />}>
                <Route path="data" element={<MarketDataPage />} />
                <Route path="generator" element={<MarketDataGeneratorPage />} />
                <Route path="datasets" element={<ResearchDatasetPage />} />
                <Route path="status" element={<MarketStatusPage />} />
                <Route path="backtesting" element={<BacktestingPage />} />
				<Route path="control-analysis" element={<ControlResearchPage />} />
				<Route path="kline-inverse" element={<KlineInversePage />} />
                <Route path="robustness" element={<RobustnessPage />} />
                <Route path="dynamic-parameters" element={<DynamicParametersPage />} />
                <Route path="parameter-research" element={<ParameterResearchPage />} />
                <Route path="tasks" element={<ComputeTasksPage />} />
              </Route>
              <Route path="templates" element={<Navigate to="/" replace />} />
              <Route path="instances" element={<Navigate to="/" replace />} />
              <Route path="instances/new" element={<Navigate to="/" replace />} />
              <Route path="agents" element={<Navigate to="/" replace />} />
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
