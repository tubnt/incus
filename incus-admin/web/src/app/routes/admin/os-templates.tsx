import type { OSTemplate, OSTemplateFormData } from "@/features/templates/api";
import { createFileRoute } from "@tanstack/react-router";
import { Plus } from "lucide-react";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import {
  useAdminOSTemplatesQuery,
  useCreateOSTemplateMutation,
  useDeleteOSTemplateMutation,
  useUpdateOSTemplateMutation,
} from "@/features/templates/api";
import {
  PageContent,
  PageHeader,
  PageShell,
} from "@/shared/components/page/page-shell";
import { Alert, AlertDescription } from "@/shared/components/ui/alert";
import { Button } from "@/shared/components/ui/button";
import { Card } from "@/shared/components/ui/card";
import { useConfirm } from "@/shared/components/ui/confirm-dialog";
import { EmptyState } from "@/shared/components/ui/empty-state";
import { Input, Textarea } from "@/shared/components/ui/input";
import { Label } from "@/shared/components/ui/label";
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
import { StatusPill } from "@/shared/components/ui/status";
import { Switch } from "@/shared/components/ui/switch";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/shared/components/ui/table";
import { formatError } from "@/shared/lib/http";

export const Route = createFileRoute("/admin/os-templates")({
  component: OSTemplatesPage,
});

const EMPTY_FORM: OSTemplateFormData = {
  slug: "",
  name: "",
  source: "",
  protocol: "simplestreams",
  server_url: "https://images.linuxcontainers.org",
  default_user: "root",
  cloud_init_template: "",
  supports_rescue: false,
  enabled: true,
  sort_order: 100,
};

function OSTemplatesPage() {
  const { t } = useTranslation();
  const [createOpen, setCreateOpen] = useState(false);
  const [editing, setEditing] = useState<OSTemplate | null>(null);

  const { data, isLoading } = useAdminOSTemplatesQuery();
  const templates = data?.templates ?? [];

  const sheetOpen = createOpen || editing !== null;

  return (
    <PageShell>
      <PageHeader
        title={t("admin.osTemplates.title", "OS 镜像模板")}
        description={t(
          "admin.osTemplates.hint",
          "这里维护用户在创建 / 重装 VM 时可选的 OS 镜像。新增镜像无需改代码。",
        )}
        actions={
          <Button
            variant="primary"
            onClick={() => {
              setEditing(null);
              setCreateOpen(true);
            }}
          >
            <Plus size={14} aria-hidden="true" />
            {t("admin.osTemplates.add", "+ 添加模板")}
          </Button>
        }
      />
      <PageContent>
        {isLoading ? (
          <Skeleton className="h-32 w-full" />
        ) : templates.length === 0 ? (
          <EmptyState
            title={t("admin.osTemplates.empty", "暂无模板。点击右上角添加。")}
          />
        ) : (
          <Card className="overflow-x-auto">
            <Table>
              <TableHeader>
                <TableRow className="hover:bg-transparent">
                  <TableHead>{t("admin.osTemplates.name", "名称")}</TableHead>
                  <TableHead>Slug</TableHead>
                  <TableHead>
                    {t("admin.osTemplates.source", "镜像源")}
                  </TableHead>
                  <TableHead>
                    {t("admin.osTemplates.defaultUser", "默认用户")}
                  </TableHead>
                  <TableHead className="text-right">
                    {t("admin.osTemplates.sortOrder", "排序")}
                  </TableHead>
                  <TableHead>
                    {t("admin.osTemplates.status", "状态")}
                  </TableHead>
                  <TableHead className="text-right">
                    {t("admin.osTemplates.actions", "操作")}
                  </TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {templates.map((tpl) => (
                  <TemplateRow
                    key={tpl.id}
                    template={tpl}
                    onEdit={() => {
                      setCreateOpen(false);
                      setEditing(tpl);
                    }}
                  />
                ))}
              </TableBody>
            </Table>
          </Card>
        )}

        <Sheet
          open={sheetOpen}
          onOpenChange={(o) => {
            if (!o) {
              setCreateOpen(false);
              setEditing(null);
            }
          }}
        >
          <SheetContent side="right" size="min(96vw, 36rem)">
            {sheetOpen ? (
              <TemplateForm
                template={editing ?? undefined}
                onDone={() => {
                  setCreateOpen(false);
                  setEditing(null);
                }}
              />
            ) : null}
          </SheetContent>
        </Sheet>
      </PageContent>
    </PageShell>
  );
}

