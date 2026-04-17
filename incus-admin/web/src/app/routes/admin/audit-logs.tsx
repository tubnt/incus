import { createFileRoute } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { http } from "@/shared/lib/http";

export const Route = createFileRoute("/admin/audit-logs")({
  component: AuditLogsPage,
});

interface AuditLog {
  id: number;
  user_id: number | null;
  action: string;
  target_type: string;
  target_id: number;
  details: string;
  ip_address: string;
  created_at: string;
}

// Falls back to details["name"] when target_id is 0 (e.g. admin ops where we didn't persist the id).
function targetLabel(log: AuditLog): string {
  if (log.target_id && log.target_id > 0) return `${log.target_type} #${log.target_id}`;
  try {
    const d = JSON.parse(log.details || "{}");
    const name = d?.name || d?.target || d?.vm || d?.vm_name;
    if (typeof name === "string" && name) return `${log.target_type} ${name}`;
  } catch {
    // details is not JSON — fall through
  }
  return log.target_type || "—";
}

function AuditLogsPage() {
  const { t } = useTranslation();
  const [offset, setOffset] = useState(0);
  const limit = 50;

  const { data, isLoading } = useQuery({
    queryKey: ["auditLogs", offset],
    queryFn: () =>
      http.get<{ logs: AuditLog[]; total: number }>(
        "/admin/audit-logs",
        { limit: String(limit), offset: String(offset) },
      ),
    refetchInterval: 15_000,
  });

  const logs = data?.logs ?? [];
  const total = data?.total ?? 0;

  return (
    <div>
      <div className="flex items-center justify-between mb-6">
        <h1 className="text-2xl font-bold">{t("admin.auditLogsTitle")}</h1>
        <span className="text-xs text-muted-foreground">{t("admin.totalRows", { count: total })}</span>
      </div>

      {isLoading ? (
        <div className="text-muted-foreground">{t("common.loading")}</div>
      ) : logs.length === 0 ? (
        <div className="border border-border rounded-lg p-6 text-center text-muted-foreground">
          {t("common.noData")}
        </div>
      ) : (
        <>
          <div className="border border-border rounded-lg overflow-hidden">
            <table className="w-full text-sm">
              <thead className="bg-muted/30">
                <tr>
                  <th className="text-left px-4 py-2 font-medium">{t("admin.auditTime")}</th>
                  <th className="text-left px-4 py-2 font-medium">{t("admin.auditUser")}</th>
                  <th className="text-left px-4 py-2 font-medium">{t("admin.auditAction")}</th>
                  <th className="text-left px-4 py-2 font-medium">{t("admin.auditTarget")}</th>
                  <th className="text-left px-4 py-2 font-medium">{t("admin.auditIp")}</th>
                  <th className="text-left px-4 py-2 font-medium">{t("admin.auditDetails")}</th>
                </tr>
              </thead>
              <tbody>
                {logs.map((log) => (
                  <tr key={log.id} className="border-t border-border">
                    <td className="px-4 py-2 text-xs text-muted-foreground whitespace-nowrap">
                      {new Date(log.created_at).toLocaleString()}
                    </td>
                    <td className="px-4 py-2 text-xs">{log.user_id ?? "—"}</td>
                    <td className="px-4 py-2">
                      <span className="px-2 py-0.5 rounded text-xs font-medium bg-primary/20 text-primary">
                        {log.action}
                      </span>
                    </td>
                    <td className="px-4 py-2 text-xs">
                      {targetLabel(log)}
                    </td>
                    <td className="px-4 py-2 text-xs font-mono">{log.ip_address || "—"}</td>
                    <td className="px-4 py-2 text-xs text-muted-foreground max-w-xs truncate">
                      {log.details}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>

          {total > limit && (
            <div className="flex justify-center gap-2 mt-4">
              <button
                onClick={() => setOffset(Math.max(0, offset - limit))}
                disabled={offset === 0}
                className="px-3 py-1.5 text-xs bg-muted/50 rounded disabled:opacity-30"
              >
                {t("admin.prevPage")}
              </button>
              <span className="px-3 py-1.5 text-xs text-muted-foreground">
                {offset + 1}-{Math.min(offset + limit, total)} / {total}
              </span>
              <button
                onClick={() => setOffset(offset + limit)}
                disabled={offset + limit >= total}
                className="px-3 py-1.5 text-xs bg-muted/50 rounded disabled:opacity-30"
              >
                {t("admin.nextPage")}
              </button>
            </div>
          )}
        </>
      )}
    </div>
  );
}
