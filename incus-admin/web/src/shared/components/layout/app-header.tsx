import { useTranslation } from "react-i18next";
import { Moon, Sun, Monitor, Globe, LogOut, Menu, UserX } from "lucide-react";
import { useTheme } from "@/shared/components/theme-provider";
import type { ShadowActingAs } from "@/shared/lib/auth";
import { cn } from "@/shared/lib/utils";

interface AppHeaderProps {
  email?: string;
  balance?: number;
  /**
   * When present the current session is shadowing another user. The header
   * switches to a red background + shows who you are acting as + an Exit
   * button that POSTs /shadow/exit.
   */
  actingAs?: ShadowActingAs;
  sidebarCollapsed: boolean;
  onMenuClick?: () => void;
}

export function AppHeader({ email, balance, actingAs, sidebarCollapsed, onMenuClick }: AppHeaderProps) {
  const { t, i18n } = useTranslation();
  const { theme, setTheme } = useTheme();

  const nextTheme = () => {
    const order = ["dark", "light", "system"] as const;
    const idx = order.indexOf(theme as any);
    setTheme(order[(idx + 1) % order.length]);
  };

  const toggleLang = () => {
    i18n.changeLanguage(i18n.language === "zh" ? "en" : "zh");
  };

  const ThemeIcon = theme === "dark" ? Moon : theme === "light" ? Sun : Monitor;

  const shadowMode = !!actingAs;

  return (
    <header className={cn(
      "fixed top-0 right-0 z-30 h-14 border-b flex items-center gap-3 px-4 transition-all",
      "left-0",
      sidebarCollapsed ? "md:left-16" : "md:left-60",
      shadowMode
        ? "bg-destructive text-destructive-foreground border-destructive-foreground/20"
        : "bg-card border-border",
    )}>
      <button
        onClick={onMenuClick}
        aria-label="Open menu"
        className={cn(
          "p-1.5 rounded md:hidden",
          shadowMode ? "hover:bg-white/10" : "hover:bg-muted/50 text-muted-foreground",
        )}
      >
        <Menu size={18} />
      </button>
      {shadowMode && (
        <div className="flex-1 flex items-center gap-2 min-w-0">
          <UserX size={16} className="shrink-0" />
          <span className="text-sm font-medium truncate">
            {t("shadow.bannerPrefix", { defaultValue: "正以用户身份操作" })}: {actingAs.target_email}
          </span>
          <span className="hidden md:inline text-xs opacity-80 truncate">
            ({t("shadow.actor", { defaultValue: "操作人" })}: {actingAs.actor_email})
          </span>
          <form method="POST" action="/shadow/exit" className="ml-auto">
            <button
              type="submit"
              className="px-3 py-1 text-xs bg-white/20 hover:bg-white/30 rounded font-medium"
            >
              {t("shadow.exit", { defaultValue: "退出 Shadow" })}
            </button>
          </form>
        </div>
      )}
      {!shadowMode && <div className="flex-1" />}
      <button onClick={toggleLang} className={cn("p-1.5 rounded", shadowMode ? "hover:bg-white/10" : "hover:bg-muted/50 text-muted-foreground")} title={t("topbar.language", { defaultValue: "Language" })}>
        <Globe size={16} />
      </button>
      <button onClick={nextTheme} className={cn("p-1.5 rounded", shadowMode ? "hover:bg-white/10" : "hover:bg-muted/50 text-muted-foreground")} title={t(`topbar.theme.${theme}`, { defaultValue: theme })}>
        <ThemeIcon size={16} />
      </button>
      {balance !== undefined && !shadowMode && (
        <span className="text-xs font-mono text-muted-foreground">${balance.toFixed(2)}</span>
      )}
      {!shadowMode && (
        <span className="hidden sm:inline text-sm text-muted-foreground truncate max-w-[180px]">{email}</span>
      )}
      {!shadowMode && (
        <a href="/oauth2/sign_out?rd=/" className="p-1.5 rounded hover:bg-muted/50 text-muted-foreground" title={t("topbar.logout", { defaultValue: "Logout" })}>
          <LogOut size={16} />
        </a>
      )}
    </header>
  );
}