function TemplateRow({
  template,
  onEdit,
}: {
  template: OSTemplate;
  onEdit: () => void;
}) {
  const { t } = useTranslation();
  const confirm = useConfirm();
  const toggleMutation = useUpdateOSTemplateMutation(template.id);
  const deleteMutation = useDeleteOSTemplateMutation(template.id);

  const onDelete = async () => {
    const ok = await confirm({
      title: t("admin.osTemplates.deleteTitle", "删除模板"),
      message: t("admin.osTemplates.deleteMessage", {
        defaultValue: `确认删除模板「${template.name}」?`,
        name: template.name,
      }),
      destructive: true,
    });
    if (!ok) return;
    deleteMutation.mutate(undefined, {
      onSuccess: () =>
        toast.success(t("admin.osTemplates.deleted", "已删除")),
      onError: (err) => toast.error(formatError(err)),
    });
  };

  return (
    <TableRow>
      <TableCell className="font-emphasis">{template.name}</TableCell>
      <TableCell className="font-mono text-xs text-muted-foreground">
        {template.slug}
      </TableCell>
      <TableCell className="font-mono text-xs">{template.source}</TableCell>
      <TableCell>{template.default_user}</TableCell>
      <TableCell className="text-right font-mono">
        {template.sort_order}
      </TableCell>
      <TableCell>
        <StatusPill status={template.enabled ? "success" : "disabled"}>
          {template.enabled
            ? t("admin.osTemplates.enabled", "启用")
            : t("admin.osTemplates.disabled", "停用")}
        </StatusPill>
      </TableCell>
      <TableCell className="text-right">
        <div className="flex items-center justify-end gap-2">
          <Button variant="ghost" size="sm" onClick={onEdit}>
            {t("common.edit", "编辑")}
          </Button>
          <Button
            variant={template.enabled ? "destructive" : "ghost"}
            size="sm"
            onClick={() =>
              toggleMutation.mutate(
                { enabled: !template.enabled },
                { onError: (err) => toast.error(formatError(err)) },
              )
            }
            disabled={toggleMutation.isPending}
            aria-label={
              template.enabled
                ? `Disable OS template ${template.slug}`
                : `Enable OS template ${template.slug}`
            }
            data-testid={
              template.enabled
                ? `disable-os-template-${template.slug}`
                : `enable-os-template-${template.slug}`
            }
          >
            {template.enabled
              ? t("admin.osTemplates.disable", "停用")
              : t("admin.osTemplates.enable", "启用")}
          </Button>
          <Button
            variant="destructive"
            size="sm"
            onClick={onDelete}
            disabled={deleteMutation.isPending}
            aria-label={`Delete OS template ${template.slug}`}
            data-testid={`delete-os-template-${template.slug}`}
          >
            {t("common.delete", "删除")}
          </Button>
        </div>
      </TableCell>
    </TableRow>
  );
}

