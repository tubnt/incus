import { useEffect, useMemo, useState } from "react";
import { Link, useRouterState } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import {
  LayoutDashboard, Server, Key, CreditCard, MessageSquare, KeyRound,
  Network, ServerCog, Activity, Globe, Users, Package, ShoppingCart,
  Ticket, FileText, Plus, ChevronLeft, Menu, Shield, HardDrive, BarChart3, MapPin, Terminal, Settings, X,
  ChevronDown, ArrowLeft, ArrowRight,
} from "lucide-react";
import { Accordion } from "@base-ui-components/react/accordion";
import { cn } from "@/shared/lib/utils";
import { adminSidebar, userSidebar, type NavGroup, type NavItem } from "./sidebar-data";

const iconMap: Record<string, React.ElementType> = {
  LayoutDashboard, Server, Key, CreditCard, MessageSquare, KeyRound,
  Network, ServerCog, Activity, Globe, Users, Package, ShoppingCart,
  Ticket, FileText, Plus, Menu, Shield, HardDrive, BarChart3, MapPin, Terminal, Settings,
};

const OPEN_GROUPS_LS_KEY = "incus.sidebar.admin.openGroups";

interface AppSidebarProps {
  isAdmin: boolean;
  collapsed: boolean;
  mobileOpen?: boolean;
  onToggle: () => void;
  onNavigate?: () => void;
}

export function AppSidebar({ isAdmin, collapsed, mobileOpen, onToggle, onNavigate }: AppSidebarProps) {
  const routerState = useRouterState();
  const currentPath = routerState.location.pathname;
  const perspective: "admin" | "user" = currentPath.startsWith("/admin") ? "admin" : "user";
  const showLabels = !collapsed || Boolean(mobileOpen);

  const mobileTx = mobileOpen ? "translate-x-0 w-60" : "-translate-x-full w-60";
  const desktopLayout = collapsed ? "md:w-16 md:translate-x-0" : "md:w-60 md:translate-x-0";

  return (
    <aside className={cn(
      "fixed left-0 top-0 z-40 h-screen bg-card border-r border-border flex flex-col transition-all duration-200",
      mobileTx,
      desktopLayout,
    )}>
      <div className="flex items-center justify-between h-14 px-4 border-b border-border">
        {showLabels && <span className="font-bold text-lg">IncusAdmin</span>}
        <button
          onClick={onToggle}
          aria-label="Toggle sidebar"
          className="p-1.5 rounded hover:bg-muted/50"
        >
          {mobileOpen ? <X size={18} className="md:hidden" /> : null}
          {!mobileOpen && (collapsed ? <Menu size={18} /> : <ChevronLeft size={18} />)}
        </button>
      </div>

      {isAdmin && (
        <PerspectiveSwitch
          perspective={perspective}
          showLabels={showLabels}
          onNavigate={onNavigate}
        />
      )}

      <nav className="flex-1 overflow-y-auto py-2">
        {perspective === "user"
          ? <UserNav items={userSidebar} currentPath={currentPath} showLabels={showLabels} onNavigate={onNavigate} />
          : <AdminNav groups={adminSidebar} currentPath={currentPath} showLabels={showLabels} onNavigate={onNavigate} />}
      </nav>
    </aside>
  );
}

function PerspectiveSwitch({
  perspective,
  showLabels,
  onNavigate,
}: {
  perspective: "admin" | "user";
  showLabels: boolean;
  onNavigate?: () => void;
}) {
  const { t } = useTranslation();
  const toAdmin = perspective === "user";
  const targetPath = toAdmin ? "/admin/monitoring" : "/";
  const label = toAdmin ? t("sidebar.switchToAdmin") : t("sidebar.backToUser");
  const Icon = toAdmin ? ArrowRight : ArrowLeft;

  return (
    <Link
      to={targetPath}
      onClick={onNavigate}
      title={!showLabels ? label : undefined}
      className={cn(
        "flex items-center gap-2 mx-2 my-2 px-3 py-2 rounded-md text-sm border border-border",
        "bg-muted/30 text-foreground hover:bg-muted/60 transition-colors",
        !showLabels && "justify-center px-0",
      )}
    >
      {!toAdmin && showLabels && <Icon size={14} />}
      {showLabels ? <span className="truncate">{label}</span> : <Icon size={16} />}
      {toAdmin && showLabels && <Icon size={14} className="ml-auto" />}
    </Link>
  );
}

function UserNav({
  items,
  currentPath,
  showLabels,
  onNavigate,
}: {
  items: NavItem[];
  currentPath: string;
  showLabels: boolean;
  onNavigate?: () => void;
}) {
  const { t } = useTranslation();
  return (
    <div>
      {items.map((item) => {
        const Icon = iconMap[item.icon] ?? LayoutDashboard;
        const isActive = isItemActive(currentPath, item.to);
        return (
          <Link
            key={item.to}
            to={item.to as any}
            onClick={onNavigate}
            className={cn(
              "flex items-center gap-3 mx-2 px-3 py-2 rounded-md text-sm transition-colors",
              isActive
                ? "bg-primary/10 text-primary font-medium"
                : "text-muted-foreground hover:bg-muted/50 hover:text-foreground",
              !showLabels && "justify-center px-0",
            )}
            title={!showLabels ? t(item.key) : undefined}
          >
            <Icon size={18} />
            {showLabels && <span>{t(item.key)}</span>}
          </Link>
        );
      })}
    </div>
  );
}

