import type { SelectHTMLAttributes } from "react";
import { useClustersQuery } from "./api";

interface ClusterPickerProps extends Omit<SelectHTMLAttributes<HTMLSelectElement>, "onChange" | "value"> {
  value: string;
  onChange: (name: string) => void;
  /** When true, renders an empty "Select cluster" option as the first entry. */
  allowEmpty?: boolean;
  placeholder?: string;
}

export function ClusterPicker({
  value,
  onChange,
  allowEmpty = false,
  placeholder,
  className,
  disabled,
  ...rest
}: ClusterPickerProps) {
  const { data, isLoading } = useClustersQuery();
  const clusters = data?.clusters ?? [];

  return (
    <select
      {...rest}
      value={value}
      onChange={(e) => onChange(e.target.value)}
      disabled={disabled || isLoading}
      className={className ?? "px-3 py-2 rounded border border-border bg-card text-sm"}
    >
      {allowEmpty && <option value="">{placeholder ?? "Select cluster"}</option>}
      {clusters.map((c) => (
        <option key={c.name} value={c.name}>
          {c.display_name || c.name}
        </option>
      ))}
    </select>
  );
}
