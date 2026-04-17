import { createFileRoute } from "@tanstack/react-router";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { type AuditLog, useAuditLogsQuery } from "@/features/audit-logs/api";
import { Pagination } from "@/shared/components/ui/pagination";

export const Route = createFileRoute("/admin/audit-logs")({
  component: AuditLogsPage,
});

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
  const [limit, setLimit] = useState(50);

  const { data, isLoading } = useAuditLogsQuery(offset, limit);

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

          <Pagination
            total={total}
            limit={limit}
            offset={offset}
            onChange={(nextLimit, nextOffset) => {
              setLimit(nextLimit);
              setOffset(nextOffset);
            }}
            className="mt-3"
          />
        </>
      )}
    </div>
  );
}
