import { useTranslation } from "react-i18next";
import { Moon, Sun, Monitor, Globe, LogOut, Menu } from "lucide-react";
import { useTheme } from "@/shared/components/theme-provider";
import { cn } from "@/shared/lib/utils";

interface AppHeaderProps {
  email?: string;
  balance?: number;
  sidebarCollapsed: boolean;
  onMenuClick?: () => void;
}

export function AppHeader({ email, balance, sidebarCollapsed, onMenuClick }: AppHeaderProps) {
  const { i18n } = useTranslation();
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

  return (
    <header className={cn(
      "fixed top-0 right-0 z-30 h-14 bg-card border-b border-border flex items-center gap-3 px-4 transition-all",
      "left-0",
      sidebarCollapsed ? "md:left-16" : "md:left-60",
    )}>
      <button
        onClick={onMenuClick}
        aria-label="Open menu"
        className="p-1.5 rounded hover:bg-muted/50 text-muted-foreground md:hidden"
      >
        <Menu size={18} />
      </button>
      <div className="flex-1" />
      <button onClick={toggleLang} className="p-1.5 rounded hover:bg-muted/50 text-muted-foreground" title="Language">
        <Globe size={16} />
      </button>
      <button onClick={nextTheme} className="p-1.5 rounded hover:bg-muted/50 text-muted-foreground" title={theme}>
        <ThemeIcon size={16} />
      </button>
      {balance !== undefined && (
        <span className="text-xs font-mono text-muted-foreground">${balance.toFixed(2)}</span>
      )}
      <span className="hidden sm:inline text-sm text-muted-foreground truncate max-w-[180px]">{email}</span>
      <a href="/oauth2/sign_out?rd=/" className="p-1.5 rounded hover:bg-muted/50 text-muted-foreground" title="Logout">
        <LogOut size={16} />
      </a>
    </header>
  );
}
