import { Link, useRouterState } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import {
  LayoutDashboard, Server, Key, CreditCard, MessageSquare, KeyRound,
  Network, ServerCog, Activity, Globe, Users, Package, ShoppingCart,
  Ticket, FileText, Plus, ChevronLeft, Menu, Shield, HardDrive, BarChart3, MapPin, Terminal, Settings,
} from "lucide-react";
import { cn } from "@/shared/lib/utils";
import { sidebarGroups } from "./sidebar-data";

const iconMap: Record<string, React.ElementType> = {
  LayoutDashboard, Server, Key, CreditCard, MessageSquare, KeyRound,
  Network, ServerCog, Activity, Globe, Users, Package, ShoppingCart,
  Ticket, FileText, Plus, Menu, Shield, HardDrive, BarChart3, MapPin, Terminal, Settings,
};

interface AppSidebarProps {
  isAdmin: boolean;
  collapsed: boolean;
  onToggle: () => void;
}

export function AppSidebar({ isAdmin, collapsed, onToggle }: AppSidebarProps) {
  const { t } = useTranslation();
  const routerState = useRouterState();
  const currentPath = routerState.location.pathname;

  return (
    <aside className={cn(
      "fixed left-0 top-0 z-40 h-screen bg-card border-r border-border flex flex-col transition-all duration-200",
      collapsed ? "w-16 max-md:-translate-x-full" : "w-60",
    )}>
      <div className="flex items-center justify-between h-14 px-4 border-b border-border">
        {!collapsed && <span className="font-bold text-lg">IncusAdmin</span>}
        <button onClick={onToggle} className="p-1.5 rounded hover:bg-muted/50">
          {collapsed ? <Menu size={18} /> : <ChevronLeft size={18} />}
        </button>
      </div>

      <nav className="flex-1 overflow-y-auto py-2">
        {sidebarGroups.map((group, gi) => {
          const visibleItems = group.items.filter((item) => !item.adminOnly || isAdmin);
          if (visibleItems.length === 0) return null;

          return (
            <div key={gi} className="mb-1">
              {group.titleKey && !collapsed && (
                <div className="px-4 py-1.5 text-xs font-medium text-muted-foreground uppercase tracking-wider">
                  {group.titleKey === "admin" ? "Admin" : ""}
                </div>
              )}
              {group.titleKey === "admin" && <div className="mx-3 mb-1 border-t border-border" />}
              {visibleItems.map((item) => {
                const Icon = iconMap[item.icon] ?? LayoutDashboard;
                const isActive = currentPath === item.to || (item.to !== "/" && currentPath.startsWith(item.to));

                return (
                  <Link
                    key={item.to}
                    to={item.to as any}
                    className={cn(
                      "flex items-center gap-3 mx-2 px-3 py-2 rounded-md text-sm transition-colors",
                      isActive
                        ? "bg-primary/10 text-primary font-medium"
                        : "text-muted-foreground hover:bg-muted/50 hover:text-foreground",
                      collapsed && "justify-center px-0",
                    )}
                    title={collapsed ? t(item.key) : undefined}
                  >
                    <Icon size={18} />
                    {!collapsed && <span>{t(item.key)}</span>}
                  </Link>
                );
              })}
            </div>
          );
        })}
      </nav>
    </aside>
  );
}
