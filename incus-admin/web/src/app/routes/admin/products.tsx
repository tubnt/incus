import { createFileRoute } from "@tanstack/react-router";
import { useMutation, useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { http } from "@/shared/lib/http";
import { queryClient } from "@/shared/lib/query-client";
import { useTranslation } from "react-i18next";

export const Route = createFileRoute("/admin/products")({
  component: ProductsPage,
});

interface Product {
  id: number;
  name: string;
  slug: string;
  cpu: number;
  memory_mb: number;
  disk_gb: number;
  bandwidth_tb: number;
  price_monthly: number;
  access: string;
  active: boolean;
  sort_order: number;
}

function ProductsPage() {
  const { t } = useTranslation();
  const [showCreate, setShowCreate] = useState(false);
  const [editingProduct, setEditingProduct] = useState<Product | null>(null);

  const { data, isLoading } = useQuery({
    queryKey: ["adminProducts"],
    queryFn: () => http.get<{ products: Product[] }>("/admin/products"),
  });

  const toggleMutation = useMutation({
    mutationFn: (p: Product) =>
      http.put(`/admin/products/${p.id}`, { ...p, active: !p.active }),
    onSuccess: () =>
      queryClient.invalidateQueries({ queryKey: ["adminProducts"] }),
  });

  const products = data?.products ?? [];

  return (
    <div>
      <div className="flex items-center justify-between mb-6">
        <h1 className="text-2xl font-bold">{t("admin.products.title", "产品套餐")}</h1>
        <button
          onClick={() => {
            setShowCreate(!showCreate);
            setEditingProduct(null);
          }}
          className="px-4 py-2 bg-primary text-primary-foreground rounded-md text-sm font-medium hover:opacity-90"
        >
          {showCreate ? t("common.cancel", "取消") : t("admin.products.add", "+ 添加套餐")}
        </button>
      </div>

      {showCreate && (
        <ProductForm
          onDone={() => setShowCreate(false)}
        />
      )}

      {editingProduct && (
        <ProductForm
          product={editingProduct}
          onDone={() => setEditingProduct(null)}
        />
      )}

      {isLoading ? (
        <div className="text-muted-foreground">{t("common.loading", "加载中...")}</div>
      ) : products.length === 0 ? (
        <div className="border border-border rounded-lg p-6 text-center text-muted-foreground">
          {t("admin.products.empty", "暂无产品套餐。添加后用户可以选择购买。")}
        </div>
      ) : (
        <div className="border border-border rounded-lg overflow-hidden">
          <table className="w-full text-sm">
            <thead className="bg-muted/30">
              <tr>
                <th className="text-left px-4 py-2 font-medium">{t("admin.products.name", "名称")}</th>
                <th className="text-left px-4 py-2 font-medium">{t("admin.products.specs", "配置")}</th>
                <th className="text-right px-4 py-2 font-medium">{t("admin.products.price", "月价")}</th>
                <th className="text-left px-4 py-2 font-medium">{t("admin.products.status", "状态")}</th>
                <th className="text-left px-4 py-2 font-medium">{t("admin.products.access", "访问")}</th>
                <th className="text-right px-4 py-2 font-medium">{t("admin.products.actions", "操作")}</th>
              </tr>
            </thead>
            <tbody>
              {products.map((p) => (
                <tr key={p.id} className="border-t border-border">
                  <td className="px-4 py-2">
                    <div className="font-medium">{p.name}</div>
                    <div className="text-xs text-muted-foreground">{p.slug}</div>
                  </td>
                  <td className="px-4 py-2 text-muted-foreground">
                    {p.cpu}C / {(p.memory_mb / 1024).toFixed(0)}G RAM / {p.disk_gb}G SSD
                    {p.bandwidth_tb > 0 && ` / ${p.bandwidth_tb}TB`}
                  </td>
                  <td className="px-4 py-2 text-right font-mono">
                    ${p.price_monthly.toFixed(2)}
                  </td>
                  <td className="px-4 py-2">
                    <span
                      className={`px-2 py-0.5 rounded text-xs font-medium ${p.active ? "bg-success/20 text-success" : "bg-muted text-muted-foreground"}`}
                    >
                      {p.active
                        ? t("admin.products.active", "上架")
                        : t("admin.products.inactive", "下架")}
                    </span>
                  </td>
                  <td className="px-4 py-2 text-xs text-muted-foreground">{p.access}</td>
                  <td className="px-4 py-2 text-right">
                    <div className="flex items-center justify-end gap-2">
                      <button
                        onClick={() => {
                          setEditingProduct(p);
                          setShowCreate(false);
                        }}
                        className="px-2 py-1 text-xs rounded border border-border hover:bg-muted"
                      >
                        {t("common.edit", "编辑")}
                      </button>
                      <button
                        onClick={() => toggleMutation.mutate(p)}
                        disabled={toggleMutation.isPending}
                        className={`px-2 py-1 text-xs rounded border ${
                          p.active
                            ? "border-destructive/30 text-destructive hover:bg-destructive/10"
                            : "border-success/30 text-success hover:bg-success/10"
                        }`}
                      >
                        {p.active
                          ? t("admin.products.deactivate", "下架")
                          : t("admin.products.activate", "上架")}
                      </button>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

function ProductForm({
  product,
  onDone,
}: {
  product?: Product;
  onDone: () => void;
}) {
  const { t } = useTranslation();
  const isEdit = !!product;

  const [form, setForm] = useState({
    name: product?.name ?? "",
    slug: product?.slug ?? "",
    cpu: product?.cpu ?? 1,
    memory_mb: product?.memory_mb ?? 1024,
    disk_gb: product?.disk_gb ?? 25,
    bandwidth_tb: product?.bandwidth_tb ?? 1,
    price_monthly: product?.price_monthly ?? 0,
    access: product?.access ?? "public",
    active: product?.active ?? true,
    sort_order: product?.sort_order ?? 0,
  });

  const mutation = useMutation({
    mutationFn: () =>
      isEdit
        ? http.put(`/admin/products/${product!.id}`, form)
        : http.post("/admin/products", form),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["adminProducts"] });
      onDone();
    },
  });

  const set = (k: string, v: string | number | boolean) =>
    setForm({ ...form, [k]: v });

  return (
    <div className="border border-border rounded-lg bg-card p-4 mb-6">
      <div className="flex items-center justify-between mb-3">
        <h3 className="font-semibold">
          {isEdit
            ? t("admin.products.editTitle", "编辑产品套餐")
            : t("admin.products.createTitle", "添加产品套餐")}
        </h3>
        <button
          onClick={onDone}
          className="text-xs text-muted-foreground hover:text-foreground"
        >
          {t("common.cancel", "取消")}
        </button>
      </div>
      <div className="grid grid-cols-2 gap-3 mb-4">
        <input
          placeholder={t("admin.products.namePlaceholder", "名称")}
          value={form.name}
          onChange={(e) => set("name", e.target.value)}
          className="px-3 py-2 rounded border border-border bg-card text-sm"
        />
        <input
          placeholder="Slug"
          value={form.slug}
          onChange={(e) => set("slug", e.target.value)}
          className="px-3 py-2 rounded border border-border bg-card text-sm"
        />
        <div className="flex gap-2">
          <input
            type="number"
            placeholder="CPU"
            value={form.cpu}
            onChange={(e) => set("cpu", +e.target.value)}
            className="w-20 px-3 py-2 rounded border border-border bg-card text-sm"
          />
          <input
            type="number"
            placeholder={t("admin.products.memoryMB", "内存MB")}
            value={form.memory_mb}
            onChange={(e) => set("memory_mb", +e.target.value)}
            className="w-28 px-3 py-2 rounded border border-border bg-card text-sm"
          />
          <input
            type="number"
            placeholder={t("admin.products.diskGB", "磁盘GB")}
            value={form.disk_gb}
            onChange={(e) => set("disk_gb", +e.target.value)}
            className="w-28 px-3 py-2 rounded border border-border bg-card text-sm"
          />
        </div>
        <div className="flex gap-2">
          <input
            type="number"
            step="0.01"
            placeholder={t("admin.products.monthlyPrice", "月价")}
            value={form.price_monthly}
            onChange={(e) => set("price_monthly", +e.target.value)}
            className="w-32 px-3 py-2 rounded border border-border bg-card text-sm"
          />
          <input
            type="number"
            placeholder={t("admin.products.bandwidthTB", "带宽TB")}
            value={form.bandwidth_tb}
            onChange={(e) => set("bandwidth_tb", +e.target.value)}
            className="w-28 px-3 py-2 rounded border border-border bg-card text-sm"
          />
          <input
            type="number"
            placeholder={t("admin.products.sortOrder", "排序")}
            value={form.sort_order}
            onChange={(e) => set("sort_order", +e.target.value)}
            className="w-20 px-3 py-2 rounded border border-border bg-card text-sm"
          />
        </div>
      </div>
      {mutation.isError && (
        <div className="text-destructive text-sm mb-2">
          {(mutation.error as Error).message}
        </div>
      )}
      <button
        onClick={() => mutation.mutate()}
        disabled={mutation.isPending || !form.name}
        className="px-4 py-2 bg-primary text-primary-foreground rounded text-sm font-medium disabled:opacity-50"
      >
        {mutation.isPending
          ? t("common.saving", "保存中...")
          : isEdit
            ? t("common.save", "保存")
            : t("admin.products.create", "创建套餐")}
      </button>
    </div>
  );
}
