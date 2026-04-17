import { createFileRoute } from "@tanstack/react-router";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import {
  type Product,
  type ProductFormData,
  useAdminProductsQuery,
  useCreateProductMutation,
  useUpdateProductMutation,
} from "@/features/products/api";
import { Pagination } from "@/shared/components/ui/pagination";
import type { PageParams } from "@/shared/lib/pagination";
import { formatCurrency } from "@/shared/lib/utils";

export const Route = createFileRoute("/admin/products")({
  component: ProductsPage,
});

function ProductsPage() {
  const { t } = useTranslation();
  const [showCreate, setShowCreate] = useState(false);
  const [editingProduct, setEditingProduct] = useState<Product | null>(null);
  const [page, setPage] = useState<PageParams>({ limit: 50, offset: 0 });

  const { data, isLoading } = useAdminProductsQuery(page);
  const products = data?.products ?? [];
  const total = data?.total ?? products.length;

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

      {showCreate && <ProductForm onDone={() => setShowCreate(false)} />}

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
        <>
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
                <ProductRow
                  key={p.id}
                  product={p}
                  onEdit={() => {
                    setEditingProduct(p);
                    setShowCreate(false);
                  }}
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

function ProductRow({ product, onEdit }: { product: Product; onEdit: () => void }) {
  const { t } = useTranslation();
  const toggleMutation = useUpdateProductMutation(product.id);

  return (
    <tr className="border-t border-border">
      <td className="px-4 py-2">
        <div className="font-medium">{product.name}</div>
        <div className="text-xs text-muted-foreground">{product.slug}</div>
      </td>
      <td className="px-4 py-2 text-muted-foreground">
        {product.cpu}C / {(product.memory_mb / 1024).toFixed(0)}G RAM / {product.disk_gb}G SSD
        {product.bandwidth_tb > 0 && ` / ${product.bandwidth_tb}TB`}
      </td>
      <td className="px-4 py-2 text-right font-mono">{formatCurrency(product.price_monthly, product.currency)}</td>
      <td className="px-4 py-2">
        <span
          className={`px-2 py-0.5 rounded text-xs font-medium ${product.active ? "bg-success/20 text-success" : "bg-muted text-muted-foreground"}`}
        >
          {product.active
            ? t("admin.products.active", "上架")
            : t("admin.products.inactive", "下架")}
        </span>
      </td>
      <td className="px-4 py-2 text-xs text-muted-foreground">{product.access}</td>
      <td className="px-4 py-2 text-right">
        <div className="flex items-center justify-end gap-2">
          <button
            onClick={onEdit}
            className="px-2 py-1 text-xs rounded border border-border hover:bg-muted"
          >
            {t("common.edit", "编辑")}
          </button>
          <button
            onClick={() => toggleMutation.mutate({ active: !product.active })}
            disabled={toggleMutation.isPending}
            className={`px-2 py-1 text-xs rounded border ${
              product.active
                ? "border-destructive/30 text-destructive hover:bg-destructive/10"
                : "border-success/30 text-success hover:bg-success/10"
            }`}
          >
            {product.active
              ? t("admin.products.deactivate", "下架")
              : t("admin.products.activate", "上架")}
          </button>
        </div>
      </td>
    </tr>
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

  const [form, setForm] = useState<ProductFormData>({
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

  const createMutation = useCreateProductMutation();
  const updateMutation = useUpdateProductMutation(product?.id ?? 0);
  const mutation = isEdit ? updateMutation : createMutation;

  const onSubmit = () => mutation.mutate(form, { onSuccess: onDone });

  const set = (k: keyof ProductFormData, v: string | number | boolean) =>
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
        onClick={onSubmit}
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
