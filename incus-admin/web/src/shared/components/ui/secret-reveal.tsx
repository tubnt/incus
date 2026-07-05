import { Check, Copy, Eye, EyeOff } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { cn } from "@/shared/lib/utils";

/**
 * SecretReveal —— B2 凭据安全显示。
 * 默认遮罩；点击 Eye 显示，8s 后自动复盖；卸载时清除引用。
 * 用于 admin 创建/重装 VM 后展示密码、Shadow Login redirect URL 等一次性敏感信息。
 */
interface SecretRevealProps {
  value: string;
  /** 字段标签（如 "Password"） */
  label?: string;
  /** 是否单行显示（false 则用 mono 块状） */
  inline?: boolean;
  className?: string;
  /** 自动复盖延时（ms，默认 8000；传 0 关闭） */
  autoMaskMs?: number;
  /** OPS-051 / PLAN-052 Q7：DonePanel 凭据明文显示模式 —— 始终展示明文 +
   * 一键复制，省略 Eye 按钮（用户决策：私有云场景肩窥风险低于"复制不上"
   * 的功能挫败）。其它一次性 secret（如 shadow login URL）保持默认 mask. */
  alwaysReveal?: boolean;
}

export function SecretReveal({
  value,
  label,
  inline = true,
  className,
  autoMaskMs = 8000,
  alwaysReveal = false,
}: SecretRevealProps) {
  const { t } = useTranslation();
  const [revealed, setRevealed] = useState(alwaysReveal);
  const [copied, setCopied] = useState(false);
  const maskTimerRef = useRef<number | null>(null);

  useEffect(() => {
    return () => {
      if (maskTimerRef.current) {
        window.clearTimeout(maskTimerRef.current);
      }
    };
  }, []);

  const reveal = () => {
    setRevealed(true);
    if (autoMaskMs > 0) {
      if (maskTimerRef.current) window.clearTimeout(maskTimerRef.current);
      maskTimerRef.current = window.setTimeout(setRevealed, autoMaskMs, false);
    }
  };

  const mask = () => {
    setRevealed(false);
    if (maskTimerRef.current) window.clearTimeout(maskTimerRef.current);
  };

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(value);
      setCopied(true);
      window.setTimeout(setCopied, 1500, false);
    } catch {
      // ignore — clipboard 在某些上下文不可用
    }
  };

  const display = revealed ? value : "•".repeat(Math.min(value.length, 16));

  return (
    <div
      className={cn(
        "inline-flex items-center gap-2 rounded-md border border-border bg-surface-1 px-2.5 py-1.5",
        inline ? "" : "w-full",
        className,
      )}
    >
      {label ? (
        <span className="text-caption text-muted-foreground select-none">{label}:</span>
      ) : null}
      <code
        className={cn(
          "flex-1 font-mono text-sm text-foreground select-all",
          !revealed && "tracking-widest",
        )}
        aria-label={revealed ? value : t("secret.masked", { defaultValue: "已遮罩" })}
      >
        {display}
      </code>
      {alwaysReveal ? null : (
        <button
          type="button"
          aria-label={revealed
            ? t("secret.hide", { defaultValue: "隐藏" })
            : t("secret.reveal", { defaultValue: "显示" })}
          onClick={revealed ? mask : reveal}
          className="text-text-tertiary hover:text-foreground transition-colors"
        >
          {revealed ? <EyeOff size={14} /> : <Eye size={14} />}
        </button>
      )}
      <button
        type="button"
        aria-label={copied
          ? t("secret.copied", { defaultValue: "已复制" })
          : t("secret.copy", { defaultValue: "复制" })}
        onClick={copy}
        className={cn(
          "transition-colors",
          copied ? "text-status-success" : "text-text-tertiary hover:text-foreground",
        )}
      >
        {copied ? <Check size={14} /> : <Copy size={14} />}
      </button>
    </div>
  );
}
