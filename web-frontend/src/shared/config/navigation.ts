import {
  Activity,
  BarChart3,
  BrainCircuit,
  ClipboardList,
  Database,
	Dices,
	LineChart,
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
  group?: NavGroup;
  feature?: AppFeature;
  end?: boolean;
};

export type NavGroup = "dataPreparation" | "researchExecution" | "analysis" | "management";

export const navGroups: Array<{ id: NavGroup; labelKey: string }> = [
  { id: "dataPreparation", labelKey: "navGroup.dataPreparation" },
  { id: "researchExecution", labelKey: "navGroup.researchExecution" },
  { id: "analysis", labelKey: "navGroup.analysis" },
  { id: "management", labelKey: "navGroup.management" }
];

export const navItems: NavItem[] = [
  { to: "/", labelKey: "nav.research", icon: Telescope, feature: "dashboard", end: true },
  { to: "/data", labelKey: "nav.marketData", icon: Database, group: "dataPreparation", feature: "backtesting" },
  { to: "/generator", labelKey: "行情產生器", icon: WandSparkles, group: "dataPreparation", feature: "backtesting" },
  { to: "/datasets", labelKey: "研究資料集", icon: Layers3, group: "dataPreparation", feature: "backtesting" },
  { to: "/evolution", labelKey: "參數實驗室", icon: Layers3, group: "researchExecution", feature: "backtesting" },
  { to: "/parameter-research", labelKey: "優化實驗室", icon: Grid3X3, group: "researchExecution", feature: "backtesting" },
  { to: "/backtesting", labelKey: "nav.backtesting", icon: BarChart3, group: "researchExecution", feature: "backtesting" },
  { to: "/dynamic-parameters", labelKey: "預測與動態參數", icon: BrainCircuit, group: "researchExecution", feature: "backtesting" },
  { to: "/trend-geometry", labelKey: "走勢幾何預測", icon: BrainCircuit, group: "researchExecution", feature: "backtesting" },
  { to: "/status", labelKey: "市場狀態", icon: Gauge, group: "analysis", feature: "backtesting" },
  { to: "/robustness", labelKey: "參數穩健性", icon: Grid3X3, group: "analysis", feature: "backtesting" },
  { to: "/control-analysis", labelKey: "隨機對照研究", icon: Dices, group: "analysis", feature: "backtesting" },
  { to: "/kline-inverse", labelKey: "K 線樣貌反推", icon: LineChart, group: "analysis", feature: "backtesting" },
  { to: "/tasks", labelKey: "計算任務", icon: ClipboardList, group: "management", feature: "backtesting" },
  { to: "/settings", labelKey: "nav.settings", icon: Settings, group: "management", feature: "settings" }
];

export const brandIcon = Activity;
