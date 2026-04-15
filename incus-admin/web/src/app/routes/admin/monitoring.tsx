import { createFileRoute } from "@tanstack/react-router";

export const Route = createFileRoute("/admin/monitoring")({
  component: MonitoringPage,
});

function MonitoringPage() {
  return (
    <div>
      <h1 className="text-2xl font-bold mb-6">Monitoring</h1>

      <div className="space-y-6">
        <div className="border border-border rounded-lg bg-card overflow-hidden">
          <div className="px-4 py-3 border-b border-border">
            <h3 className="font-semibold">Cluster Overview</h3>
            <p className="text-sm text-muted-foreground">
              Grafana dashboards are accessible via WireGuard tunnel.
              Direct access requires VPN or SSH tunnel to the cluster network.
            </p>
          </div>
          <div className="p-4 space-y-3">
            <MonitorLink
              title="Grafana Dashboard"
              url="http://10.0.20.1:3000"
              desc="Full monitoring UI — requires WireGuard VPN access"
            />
            <MonitorLink
              title="Prometheus"
              url="http://10.0.20.1:9090"
              desc="Metrics query and exploration"
            />
            <MonitorLink
              title="Alertmanager"
              url="http://10.0.20.1:9093"
              desc="Alert routing and silencing"
            />
          </div>
        </div>

        <div className="border border-border rounded-lg bg-card p-4">
          <h3 className="font-semibold mb-3">Quick Metrics</h3>
          <p className="text-sm text-muted-foreground mb-4">
            Node resource summary from the Incus cluster API (refreshed every 60s).
          </p>
          <div className="text-sm text-muted-foreground">
            See the <a href="/admin/clusters" className="text-primary hover:underline">Clusters</a> page
            for real-time node CPU/RAM/status data.
          </div>
        </div>
      </div>
    </div>
  );
}

function MonitorLink({ title, url, desc }: { title: string; url: string; desc: string }) {
  return (
    <div className="flex items-center justify-between p-3 rounded-lg border border-border">
      <div>
        <div className="font-medium text-sm">{title}</div>
        <div className="text-xs text-muted-foreground">{desc}</div>
      </div>
      <a
        href={url}
        target="_blank"
        rel="noopener noreferrer"
        className="px-3 py-1.5 rounded text-xs bg-primary/20 text-primary hover:bg-primary/30"
      >
        Open →
      </a>
    </div>
  );
}