function TemplateForm({
  template,
  onDone,
}: {
  template?: OSTemplate;
  onDone: () => void;
}) {
  const { t } = useTranslation();
  const isEdit = !!template;

  const [form, setForm] = useState<OSTemplateFormData>(
    template
      ? {
          slug: template.slug,
          name: template.name,
          source: template.source,
          protocol: template.protocol,
          server_url: template.server_url,
          default_user: template.default_user,
          cloud_init_template: template.cloud_init_template,
          supports_rescue: template.supports_rescue,
          enabled: template.enabled,
          sort_order: template.sort_order,
        }
      : EMPTY_FORM,
  );

  const createMutation = useCreateOSTemplateMutation();
  const updateMutation = useUpdateOSTemplateMutation(template?.id ?? 0);
  const mutation = isEdit ? updateMutation : createMutation;

  const onSubmit = () => {
    mutation.mutate(form, {
      onSuccess: () => {
        toast.success(
          isEdit
            ? t("admin.osTemplates.updated", "已保存")
            : t("admin.osTemplates.created", "已创建"),
        );
        onDone();
      },
      onError: (err) => toast.error(formatError(err)),
    });
  };

  const set = <K extends keyof OSTemplateFormData>(
    k: K,
    v: OSTemplateFormData[K],
  ) => setForm({ ...form, [k]: v });

  return (
    <>
      <SheetHeader>
        <SheetTitle>
          {isEdit
            ? t("admin.osTemplates.editTitle", "编辑模板")
            : t("admin.osTemplates.createTitle", "添加模板")}
        </SheetTitle>
      </SheetHeader>
      <SheetBody>
        <div className="grid grid-cols-2 gap-3">
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="ostpl-name">
              {t("admin.osTemplates.name", "名称")}
            </Label>
            <Input
              id="ostpl-name"
              value={form.name}
              maxLength={64}
              onChange={(e) => set("name", e.target.value)}
              placeholder="Ubuntu 24.04 LTS"
            />
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="ostpl-slug">Slug</Label>
            <Input
              id="ostpl-slug"
              value={form.slug}
              maxLength={32}
              pattern="[a-z0-9-]+"
              onChange={(e) => set("slug", e.target.value)}
              placeholder="ubuntu-24-04"
              className="font-mono"
            />
          </div>
          <div className="flex flex-col gap-1.5 col-span-2">
            <Label htmlFor="ostpl-source">
              {t(
                "admin.osTemplates.source",
                "镜像源 (images:… 不含前缀)",
              )}
            </Label>
            <Input
              id="ostpl-source"
              value={form.source}
              maxLength={128}
              onChange={(e) => set("source", e.target.value)}
              placeholder="ubuntu/24.04/cloud"
              className="font-mono"
            />
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="ostpl-user">
              {t("admin.osTemplates.defaultUser", "默认用户")}
            </Label>
            <Input
              id="ostpl-user"
              value={form.default_user}
              onChange={(e) => set("default_user", e.target.value)}
              placeholder="root"
            />
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="ostpl-protocol">
              {t("admin.osTemplates.protocol", "协议")}
            </Label>
            <Select
              value={form.protocol}
              onValueChange={(v) => set("protocol", String(v ?? "simplestreams"))}
            >
              <SelectTrigger id="ostpl-protocol">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="simplestreams">simplestreams</SelectItem>
                <SelectItem value="incus">incus</SelectItem>
              </SelectContent>
            </Select>
          </div>
          <div className="flex flex-col gap-1.5 col-span-2">
            <Label htmlFor="ostpl-server">
              {t("admin.osTemplates.serverUrl", "镜像服务器")}
            </Label>
            <Input
              id="ostpl-server"
              value={form.server_url}
              onChange={(e) => set("server_url", e.target.value)}
              className="font-mono"
            />
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="ostpl-sort">
              {t("admin.osTemplates.sortOrder", "排序")}
            </Label>
            <Input
              id="ostpl-sort"
              type="number"
              value={form.sort_order}
              onChange={(e) => set("sort_order", +e.target.value)}
            />
          </div>
          <div className="flex flex-col gap-1.5 col-span-2">
            <Label htmlFor="ostpl-cloud-init">
              {t(
                "admin.osTemplates.cloudInitTemplate",
                "高级 cloud-init 注入（可选）",
              )}
            </Label>
            <Textarea
              id="ostpl-cloud-init"
              value={form.cloud_init_template}
              onChange={(e) =>
                set("cloud_init_template", e.target.value)
              }
              placeholder={
                "# 可选：粘贴额外 cloud-config 字段（如 packages / write_files / runcmd）\n# 系统已强制 openssh-server + root + sshd_config，请勿覆盖\n# 例：\n# packages:\n#   - htop\n# write_files:\n#   - path: /etc/motd\n#     content: hello"
              }
              rows={6}
              className="font-mono text-caption"
              maxLength={8192}
            />
            <span className="text-caption text-text-tertiary">
              {t(
                "admin.osTemplates.cloudInitHint",
                "禁止 disable_root / ssh_pwauth: false。其它字段追加合并到系统模板。预留 AI 生成接入位。",
              )}
            </span>
          </div>
          <div className="flex items-center gap-4 col-span-2 pt-1">
            <div className="flex items-center gap-2">
              <Switch
                id="ostpl-enabled"
                checked={form.enabled}
                onCheckedChange={(checked) => set("enabled", checked)}
              />
              <Label htmlFor="ostpl-enabled">
                {t("admin.osTemplates.enabled", "启用")}
              </Label>
            </div>
            <div className="flex items-center gap-2">
              <Switch
                id="ostpl-rescue"
                checked={form.supports_rescue}
                onCheckedChange={(checked) => set("supports_rescue", checked)}
              />
              <Label htmlFor="ostpl-rescue">
                {t("admin.osTemplates.supportsRescue", "支持救援模式")}
              </Label>
            </div>
          </div>
        </div>

        {mutation.isError ? (
          <Alert variant="error" className="mt-3">
            <AlertDescription>
              {formatError(mutation.error)}
            </AlertDescription>
          </Alert>
        ) : null}
      </SheetBody>
      <SheetFooter>
        <Button variant="ghost" onClick={onDone}>
          {t("common.cancel", "取消")}
        </Button>
        <Button
          variant="primary"
          onClick={onSubmit}
          disabled={
            mutation.isPending || !form.name || !form.slug || !form.source
          }
        >
          {mutation.isPending
            ? t("common.saving", "保存中...")
            : isEdit
              ? t("common.save", "保存")
              : t("admin.osTemplates.create", "创建模板")}
        </Button>
      </SheetFooter>
    </>
  );
}
