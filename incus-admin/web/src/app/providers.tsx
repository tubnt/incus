import { QueryClientProvider } from "@tanstack/react-query";
import { I18nextProvider } from "react-i18next";
import type { ReactNode } from "react";
import { queryClient } from "@/shared/lib/query-client";
import { ThemeProvider } from "@/shared/components/theme-provider";
import i18n from "./i18n";

export function Providers({ children }: { children: ReactNode }) {
  return (
    <I18nextProvider i18n={i18n}>
      <QueryClientProvider client={queryClient}>
        <ThemeProvider defaultTheme="dark">
          {children}
        </ThemeProvider>
      </QueryClientProvider>
    </I18nextProvider>
  );
}
