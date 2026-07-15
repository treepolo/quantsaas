import {
  Activity,
  BarChart3,
  BrainCircuit,
  ClipboardList,
  Database,
  FlaskConical,
  Gauge,
  Layers3,
  Grid3X3,
  LucideIcon,
  Settings,
  Telescope,
  WandSparkles
} from "lucide-react";
import type { AppFeature } from "./features";

export type NavItem = {
  to: string;
  labelKey: string;
  icon: LucideIcon;
  placement: "main" | "footer";
  feature?: AppFeature;
  end?: boolean;
};

export const navItems: NavItem[] = [
  { to: "/", labelKey: "nav.research", icon: Telescope, placement: "main", feature: "dashboard", end: true },
  { to: "/data", labelKey: "nav.marketData", icon: Database, placement: "main", feature: "backtesting" },
  { to: "/generator", labelKey: "行情產生器", icon: WandSparkles, placement: "main", feature: "backtesting" },
  { to: "/datasets", labelKey: "研究資料集", icon: Layers3, placement: "main", feature: "backtesting" },
  { to: "/status", labelKey: "市場狀態", icon: Gauge, placement: "main", feature: "backtesting" },
  { to: "/evolution", labelKey: "nav.evolution", icon: FlaskConical, placement: "main", feature: "strategies" },
  { to: "/backtesting", labelKey: "nav.backtesting", icon: BarChart3, placement: "main", feature: "backtesting" },
  { to: "/robustness", labelKey: "參數穩健性", icon: Grid3X3, placement: "main", feature: "backtesting" },
  { to: "/dynamic-parameters", labelKey: "預測與動態參數", icon: BrainCircuit, placement: "main", feature: "backtesting" },
  { to: "/parameter-research", labelKey: "參數地形研究", icon: Layers3, placement: "main", feature: "backtesting" },
  { to: "/tasks", labelKey: "計算任務", icon: ClipboardList, placement: "main", feature: "backtesting" },
  { to: "/settings", labelKey: "nav.settings", icon: Settings, placement: "footer", feature: "settings" }
];

export const brandIcon = Activity;
