import { createRootRoute, Outlet } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { fetchCurrentUser, isAdmin } from "@/shared/lib/auth";
import { useTranslation } from "react-i18next";
import { AppSidebar } from "@/shared/components/layout/app-sidebar";
import { AppHeader } from "@/shared/components/layout/app-header";
import { cn } from "@/shared/lib/utils";
import { Toaster } from "sonner";
import { ErrorBoundary } from "@/shared/components/error-boundary";

export const Route = createRootRoute({
  component: RootLayout,
  notFoundComponent: NotFound,
});

function NotFound() {
  return (
    <div className="flex flex-col items-center justify-center py-20 gap-4">
      <div className="text-6xl font-bold text-muted-foreground">404</div>
      <p className="text-muted-foreground">Page not found</p>
      <a href="/" className="px-4 py-2 bg-primary text-primary-foreground rounded-md text-sm font-medium hover:opacity-90">
        Back to Dashboard
      </a>
    </div>
  );
}

function useIsMobile() {
  const [isMobile, setIsMobile] = useState(() =>
    typeof window === "undefined" ? false : window.innerWidth < 768,
  );
  useEffect(() => {
    const handler = () => setIsMobile(window.innerWidth < 768);
    window.addEventListener("resize", handler);
    return () => window.removeEventListener("resize", handler);
  }, []);
  return isMobile;
}

function RootLayout() {
  const { t } = useTranslation();
  const isMobile = useIsMobile();
  // Desktop: collapsed controls narrow rail. Mobile: drawer is closed unless toggled.
  const [desktopCollapsed, setDesktopCollapsed] = useState(false);
  const [mobileOpen, setMobileOpen] = useState(false);
  useEffect(() => {
    if (!isMobile) setMobileOpen(false);
  }, [isMobile]);

  const { data: user, isLoading, isError } = useQuery({
    queryKey: ["currentUser"],
    queryFn: fetchCurrentUser,
    retry: false,
  });

  if (isLoading) {
    return (
      <div className="flex items-center justify-center min-h-screen">
        <div className="text-muted-foreground">{t("common.loading")}</div>
      </div>
    );
  }

  if (isError || !user) {
    return (
      <div className="flex flex-col items-center justify-center min-h-screen gap-4">
        <h1 className="text-2xl font-bold">IncusAdmin</h1>
        <p className="text-muted-foreground">Please sign in to continue.</p>
        <a
          href="/oauth2/start?rd=/"
          className="px-6 py-2 bg-primary text-primary-foreground rounded-md font-medium hover:opacity-90"
        >
          {t("common.signIn")}
        </a>
      </div>
    );
  }

  const toggleSidebar = () => {
    if (isMobile) setMobileOpen((v) => !v);
    else setDesktopCollapsed((v) => !v);
  };

  return (
    <div className="min-h-screen bg-background">
      <Toaster position="top-right" richColors closeButton />
      <AppSidebar
        isAdmin={isAdmin(user)}
        collapsed={desktopCollapsed}
        mobileOpen={mobileOpen}
        onToggle={toggleSidebar}
        onNavigate={() => setMobileOpen(false)}
      />
      {mobileOpen && (
        <button
          type="button"
          aria-label="Close sidebar"
          onClick={() => setMobileOpen(false)}
          className="fixed inset-0 z-30 bg-black/40 md:hidden"
        />
      )}
      <AppHeader
        email={user.email}
        balance={user.balance}
        actingAs={user.acting_as}
        sidebarCollapsed={desktopCollapsed}
        onMenuClick={toggleSidebar}
      />
      <main className={cn(
        "pt-14 transition-all min-h-screen",
        isMobile ? "pl-0" : desktopCollapsed ? "pl-16" : "pl-60",
      )}>
        <div className="max-w-7xl mx-auto px-4 md:px-6 py-4 md:py-6">
          <ErrorBoundary>
            <Outlet />
          </ErrorBoundary>
        </div>
      </main>
    </div>
  );
}
