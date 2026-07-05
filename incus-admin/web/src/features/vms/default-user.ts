// OPS-051 / PLAN-052 Q7：所有 Linux 镜像统一 root 登录。Windows 仍 Administrator。
// 后端 os_templates.default_user 已 migrate root；前端 helper 与之对齐，避免
// 用户在 UI 上看到 ubuntu/debian 等历史用户名误以为是登录账号。
export function defaultUserForImage(image: string | undefined | null): string {
  const v = (image ?? "").trim().toLowerCase();
  if (v.includes("windows")) return "Administrator";
  return "root";
}
