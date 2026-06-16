import {
  Activity,
  BarChart3,
  Database,
  FlaskConical,
  Gauge,
  LucideIcon,
  Settings,
  Telescope
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
  { to: "/status", labelKey: "市場狀態", icon: Gauge, placement: "main", feature: "backtesting" },
  { to: "/evolution", labelKey: "nav.evolution", icon: FlaskConical, placement: "main", feature: "strategies" },
  { to: "/backtesting", labelKey: "nav.backtesting", icon: BarChart3, placement: "main", feature: "backtesting" },
  { to: "/settings", labelKey: "nav.settings", icon: Settings, placement: "footer", feature: "settings" }
];

export const brandIcon = Activity;
