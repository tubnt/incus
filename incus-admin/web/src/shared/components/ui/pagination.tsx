import { useTranslation } from "react-i18next";
import { cn } from "@/shared/lib/utils";

export interface PaginationProps {
  total: number;
  limit: number;
  offset: number;
  onChange: (limit: number, offset: number) => void;
  className?: string;
  pageSizeOptions?: number[];
}

export function Pagination({
  total,
  limit,
  offset,
  onChange,
  className,
  pageSizeOptions = [20, 50, 100],
}: PaginationProps) {
  const { t } = useTranslation();
  const pageSize = limit > 0 ? limit : 50;
  const currentPage = Math.floor(offset / pageSize) + 1;
  const totalPages = Math.max(1, Math.ceil(total / pageSize));
  const from = total === 0 ? 0 : offset + 1;
  const to = Math.min(total, offset + pageSize);
  const disabledPrev = offset <= 0;
  const disabledNext = offset + pageSize >= total;

  return (
    <div className={cn("flex items-center justify-between gap-3 text-xs", className)}>
      <span className="text-muted-foreground">
        {t("pagination.range", {
          defaultValue: "{{from}}-{{to}} / {{total}}",
          from,
          to,
          total,
        })}
      </span>
      <div className="flex items-center gap-2">
        <label className="flex items-center gap-1 text-muted-foreground">
          {t("pagination.pageSize", { defaultValue: "每页" })}
          <select
            value={pageSize}
            onChange={(e) => onChange(Number(e.target.value), 0)}
            className="px-1.5 py-0.5 rounded border border-border bg-card"
          >
            {pageSizeOptions.map((n) => (
              <option key={n} value={n}>
                {n}
              </option>
            ))}
          </select>
        </label>
        <button
          type="button"
          onClick={() => onChange(pageSize, Math.max(0, offset - pageSize))}
          disabled={disabledPrev}
          className="px-2 py-1 rounded border border-border hover:bg-muted disabled:opacity-50 disabled:cursor-not-allowed"
        >
          {t("pagination.prev", { defaultValue: "上一页" })}
        </button>
        <span className="text-muted-foreground">
          {t("pagination.pageOf", {
            defaultValue: "{{page}}/{{total}}",
            page: currentPage,
            total: totalPages,
          })}
        </span>
        <button
          type="button"
          onClick={() => onChange(pageSize, offset + pageSize)}
          disabled={disabledNext}
          className="px-2 py-1 rounded border border-border hover:bg-muted disabled:opacity-50 disabled:cursor-not-allowed"
        >
          {t("pagination.next", { defaultValue: "下一页" })}
        </button>
      </div>
    </div>
  );
}
