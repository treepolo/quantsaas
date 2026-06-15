import {
  Activity,
  BarChart3,
  Bot,
  FlaskConical,
  LayoutDashboard,
  LucideIcon,
  Settings,
  Shapes,
  Workflow
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
  { to: "/", labelKey: "nav.dashboard", icon: LayoutDashboard, placement: "main", feature: "dashboard", end: true },
  { to: "/templates", labelKey: "nav.templates", icon: Shapes, placement: "main", feature: "strategies" },
  { to: "/instances", labelKey: "nav.instances", icon: Workflow, placement: "main", feature: "strategies" },
  { to: "/evolution", labelKey: "nav.evolution", icon: FlaskConical, placement: "main", feature: "strategies" },
  { to: "/agents", labelKey: "nav.agents", icon: Bot, placement: "main", feature: "agents" },
  { to: "/backtesting", labelKey: "nav.backtesting", icon: BarChart3, placement: "main", feature: "backtesting" },
  { to: "/settings", labelKey: "nav.settings", icon: Settings, placement: "footer", feature: "settings" }
];

export const brandIcon = Activity;
