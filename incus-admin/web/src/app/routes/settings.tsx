import { createFileRoute } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { useTheme } from "@/shared/components/theme-provider";

export const Route = createFileRoute("/settings")({
  component: SettingsPage,
});

function SettingsPage() {
  const { t, i18n } = useTranslation();
  const { theme, setTheme } = useTheme();

  return (
    <div>
      <h1 className="text-2xl font-bold mb-6">
        {t("settings.title", "设置")}
      </h1>

      <div className="space-y-6 max-w-lg">
        {/* 语言设置 */}
        <div className="border border-border rounded-lg bg-card p-4">
          <h3 className="font-semibold mb-3">
            {t("settings.language", "语言")}
          </h3>
          <div className="flex gap-2">
            {[
              { code: "zh", label: "中文" },
              { code: "en", label: "English" },
            ].map((lang) => (
              <button
                key={lang.code}
                onClick={() => i18n.changeLanguage(lang.code)}
                className={`px-4 py-2 rounded text-sm font-medium border transition ${
                  i18n.language === lang.code
                    ? "border-primary bg-primary/10 text-primary"
                    : "border-border hover:bg-muted"
                }`}
              >
                {lang.label}
              </button>
            ))}
          </div>
        </div>

        {/* 主题设置 */}
        <div className="border border-border rounded-lg bg-card p-4">
          <h3 className="font-semibold mb-3">
            {t("settings.theme", "主题")}
          </h3>
          <div className="flex gap-2">
            {[
              { value: "system" as const, label: t("settings.system", "跟随系统") },
              { value: "light" as const, label: t("settings.light", "浅色") },
              { value: "dark" as const, label: t("settings.dark", "深色") },
            ].map((opt) => (
              <button
                key={opt.value}
                onClick={() => setTheme(opt.value)}
                className={`px-4 py-2 rounded text-sm font-medium border transition ${
                  theme === opt.value
                    ? "border-primary bg-primary/10 text-primary"
                    : "border-border hover:bg-muted"
                }`}
              >
                {opt.label}
              </button>
            ))}
          </div>
        </div>
      </div>
    </div>
  );
}
