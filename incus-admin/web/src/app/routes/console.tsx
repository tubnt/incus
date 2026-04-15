import { createFileRoute } from "@tanstack/react-router";
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

  if (!vm || !cluster) {
    return (
      <div className="flex items-center justify-center min-h-[60vh]">
        <div className="text-muted-foreground">
          Missing parameters. Use: /console?vm=NAME&cluster=CLUSTER&project=PROJECT
        </div>
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
        <a href="/admin/vms" className="text-sm text-primary hover:underline">
          ← Back to VMs
        </a>
      </div>
      <ConsoleTerminal vmName={vm} project={project} cluster={cluster} />
    </div>
  );
}
