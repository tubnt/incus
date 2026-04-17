import { useEffect } from "react";
import { useTranslation } from "react-i18next";
import { cn } from "@/shared/lib/utils";
import { useClusterProjectsQuery } from "./api";

export interface ProjectPickerProps {
  clusterName: string;
  value: string;
  onChange: (projectName: string) => void;
  className?: string;
  placeholder?: string;
}

export function ProjectPicker({
  clusterName,
  value,
  onChange,
  className,
  placeholder,
}: ProjectPickerProps) {
  const { t } = useTranslation();
  const { data, isLoading, isError } = useClusterProjectsQuery(clusterName);
  const projects = data?.projects ?? [];

  useEffect(() => {
    if (!value && projects.length > 0) {
      const preferred =
        projects.find((p) => p.name === "customers") ??
        projects.find((p) => p.name === "default") ??
        projects[0]!;
      onChange(preferred.name);
    }
  }, [projects, value, onChange]);

  if (isError) {
    return (
      <select
        value={value}
        onChange={(e) => onChange(e.target.value)}
        className={cn("w-full px-3 py-2 rounded-md border border-border bg-card text-sm", className)}
      >
        <option value="customers">customers</option>
        <option value="default">default</option>
      </select>
    );
  }

  return (
    <select
      value={value}
      onChange={(e) => onChange(e.target.value)}
      disabled={isLoading || !clusterName}
      className={cn("w-full px-3 py-2 rounded-md border border-border bg-card text-sm disabled:opacity-50", className)}
    >
      {!value && (
        <option value="" disabled>
          {placeholder ?? t("project.select", { defaultValue: "选择项目" })}
        </option>
      )}
      {projects.map((p) => (
        <option key={p.name} value={p.name}>
          {p.name}
          {p.description ? ` — ${p.description}` : ""}
        </option>
      ))}
    </select>
  );
}
