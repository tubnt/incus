import { createFileRoute } from "@tanstack/react-router";
import { useMutation } from "@tanstack/react-query";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { http } from "@/shared/lib/http";

export const Route = createFileRoute("/admin/node-ops")({
  component: NodeOpsPage,
});

// IPv4, IPv6 (basic) or RFC 1123 hostname.
const IPV4_RE = /^((25[0-5]|2[0-4]\d|[01]?\d\d?)\.){3}(25[0-5]|2[0-4]\d|[01]?\d\d?)$/;
const IPV6_RE = /^(([0-9a-fA-F]{1,4}:){7}[0-9a-fA-F]{1,4}|([0-9a-fA-F]{1,4}:){1,7}:|([0-9a-fA-F]{1,4}:){1,6}:[0-9a-fA-F]{1,4}|::([0-9a-fA-F]{1,4}:){0,6}[0-9a-fA-F]{1,4}|::)$/;
const HOSTNAME_RE = /^(?=.{1,253}$)([a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)(\.[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)*$/;

function validHost(h: string): boolean {
  const v = h.trim();
  if (!v) return false;
  return IPV4_RE.test(v) || IPV6_RE.test(v) || HOSTNAME_RE.test(v);
}

function NodeOpsPage() {
  const { t } = useTranslation();
  const [host, setHost] = useState("");
  const [hostTouched, setHostTouched] = useState(false);
  const [output, setOutput] = useState("");

  const testMutation = useMutation({
    mutationFn: () => http.post<{ status: string; output: string; error?: string }>("/admin/nodes/test-ssh", { host }),
    onSuccess: (data) => setOutput(data.output + (data.error ? `\nError: ${data.error}` : "")),
  });

  const execMutation = useMutation({
    mutationFn: (cmd: string) => http.post<{ status: string; output: string; error?: string }>("/admin/nodes/exec", { host, command: cmd }),
    onSuccess: (data) => setOutput(data.output + (data.error ? `\nError: ${data.error}` : "")),
  });

  const quickCommands = [
    { label: "System Info", cmd: "hostname && uname -r && cat /etc/os-release | head -3" },
    { label: "Memory", cmd: "free -h" },
    { label: "Disk", cmd: "df -h" },
    { label: "Block Devices", cmd: "lsblk -d -o NAME,SIZE,ROTA,MODEL" },
    { label: "Network", cmd: "ip addr show | grep 'inet '" },
    { label: "Incus Status", cmd: "incus cluster list 2>/dev/null || echo 'incus not installed'" },
    { label: "Ceph Status", cmd: "ceph -s 2>/dev/null || echo 'ceph not available'" },
  ];

  const hostInvalid = hostTouched && !!host && !validHost(host);
  const canAct = validHost(host);

  return (
    <div>
      <h1 className="text-2xl font-bold mb-6">{t("admin.nodeOpsTitle")}</h1>

      <div className="border border-border rounded-lg bg-card p-4 mb-6">
        <h3 className="font-semibold mb-3">{t("admin.sshConnection")}</h3>
        <div className="flex gap-3 mb-1">
          <input type="text" value={host}
            onChange={(e) => setHost(e.target.value)}
            onBlur={() => setHostTouched(true)}
            placeholder={t("admin.hostPlaceholder")}
            className={`flex-1 px-3 py-2 rounded border bg-card text-sm font-mono ${hostInvalid ? "border-destructive" : "border-border"}`} />
          <button onClick={() => testMutation.mutate()}
            disabled={testMutation.isPending || !canAct}
            className="px-4 py-2 bg-primary text-primary-foreground rounded text-sm font-medium disabled:opacity-50">
            {testMutation.isPending ? t("admin.sshTesting") : t("admin.sshTest")}
          </button>
        </div>
        {hostInvalid && (
          <div className="text-xs text-destructive mb-2">{t("admin.hostInvalid")}</div>
        )}

        {canAct && (
          <div className="flex flex-wrap gap-2 mt-3">
            {quickCommands.map((qc) => (
              <button key={qc.label} onClick={() => execMutation.mutate(qc.cmd)}
                disabled={execMutation.isPending}
                className="px-3 py-1.5 text-xs bg-muted/50 text-muted-foreground rounded hover:bg-muted disabled:opacity-50">
                {qc.label}
              </button>
            ))}
          </div>
        )}
      </div>

      {output && (
        <div className="border border-border rounded-lg overflow-hidden">
          <div className="px-4 py-2 bg-muted/30 flex items-center justify-between">
            <span className="text-sm font-medium">{t("common.output")}</span>
            <button onClick={() => setOutput("")} className="text-xs text-muted-foreground hover:text-foreground">{t("common.clear")}</button>
          </div>
          <pre className="p-4 text-xs font-mono bg-black text-green-400 overflow-x-auto whitespace-pre-wrap max-h-96">
            {output}
          </pre>
        </div>
      )}
    </div>
  );
}
