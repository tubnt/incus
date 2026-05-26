import type {APIToken} from "@/features/api-tokens/api";
import { createFileRoute } from "@tanstack/react-router";
import { Cloud, KeyRound, Plus, RefreshCw, Trash2 } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import {
  useAPITokensQuery,
  useCreateAPITokenMutation,
  useDeleteAPITokenMutation,
  useRenewAPITokenMutation,
} from "@/features/api-tokens/api";
import {
  PageContent,
  PageHeader,
  PageShell,
} from "@/shared/components/page/page-shell";
import { Button } from "@/shared/components/ui/button";
import { Card, CardContent } from "@/shared/components/ui/card";
import { useConfirm } from "@/shared/components/ui/confirm-dialog";
import { EmptyState } from "@/shared/components/ui/empty-state";
import { Input } from "@/shared/components/ui/input";
import { Label } from "@/shared/components/ui/label";
import { SecretReveal } from "@/shared/components/ui/secret-reveal";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/shared/components/ui/select";
import {
  Sheet,
  SheetBody,
  SheetContent,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from "@/shared/components/ui/sheet";
import { Skeleton } from "@/shared/components/ui/skeleton";
import { formatError } from "@/shared/lib/http";
import { cn, formatDate, formatDateTime } from "@/shared/lib/utils";

// OPS-030: TTL 选项 + 倒计时文案改成 i18n 化（接 t fn）。
function ttlOptions(t: (k: string, opts?: Record<string, unknown>) => string): Array<{ hours: number; label: string }> {
  return [
    { hours: 1, label: t("apiToken.ttl.1h", { defaultValue: "1 hour" }) },
    { hours: 6, label: t("apiToken.ttl.6h", { defaultValue: "6 hours" }) },
    { hours: 24, label: t("apiToken.ttl.24hDefault", { defaultValue: "24 hours (default)" }) },
    { hours: 168, label: t("apiToken.ttl.7d", { defaultValue: "7 days" }) },
    { hours: 720, label: t("apiToken.ttl.30d", { defaultValue: "30 days" }) },
    { hours: 2160, label: t("apiToken.ttl.90dMax", { defaultValue: "90 days (max)" }) },
  ];
}

function formatTimeLeft(
  expiresAt: string | null,
  t: (k: string, opts?: Record<string, unknown>) => string,
): { text: string; tone: "normal" | "warn" | "expired" } {
  if (!expiresAt) return { text: t("apiToken.neverExpires", { defaultValue: "Never expires" }), tone: "normal" };
  const ms = new Date(expiresAt).getTime() - Date.now();
  if (ms <= 0) return { text: t("apiToken.expired", { defaultValue: "Expired" }), tone: "expired" };
  const hours = Math.floor(ms / 3_600_000);
  const days = Math.floor(hours / 24);
  let text: string;
  if (days >= 1) text = t("apiToken.expiresInDays", { defaultValue: "Expires in {{n}} days", n: days });
  else if (hours >= 1) text = t("apiToken.expiresInHours", { defaultValue: "Expires in {{n}} hours", n: hours });
  else text = t("apiToken.expiresInMinutes", { defaultValue: "Expires in {{n}} min", n: Math.max(1, Math.floor(ms / 60_000)) });
  const tone = hours < 1 ? "warn" : "normal";
  return { text, tone };
}

export const Route = createFileRoute("/api-tokens")({
  component: APITokensPage,
});

function APITokensPage() {
  const { t } = useTranslation();
  const [createOpen, setCreateOpen] = useState(false);
  const [revealedToken, setRevealedToken] = useState<string | null>(null);

  const { data, isLoading } = useAPITokensQuery();
  const tokens = data?.tokens ?? [];

  return (
    <PageShell>
      <PageHeader
        title={t("apiToken.title", { defaultValue: "API Tokens" })}
        description={t("apiToken.description", {
          defaultValue: "用于程序化访问 API 的令牌，最长 90 天有效期。",
        })}
        actions={
          <Button variant="primary" onClick={() => setCreateOpen(true)}>
            <Plus size={14} aria-hidden="true" />
            {t("apiToken.create", { defaultValue: "创建 Token" })}
          </Button>
        }
      />
      <PageContent>
        <CloudGatewayBanner />

        {revealedToken ? (
          <Card className="border-status-success/30 bg-status-success/8">
            <CardContent className="p-4 space-y-3">
              <div className="font-emphasis text-sm">
                {t("apiToken.createdSaveNow", {
                  defaultValue: "Token 创建成功！请立即保存，此后不再显示。",
                })}
              </div>
              <SecretReveal value={revealedToken} inline={false} autoMaskMs={0} />
              <div className="flex justify-end">
                <Button variant="ghost" onClick={() => setRevealedToken(null)}>
                  {t("common.ok", { defaultValue: "好的" })}
                </Button>
              </div>
            </CardContent>
          </Card>
        ) : null}

        {isLoading ? (
          <div className="space-y-2">
            {Array.from({ length: 3 }).map((_, i) => (
              // eslint-disable-next-line react/no-array-index-key -- skeleton 占位
              <Skeleton key={i} className="h-20 w-full" />
            ))}
          </div>
        ) : tokens.length === 0 ? (
          <EmptyState
            icon={KeyRound}
            title={t("apiToken.emptyTitle", { defaultValue: "暂无 API Token" })}
            description={t("apiToken.empty", {
              defaultValue: "创建后可用于程序化访问 API。",
            })}
            action={
              <Button variant="primary" onClick={() => setCreateOpen(true)}>
                <Plus size={14} aria-hidden="true" />
                {t("apiToken.create", { defaultValue: "创建 Token" })}
              </Button>
            }
          />
        ) : (
          <div className="space-y-2">
            {tokens.map((tk) => (
              <TokenCard key={tk.id} token={tk} onRenewed={(raw) => setRevealedToken(raw)} />
            ))}
          </div>
        )}
      </PageContent>
      <CreateTokenSheet
        open={createOpen}
        onClose={() => setCreateOpen(false)}
        onCreated={(raw) => {
          setRevealedToken(raw);
          setCreateOpen(false);
        }}
      />
    </PageShell>
  );
}

function CreateTokenSheet({
  open,
  onClose,
  onCreated,
}: {
  open: boolean;
  onClose: () => void;
  onCreated: (raw: string) => void;
}) {
  const { t } = useTranslation();
  const [name, setName] = useState("");
  const [ttlHours, setTtlHours] = useState("24");
  const mutation = useCreateAPITokenMutation();

  const submit = () => {
    mutation.mutate(
      { name, expiresInHours: Number(ttlHours) },
      {
        onSuccess: (data) => {
          if (data.token.token) {
            onCreated(data.token.token);
            setName("");
            setTtlHours("24");
          }
        },
      },
    );
  };

  return (
    <Sheet open={open} onOpenChange={(o) => { if (!o) onClose(); }}>
      <SheetContent side="right" size="min(96vw, 28rem)">
        <SheetHeader>
          <SheetTitle>
            {t("apiToken.createTitle", { defaultValue: "创建 API Token" })}
          </SheetTitle>
        </SheetHeader>
        <SheetBody className="space-y-4">
          <div className="space-y-1.5">
            <Label htmlFor="token-name">{t("apiToken.nameLabel", { defaultValue: "名称" })}</Label>
            <Input
              id="token-name"
              type="text"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder={t("apiToken.namePlaceholder", { defaultValue: "如 ci-deploy" })}
            />
          </div>
          <div className="space-y-1.5">
            <Label>{t("apiToken.ttlLabel", { defaultValue: "有效期" })}</Label>
            <Select value={ttlHours} onValueChange={(v) => setTtlHours(String(v))}>
              <SelectTrigger>
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {ttlOptions(t).map((o) => (
                  <SelectItem key={o.hours} value={String(o.hours)}>
                    {o.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          {mutation.isError ? (
            <div className="text-status-error text-sm">
              {formatError(mutation.error)}
            </div>
          ) : null}
        </SheetBody>
        <SheetFooter>
          <Button variant="ghost" onClick={onClose}>
            {t("common.cancel")}
          </Button>
          <Button variant="primary" disabled={mutation.isPending} onClick={submit}>
            {mutation.isPending
              ? t("apiToken.submitting", { defaultValue: "创建中..." })
              : t("apiToken.submit", { defaultValue: "创建" })}
          </Button>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  );
}

function TokenCard({
  token,
  onRenewed,
}: {
  token: APIToken;
  onRenewed: (raw: string) => void;
}) {
  const { t } = useTranslation();
  const confirm = useConfirm();
  const deleteMutation = useDeleteAPITokenMutation();
  const renewMutation = useRenewAPITokenMutation();
  const [renewTtl, setRenewTtl] = useState("24");

  const expiry = formatTimeLeft(token.expires_at, t);
  const expiryColor =
    expiry.tone === "expired"
      ? "text-status-error"
      : expiry.tone === "warn"
        ? "text-status-warning"
        : "text-text-tertiary";

  const onDelete = async () => {
    const ok = await confirm({
      title: t("apiToken.deleteTitle"),
      message: t("apiToken.deleteMessage", { name: token.name }),
      destructive: true,
    });
    if (ok) deleteMutation.mutate(token.id);
  };

  return (
    <Card>
      <CardContent className="p-4 flex flex-wrap items-center justify-between gap-3">
        <div className="min-w-0 flex-1">
          <div className="font-emphasis truncate">{token.name}</div>
          <div className="text-caption text-text-tertiary mt-1">
            {t("apiToken.createdAt", { defaultValue: "创建于" })}{" "}
            {formatDate(token.created_at)}
            {token.last_used_at
              ? ` · ${t("apiToken.lastUsedAt", { defaultValue: "最后使用" })} ${formatDateTime(token.last_used_at)}`
              : null}
          </div>
          <div className={cn("text-caption mt-1", expiryColor)}>{expiry.text}</div>
        </div>
        <div className="flex items-center gap-2 shrink-0">
          <Select value={renewTtl} onValueChange={(v) => setRenewTtl(String(v))}>
            <SelectTrigger
              className="h-7 text-xs w-input-narrow"
              aria-label={t("apiToken.renewTtl", { defaultValue: "续签有效期" })}
            >
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {ttlOptions(t).map((o) => (
                <SelectItem key={o.hours} value={String(o.hours)}>
                  {o.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <Button
            size="sm"
            variant="primary"
            disabled={renewMutation.isPending || expiry.tone === "expired"}
            onClick={() =>
              renewMutation.mutate(
                { id: token.id, expiresInHours: Number(renewTtl) },
                {
                  onSuccess: (data) => {
                    if (data.token.token) onRenewed(data.token.token);
                  },
                },
              )
            }
          >
            <RefreshCw size={12} aria-hidden="true" />
            {renewMutation.isPending
              ? t("apiToken.renewing", { defaultValue: "续签中..." })
              : t("apiToken.renew", { defaultValue: "续签" })}
          </Button>
          <Button
            size="sm"
            variant="destructive"
            disabled={deleteMutation.isPending}
            aria-label={`Delete API token ${token.name}`}
            data-testid={`delete-api-token-${token.id}`}
            onClick={onDelete}
          >
            <Trash2 size={12} aria-hidden="true" />
            {t("common.delete")}
          </Button>
        </div>
      </CardContent>
    </Card>
  );
}

// CloudGatewayBanner —— PLAN-053 / INFRA-012 提示用户 API token 可直接接 cloud-gateway。
// 不动现有 CRUD，纯增量；建议 TTL ≥ 7 天，与 cloud-gateway 长跑场景对齐。
function CloudGatewayBanner() {
  const { t } = useTranslation();
  return (
    <Card className="border-accent/30 bg-accent/8">
      <CardContent className="p-4 flex items-start gap-3">
        <Cloud
          size={18}
          aria-hidden="true"
          className="text-accent shrink-0 mt-0.5"
        />
        <div className="flex-1 space-y-1">
          <div className="font-emphasis text-sm">
            {t("apiToken.cloudGatewayTitle", {
              defaultValue: "AI 网关（cloud-gateway）",
            })}
          </div>
          <div className="text-caption text-text-secondary">
            {t("apiToken.cloudGatewayDescription", {
              defaultValue:
                "用 API token 调 /v1/* 接 incus-admin，按周期付费、可弹性挂起。建议 TTL ≥ 7 天，避免长跑任务断连。",
            })}
          </div>
          <a
            href="#"
            target="_blank"
            rel="noreferrer"
            className="inline-flex items-center gap-1 text-caption font-emphasis text-accent hover:text-accent-hover"
          >
            {t("apiToken.cloudGatewayDocsLink", {
              defaultValue: "查看 cloud-gateway 文档 →",
            })}
          </a>
        </div>
      </CardContent>
    </Card>
  );
}
