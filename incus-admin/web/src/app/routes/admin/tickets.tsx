import { createFileRoute } from "@tanstack/react-router";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import {
  type Ticket,
  useAdminTicketsQuery,
  useReplyTicketMutation,
  useTicketDetailQuery,
  useUpdateTicketStatusMutation,
} from "@/features/tickets/api";
import { Pagination } from "@/shared/components/ui/pagination";
import type { PageParams } from "@/shared/lib/pagination";

export const Route = createFileRoute("/admin/tickets")({
  component: AdminTicketsPage,
});

function AdminTicketsPage() {
  const { t } = useTranslation();
  const [selected, setSelected] = useState<number | null>(null);
  const [page, setPage] = useState<PageParams>({ limit: 50, offset: 0 });

  const { data, isLoading } = useAdminTicketsQuery(page);
  const tickets = data?.tickets ?? [];
  const total = data?.total ?? tickets.length;

  return (
    <div>
      <h1 className="text-2xl font-bold mb-6">{t("admin.ticketsTitle", { defaultValue: "工单管理" })}</h1>

      {isLoading ? (
        <div className="text-muted-foreground">{t("common.loading")}</div>
      ) : tickets.length === 0 ? (
        <div className="border border-border rounded-lg p-6 text-center text-muted-foreground">
          {t("admin.ticketsEmpty", { defaultValue: "暂无工单。" })}
        </div>
      ) : (
        <>
        <div className="border border-border rounded-lg overflow-hidden">
          <table className="w-full text-sm">
            <thead className="bg-muted/30">
              <tr>
                <th className="text-left px-4 py-2 font-medium">#</th>
                <th className="text-left px-4 py-2 font-medium">{t("admin.user", { defaultValue: "用户" })}</th>
                <th className="text-left px-4 py-2 font-medium">{t("ticket.subject", { defaultValue: "主题" })}</th>
                <th className="text-left px-4 py-2 font-medium">{t("ticket.status", { defaultValue: "状态" })}</th>
                <th className="text-left px-4 py-2 font-medium">{t("ticket.priority", { defaultValue: "优先级" })}</th>
                <th className="text-left px-4 py-2 font-medium">{t("ticket.updatedAt", { defaultValue: "更新时间" })}</th>
                <th className="text-right px-4 py-2 font-medium">{t("common.actions", { defaultValue: "操作" })}</th>
              </tr>
            </thead>
            <tbody>
              {tickets.map((tk) => (
                <TicketRow
                  key={tk.id}
                  ticket={tk}
                  isOpen={selected === tk.id}
                  onToggle={() => setSelected(selected === tk.id ? null : tk.id)}
                />
              ))}
            </tbody>
          </table>
        </div>
        <Pagination
          total={total}
          limit={page.limit}
          offset={page.offset}
          onChange={(limit, offset) => setPage({ limit, offset })}
          className="mt-3"
        />
        </>
      )}
    </div>
  );
}

function TicketRow({ ticket: tk, isOpen, onToggle }: { ticket: Ticket; isOpen: boolean; onToggle: () => void }) {
  const { t } = useTranslation();
  return (
    <>
      <tr className="border-t border-border hover:bg-muted/20">
        <td className="px-4 py-2">{tk.id}</td>
        <td className="px-4 py-2 text-xs">{tk.user_id}</td>
        <td className="px-4 py-2 font-medium">{tk.subject}</td>
        <td className="px-4 py-2">
          <StatusBadge status={tk.status} />
        </td>
        <td className="px-4 py-2 text-xs">{tk.priority}</td>
        <td className="px-4 py-2 text-muted-foreground text-xs">
          {new Date(tk.updated_at).toLocaleString()}
        </td>
        <td className="px-4 py-2 text-right">
          <button
            onClick={onToggle}
            className="px-2 py-1 text-xs rounded bg-primary/20 text-primary hover:bg-primary/30"
          >
            {isOpen ? t("common.collapse", { defaultValue: "收起" }) : t("common.view", { defaultValue: "查看" })}
          </button>
        </td>
      </tr>
      {isOpen && (
        <tr>
          <td colSpan={7} className="p-0">
            <TicketDetail ticketId={tk.id} status={tk.status} />
          </td>
        </tr>
      )}
    </>
  );
}

function TicketDetail({ ticketId, status }: { ticketId: number; status: string }) {
  const { t } = useTranslation();
  const [reply, setReply] = useState("");

  const { data } = useTicketDetailQuery(ticketId, "/admin");
  const replyMutation = useReplyTicketMutation(ticketId, "/admin");
  const statusMutation = useUpdateTicketStatusMutation(ticketId);

  const messages = data?.messages ?? [];

  return (
    <div className="p-4 bg-card/50 border-t border-border">
      <div className="space-y-3 mb-4 max-h-80 overflow-y-auto">
        {messages.length === 0 && (
          <div className="text-xs text-muted-foreground">{t("ticket.noMessages", { defaultValue: "暂无消息" })}</div>
        )}
        {messages.map((m) => (
          <div key={m.id} className={`p-3 rounded-lg text-sm ${m.is_staff ? "bg-primary/10 ml-8" : "bg-muted/30 mr-8"}`}>
            <div className="flex items-center gap-2 mb-1">
              <span className="text-xs font-medium">
                {m.is_staff ? t("ticket.staff", { defaultValue: "客服" }) : `${t("admin.user", { defaultValue: "用户" })} #${m.user_id}`}
              </span>
              <span className="text-xs text-muted-foreground">{new Date(m.created_at).toLocaleString()}</span>
            </div>
            <div className="whitespace-pre-wrap">{m.body}</div>
          </div>
        ))}
      </div>

      <div className="flex gap-2">
        <textarea
          value={reply}
          onChange={(e) => setReply(e.target.value)}
          placeholder={t("ticket.replyPlaceholder", { defaultValue: "回复..." })}
          rows={2}
          className="flex-1 px-3 py-2 rounded border border-border bg-card text-sm"
        />
        <div className="flex flex-col gap-1">
          <button
            onClick={() =>
              replyMutation.mutate(reply, { onSuccess: () => setReply("") })
            }
            disabled={replyMutation.isPending || !reply.trim()}
            className="px-3 py-1.5 text-xs bg-primary text-primary-foreground rounded disabled:opacity-50"
          >
            {t("ticket.reply", { defaultValue: "回复" })}
          </button>
          {status !== "closed" && (
            <button
              onClick={() => statusMutation.mutate("closed")}
              className="px-3 py-1.5 text-xs bg-muted/50 text-muted-foreground rounded"
            >
              {t("ticket.close", { defaultValue: "关闭" })}
            </button>
          )}
          {status === "closed" && (
            <button
              onClick={() => statusMutation.mutate("open")}
              className="px-3 py-1.5 text-xs bg-success/20 text-success rounded"
            >
              {t("ticket.reopen", { defaultValue: "重开" })}
            </button>
          )}
        </div>
      </div>
    </div>
  );
}

function StatusBadge({ status }: { status: string }) {
  const colors: Record<string, string> = {
    open: "bg-success/20 text-success",
    answered: "bg-primary/20 text-primary",
    closed: "bg-muted text-muted-foreground",
    pending: "bg-warning/20 text-warning",
  };
  return (
    <span className={`px-2 py-0.5 rounded text-xs font-medium ${colors[status] ?? "bg-muted text-muted-foreground"}`}>
      {status}
    </span>
  );
}
