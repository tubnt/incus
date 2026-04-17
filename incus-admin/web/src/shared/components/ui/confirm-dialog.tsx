import { createContext, useCallback, useContext, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { AlertDialog } from "@base-ui-components/react/alert-dialog";

interface ConfirmOptions {
  title?: string;
  message: string;
  confirmLabel?: string;
  cancelLabel?: string;
  destructive?: boolean;
}

interface ConfirmState extends ConfirmOptions {
  open: boolean;
}

type ConfirmFn = (opts: ConfirmOptions) => Promise<boolean>;

const ConfirmContext = createContext<ConfirmFn | null>(null);

export function ConfirmDialogProvider({ children }: { children: React.ReactNode }) {
  const { t } = useTranslation();
  const [state, setState] = useState<ConfirmState>({ open: false, message: "" });
  const resolverRef = useRef<((v: boolean) => void) | null>(null);

  const confirm = useCallback<ConfirmFn>((opts) => {
    return new Promise<boolean>((resolve) => {
      resolverRef.current = resolve;
      setState({ ...opts, open: true });
    });
  }, []);

  const finish = useCallback((value: boolean) => {
    setState((s) => ({ ...s, open: false }));
    resolverRef.current?.(value);
    resolverRef.current = null;
  }, []);

  const value = useMemo(() => confirm, [confirm]);

  return (
    <ConfirmContext.Provider value={value}>
      {children}
      <AlertDialog.Root open={state.open} onOpenChange={(open) => { if (!open) finish(false); }}>
        <AlertDialog.Portal>
          <AlertDialog.Backdrop className="fixed inset-0 z-50 bg-black/50 backdrop-blur-sm data-[starting-style]:opacity-0 data-[ending-style]:opacity-0 transition-opacity" />
          <AlertDialog.Popup className="fixed left-1/2 top-1/2 z-50 w-[min(90vw,24rem)] -translate-x-1/2 -translate-y-1/2 rounded-lg border border-border bg-card shadow-lg p-5 outline-none data-[starting-style]:opacity-0 data-[ending-style]:opacity-0 data-[starting-style]:scale-95 data-[ending-style]:scale-95 transition-all">
            <AlertDialog.Title className="text-base font-semibold text-foreground">
              {state.title ?? t("confirmDialog.defaultTitle")}
            </AlertDialog.Title>
            <AlertDialog.Description className="mt-2 text-sm text-muted-foreground whitespace-pre-wrap">
              {state.message}
            </AlertDialog.Description>
            <div className="mt-5 flex justify-end gap-2">
              <button
                type="button"
                onClick={() => finish(false)}
                className="px-3 py-1.5 rounded text-sm font-medium bg-muted/60 text-muted-foreground hover:bg-muted"
              >
                {state.cancelLabel ?? t("confirmDialog.cancel")}
              </button>
              <button
                type="button"
                onClick={() => finish(true)}
                className={
                  state.destructive
                    ? "px-3 py-1.5 rounded text-sm font-medium bg-destructive text-destructive-foreground hover:opacity-90"
                    : "px-3 py-1.5 rounded text-sm font-medium bg-primary text-primary-foreground hover:opacity-90"
                }
                autoFocus
              >
                {state.confirmLabel ?? t("confirmDialog.confirm")}
              </button>
            </div>
          </AlertDialog.Popup>
        </AlertDialog.Portal>
      </AlertDialog.Root>
    </ConfirmContext.Provider>
  );
}

export function useConfirm(): ConfirmFn {
  const ctx = useContext(ConfirmContext);
  if (!ctx) {
    throw new Error("useConfirm must be used within ConfirmDialogProvider");
  }
  return ctx;
}
