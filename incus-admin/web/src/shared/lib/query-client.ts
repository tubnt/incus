import { MutationCache, QueryClient } from "@tanstack/react-query";
import { toast } from "sonner";
import i18n from "@/app/i18n";
import { formatError } from "@/shared/lib/http";

/**
 * mutation meta 约定（WP-F）：
 *
 * - `globalErrorToast`：该 mutation 的调用处未自行处理错误（既无 `mutate(_, { onError })`
 *   也无 `useMutation({ onError })`），交给全局 `MutationCache.onError` 兜底弹一条错误 toast，
 *   避免失败被静默吞掉。调用处已经自行 toast 的 mutation *不要* 打这个标记，否则会双提示。
 * - `successToast`：成功时由全局 `MutationCache.onSuccess` 弹一条 i18n toast（值为 key）。
 *   用于调用处在本工作包 scope 之外（如 src/app/routes/* 路由文件）、无法就地补 toast 的场景。
 */
declare module "@tanstack/react-query" {
  interface Register {
    mutationMeta: {
      globalErrorToast?: boolean;
      successToast?: string;
    };
  }
}

/**
 * 全局 mutation 兜底：
 *
 * - onError：始终 `console.error` 记录（便于线上排查），并对声明了 `meta.globalErrorToast`
 *   的 mutation 弹一条统一错误 toast。绝大多数 mutation 在调用处已自行 toast（call-time
 *   `onError`，无法从这里检测），因此采用「显式声明才兜底」而非「默认全弹」，以杜绝双提示。
 * - onSuccess：对声明了 `meta.successToast` 的 mutation 弹一条成功 toast（带稳定 id 去重，
 *   批量场景下多次相同成功会折叠为一条）。
 */
const mutationCache = new MutationCache({
  onSuccess: (_data, _vars, _ctx, mutation) => {
    const key = mutation.meta?.successToast;
    if (key) toast.success(i18n.t(key), { id: `mut-ok-${key}` });
  },
  onError: (error, _vars, _ctx, mutation) => {
    console.error("[mutation]", mutation.options.mutationKey ?? mutation.mutationId, error);
    if (!mutation.meta?.globalErrorToast) return;
    toast.error(`${i18n.t("error.mutationFailed", { defaultValue: "操作失败" })}: ${formatError(error)}`);
  },
});

/**
 * 全局 QueryClient 默认值。
 *
 * D1: `refetchIntervalInBackground: false` —— hidden tab 时停止周期 fetch，
 *      避免后台大量无人看的请求消耗带宽和后端资源。
 * Session-3 §1🟡-3 / §1🔵-10：默认 `refetchOnWindowFocus: true`（受 staleTime
 *      gate 保护），避免每次切 tab 一次性 invalidate 14+ 条 polling query 撞主线程。
 *      若某个 query 必须切 tab 即刷新，单独写 `refetchOnWindowFocus: "always"`。
 */
export const queryClient = new QueryClient({
  mutationCache,
  defaultOptions: {
    queries: {
      staleTime: 30_000,
      retry: 1,
      refetchOnWindowFocus: true,
      refetchIntervalInBackground: false,
    },
  },
});
