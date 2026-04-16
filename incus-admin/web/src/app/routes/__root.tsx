import { createRootRoute, Outlet } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
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

function RootLayout() {
  const { t } = useTranslation();
  const [collapsed, setCollapsed] = useState(false);
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

  return (
    <div className="min-h-screen bg-background">
      <Toaster position="top-right" richColors closeButton />
      <AppSidebar
        isAdmin={isAdmin(user)}
        collapsed={collapsed}
        onToggle={() => setCollapsed(!collapsed)}
      />
      <AppHeader
        email={user.email}
        balance={user.balance}
        sidebarCollapsed={collapsed}
      />
      <main className={cn(
        "pt-14 transition-all min-h-screen",
        collapsed ? "pl-16" : "pl-60",
      )}>
        <div className="max-w-7xl mx-auto px-6 py-6">
          <ErrorBoundary>
            <Outlet />
          </ErrorBoundary>
        </div>
      </main>
    </div>
  );
}
