import { createRootRoute, Link, Outlet } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { fetchCurrentUser, isAdmin } from "@/shared/lib/auth";

export const Route = createRootRoute({
  component: RootLayout,
});

function RootLayout() {
  const { data: user, isLoading, isError } = useQuery({
    queryKey: ["currentUser"],
    queryFn: fetchCurrentUser,
    retry: false,
  });

  if (isLoading) {
    return (
      <div className="flex items-center justify-center min-h-screen">
        <div className="text-muted-foreground">Loading...</div>
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
          Sign in with SSO
        </a>
      </div>
    );
  }

  return (
    <div className="min-h-screen bg-background">
      <nav className="border-b border-border bg-card">
        <div className="max-w-7xl mx-auto px-4 flex items-center h-14 gap-6">
          <Link to="/" className="font-bold text-lg">
            IncusAdmin
          </Link>
          <div className="flex gap-4 text-sm">
            <Link to="/" className="text-foreground/70 hover:text-foreground [&.active]:text-foreground [&.active]:font-medium">
              Dashboard
            </Link>
            <Link to="/vms" className="text-foreground/70 hover:text-foreground [&.active]:text-foreground [&.active]:font-medium">
              My VMs
            </Link>
            {user && isAdmin(user) && (
              <>
                <Link to="/admin/clusters" className="text-foreground/70 hover:text-foreground [&.active]:text-foreground [&.active]:font-medium">
                  Clusters
                </Link>
                <Link to="/admin/vms" className="text-foreground/70 hover:text-foreground [&.active]:text-foreground [&.active]:font-medium">
                  All VMs
                </Link>
                <Link to="/admin/users" className="text-foreground/70 hover:text-foreground [&.active]:text-foreground [&.active]:font-medium">
                  Users
                </Link>
              </>
            )}
          </div>
          <div className="ml-auto text-sm text-muted-foreground">
            {user?.email}
          </div>
        </div>
      </nav>
      <main className="max-w-7xl mx-auto px-4 py-6">
        <Outlet />
      </main>
    </div>
  );
}
