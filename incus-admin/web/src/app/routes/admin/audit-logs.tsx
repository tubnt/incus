import { createFileRoute } from "@tanstack/react-router";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { useAuditLogsQuery } from "@/features/audit-logs/api";
import { stripCidrSuffix, targetLabel } from "@/features/audit-logs/helpers";
import { Pagination } from "@/shared/components/ui/pagination";

export const Route = createFileRoute("/admin/audit-logs")({
  component: AuditLogsPage,
});

function AuditLogsPage() {
  const { t } = useTranslation();
  const [offset, setOffset] = useState(0);
  const [limit, setLimit] = useState(50);
  // CSV export form state — these only affect the download URL, not the
  // paginated list above. TODO(i18n): labels here use literals; move to
  // translation keys once admin page batch sync lands.
  const [exportFrom, setExportFrom] = useState("");
  const [exportTo, setExportTo] = useState("");
  const [exportAction, setExportAction] = useState("");

  const { data, isLoading } = useAuditLogsQuery(offset, limit);

  const logs = data?.logs ?? [];
  const total = data?.total ?? 0;

  const exportParams = new URLSearchParams();
  if (exportFrom) exportParams.set("from", exportFrom);
  if (exportTo) exportParams.set("to", exportTo);
  if (exportAction) exportParams.set("action", exportAction);
  const exportQuery = exportParams.toString();
  const exportHref = `/api/admin/audit-logs/export${exportQuery ? `?${exportQuery}` : ""}`;

  return (
    <div>
      <div className="flex items-center justify-between mb-6">
        <h1 className="text-2xl font-bold">{t("admin.auditLogsTitle")}</h1>
        <span className="text-xs text-muted-foreground">{t("admin.totalRows", { count: total })}</span>
      </div>

      <div className="flex flex-wrap gap-3 items-end mb-4 p-3 border border-border rounded-lg bg-muted/20">
        <div className="flex flex-col">
          <label className="text-xs text-muted-foreground mb-1">起始日期</label>
          <input
            type="date"
            value={exportFrom}
            onChange={(e) => setExportFrom(e.target.value)}
            className="h-8 px-2 text-sm border border-border rounded bg-background"
          />
        </div>
        <div className="flex flex-col">
          <label className="text-xs text-muted-foreground mb-1">截止日期</label>
          <input
            type="date"
            value={exportTo}
            onChange={(e) => setExportTo(e.target.value)}
            className="h-8 px-2 text-sm border border-border rounded bg-background"
          />
        </div>
        <div className="flex flex-col">
          <label className="text-xs text-muted-foreground mb-1">动作前缀</label>
          <input
            type="text"
            value={exportAction}
            onChange={(e) => setExportAction(e.target.value)}
            placeholder="如 vm. / node. / http."
            className="h-8 px-2 text-sm border border-border rounded bg-background w-40"
          />
        </div>
        <a
          href={exportHref}
          download
          className="h-8 px-3 text-sm bg-primary text-primary-foreground rounded hover:bg-primary/90 inline-flex items-center"
        >
          导出 CSV
        </a>
        <span className="text-xs text-muted-foreground self-center">默认 30 天，最多 10 万行</span>
      </div>

      {isLoading ? (
        <div className="text-muted-foreground">{t("common.loading")}</div>
      ) : logs.length === 0 ? (
        <div className="border border-border rounded-lg p-6 text-center text-muted-foreground">
          {t("common.noData")}
        </div>
      ) : (
        <>
          <div className="border border-border rounded-lg overflow-x-auto">
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
                    <td className="px-4 py-2 text-xs font-mono">{stripCidrSuffix(log.ip_address)}</td>
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
