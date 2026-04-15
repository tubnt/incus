import { createFileRoute, Outlet, redirect } from "@tanstack/react-router";
import { queryClient } from "@/shared/lib/query-client";
import { fetchCurrentUser, isAdmin } from "@/shared/lib/auth";

export const Route = createFileRoute("/admin")({
  beforeLoad: async () => {
    try {
      const cached = queryClient.getQueryData<{ role: string }>(["currentUser"]);
      const user = cached ?? await queryClient.fetchQuery({
        queryKey: ["currentUser"],
        queryFn: fetchCurrentUser,
      });
      if (!user || !isAdmin(user)) {
        throw redirect({ to: "/" });
      }
    } catch (e) {
      if (e && typeof e === "object" && "to" in e) throw e;
      throw redirect({ to: "/" });
    }
  },
  component: () => <Outlet />,
});