function AdminNav({
  groups,
  currentPath,
  showLabels,
  onNavigate,
}: {
  groups: NavGroup[];
  currentPath: string;
  showLabels: boolean;
  onNavigate?: () => void;
}) {
  const { t } = useTranslation();
  const activeGroupKey = useMemo(
    () => groups.find((g) => g.items.some((i) => isItemActive(currentPath, i.to)))?.key,
    [groups, currentPath],
  );

  const [openGroups, setOpenGroups] = useState<string[]>(() => {
    if (typeof window === "undefined") return activeGroupKey ? [activeGroupKey] : [];
    try {
      const raw = window.localStorage.getItem(OPEN_GROUPS_LS_KEY);
      const parsed = raw ? (JSON.parse(raw) as string[]) : [];
      return activeGroupKey && !parsed.includes(activeGroupKey) ? [...parsed, activeGroupKey] : parsed;
    } catch {
      return activeGroupKey ? [activeGroupKey] : [];
    }
  });

  useEffect(() => {
    if (activeGroupKey && !openGroups.includes(activeGroupKey)) {
      setOpenGroups((prev) => [...prev, activeGroupKey]);
    }
  }, [activeGroupKey]); // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    try {
      window.localStorage.setItem(OPEN_GROUPS_LS_KEY, JSON.stringify(openGroups));
    } catch {
      // swallow — non-critical persistence
    }
  }, [openGroups]);

  // Collapsed sidebar: render flat icon list with group dividers; no Accordion.
  if (!showLabels) {
    return (
      <div>
        {groups.map((group, idx) => (
          <div key={group.key}>
            {idx > 0 && <div className="mx-3 my-1 border-t border-border" />}
            {group.items.map((item) => {
              const Icon = iconMap[item.icon] ?? LayoutDashboard;
              const isActive = isItemActive(currentPath, item.to);
              return (
                <Link
                  key={item.to}
                  to={item.to as any}
                  onClick={onNavigate}
                  title={t(item.key)}
                  className={cn(
                    "flex items-center justify-center mx-2 px-0 py-2 rounded-md text-sm transition-colors",
                    isActive
                      ? "bg-primary/10 text-primary"
                      : "text-muted-foreground hover:bg-muted/50 hover:text-foreground",
                  )}
                >
                  <Icon size={18} />
                </Link>
              );
            })}
          </div>
        ))}
      </div>
    );
  }

  return (
    <Accordion.Root
      value={openGroups}
      onValueChange={(v) => setOpenGroups(v.filter((x): x is string => typeof x === "string"))}
      multiple
      className="flex flex-col gap-0.5"
    >
      {groups.map((group) => {
        const GroupIcon = iconMap[group.icon] ?? LayoutDashboard;
        const hasActive = group.items.some((i) => isItemActive(currentPath, i.to));
        return (
          <Accordion.Item key={group.key} value={group.key} className="">
            <Accordion.Header className="m-0">
              <Accordion.Trigger
                className={cn(
                  "group flex w-full items-center gap-3 mx-2 px-3 py-2 rounded-md text-sm transition-colors",
                  hasActive
                    ? "text-foreground font-medium"
                    : "text-muted-foreground hover:bg-muted/50 hover:text-foreground",
                )}
                style={{ width: "calc(100% - 1rem)" }}
              >
                <GroupIcon size={18} />
                <span className="flex-1 text-left truncate">{t(group.titleKey)}</span>
                <ChevronDown
                  size={16}
                  className="transition-transform duration-150 group-data-[panel-open]:rotate-180"
                />
              </Accordion.Trigger>
            </Accordion.Header>
            <Accordion.Panel className="overflow-hidden transition-[height] duration-150 ease-out data-[starting-style]:h-0 data-[ending-style]:h-0 h-[var(--accordion-panel-height)]">
              <div className="pl-3">
                {group.items.map((item) => {
                  const Icon = iconMap[item.icon] ?? LayoutDashboard;
                  const isActive = isItemActive(currentPath, item.to);
                  return (
                    <Link
                      key={item.to}
                      to={item.to as any}
                      onClick={onNavigate}
                      className={cn(
                        "flex items-center gap-3 mx-2 px-3 py-1.5 rounded-md text-sm transition-colors",
                        isActive
                          ? "bg-primary/10 text-primary font-medium"
                          : "text-muted-foreground hover:bg-muted/50 hover:text-foreground",
                      )}
                    >
                      <Icon size={16} />
                      <span className="truncate">{t(item.key)}</span>
                    </Link>
                  );
                })}
              </div>
            </Accordion.Panel>
          </Accordion.Item>
        );
      })}
    </Accordion.Root>
  );
}

function isItemActive(currentPath: string, to: string): boolean {
  if (to === "/") return currentPath === "/";
  return currentPath === to || currentPath.startsWith(to + "/");
}
