import {
  Activity,
  Ban,
  BellRing,
  Bot,
  Brain,
  Bug,
  ClipboardList,
  Crosshair,
  FlaskConical,
  Briefcase,
  FolderOpen,
  FolderSync,
  LayoutDashboard,
  type LucideIcon,
  MessageSquare,
  Network,
  Plug,
  Radio,
  ScrollText,
  Settings2,
  Shield,
  ShieldAlert,
  Sparkles,
  Target,
  Terminal,
  Wrench,
  Zap,
} from "lucide-react";

import { t } from "@/i18n";

export type NavBadge = "new" | "soon";

export interface NavSubItem {
  id: string;
  title: string;
  url: string;
  icon?: LucideIcon;
  badge?: NavBadge;
  disabled?: boolean;
  newTab?: boolean;
}

interface NavItemBase {
  id: string;
  title: string;
  icon?: LucideIcon;
  badge?: NavBadge;
  disabled?: boolean;
  newTab?: boolean;
}

export interface NavMainLinkItem extends NavItemBase {
  url: string;
  subItems?: never;
}

export interface NavMainParentItem extends NavItemBase {
  subItems: NavSubItem[];
}

export type NavMainItem = NavMainLinkItem | NavMainParentItem;

export interface NavGroup {
  id: number;
  label?: string;
  items: NavMainItem[];
}

export const sidebarItems: NavGroup[] = [
  {
    id: 1,
    label: t("Functions"),
    items: [
      { id: "dashboard", title: t("Dashboard"), url: "/dashboard", icon: LayoutDashboard },
      { id: "chat", title: t("Chat"), url: "/chat", icon: MessageSquare },
      { id: "tasks", title: t("Tasks"), url: "/function/tasks", icon: Target },
      { id: "engagements", title: t("Engagements"), url: "/function/engagements", icon: Briefcase },
      { id: "findings", title: t("Findings"), url: "/function/findings", icon: Bug },
      { id: "traffic", title: t("Traffic"), url: "/function/traffic", icon: Activity },
      { id: "commands", title: t("Tool Execution"), url: "/function/commands", icon: Terminal },
      { id: "llm-records", title: t("LLM Recording"), url: "/function/llm-records", icon: Radio },
      { id: "assets", title: t("Assets"), url: "/function/assets", icon: Network },
      { id: "sync", title: t("Asset Sync"), url: "/function/sync", icon: FolderSync },
      { id: "workspace", title: t("Workspace"), url: "/function/workspace", icon: FolderOpen },
    ],
  },
  {
    id: 2,
    label: t("Lab"),
    items: [
      { id: "lab-desync", title: t("H2 Desync"), url: "/lab/desync", icon: Activity },
      { id: "lab-cpg", title: t("CPG / Taint"), url: "/lab/cpg", icon: Terminal },
      { id: "lab-fuzzer", title: t("Fuzzer"), url: "/lab/fuzzer", icon: Zap },
      { id: "lab-oast", title: t("OAST"), url: "/lab/oast", icon: Radio },
      { id: "lab-sliver", title: t("Sliver C2"), url: "/lab/sliver", icon: Crosshair },
      { id: "lab-mullvad", title: t("Mullvad VPN"), url: "/lab/mullvad", icon: Shield },
    ],
  },
  {
    id: 3,
    label: t("System"),
    items: [
      { id: "llm", title: t("LLM"), url: "/system/llm", icon: Brain },
      { id: "agents", title: t("Agent"), url: "/system/agents", icon: Bot },
      { id: "mcp", title: t("MCP"), url: "/system/mcp", icon: Plug },
      { id: "skills", title: t("Skill"), url: "/system/skills", icon: Sparkles },
      { id: "tools", title: t("Tools"), url: "/system/tools", icon: Wrench },
      { id: "notify", title: t("Notifications"), url: "/system/notify", icon: BellRing },
      { id: "intercept", title: t("Interception Rules"), url: "/system/intercept", icon: ShieldAlert },
      { id: "asset-intercept", title: t("Asset Interception"), url: "/system/intercept/assets", icon: Ban },
      { id: "approvals", title: t("Approval Records"), url: "/system/intercept/approvals", icon: ClipboardList },
      { id: "logs", title: t("Logs"), url: "/system/logs", icon: ScrollText },
      { id: "settings", title: t("System Settings"), url: "/system/settings", icon: Settings2 },
    ],
  },
];
