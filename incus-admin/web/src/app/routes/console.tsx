import { createFileRoute } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { fetchCurrentUser, isAdmin } from "@/shared/lib/auth";
import { ConsoleTerminal } from "@/features/console/terminal";

export const Route = createFileRoute("/console")({
  validateSearch: (search: Record<string, unknown>) => ({
    vm: (search.vm as string) || "",
    project: (search.project as string) || "customers",
    cluster: (search.cluster as string) || "",
  }),
  component: ConsolePage,
});

function ConsolePage() {
  const { vm, project, cluster } = Route.useSearch();
  const { data: user } = useQuery({ queryKey: ["currentUser"], queryFn: fetchCurrentUser });
  const backUrl = user && isAdmin(user) ? "/admin/vms" : "/vms";

  if (!vm || !cluster) {
    const missing: string[] = [];
    if (!vm) missing.push("vm");
    if (!cluster) missing.push("cluster");
    return (
      <div className="flex flex-col items-center justify-center min-h-[60vh] gap-3">
        <div className="text-muted-foreground text-sm">
          Missing parameter{missing.length > 1 ? "s" : ""}: <span className="font-mono">{missing.join(", ")}</span>
        </div>
        <div className="text-xs text-muted-foreground font-mono">
          /console?vm=NAME&amp;cluster=CLUSTER&amp;project=PROJECT
        </div>
        <a href={backUrl} className="text-sm text-primary hover:underline">← Back to VMs</a>
      </div>
    );
  }

  return (
    <div>
      <div className="flex items-center justify-between mb-4">
        <div>
          <h1 className="text-xl font-bold">Console: {vm}</h1>
          <p className="text-sm text-muted-foreground">{cluster} / {project}</p>
        </div>
        <a href={backUrl} className="text-sm text-primary hover:underline">
          ← Back to VMs
        </a>
      </div>
      <ConsoleTerminal vmName={vm} project={project} cluster={cluster} />
    </div>
  );
}
