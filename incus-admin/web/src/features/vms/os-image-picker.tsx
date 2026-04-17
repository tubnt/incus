import type { SelectHTMLAttributes } from "react";

export const OS_IMAGES = [
  { value: "images:ubuntu/24.04/cloud", label: "Ubuntu 24.04 LTS" },
  { value: "images:ubuntu/22.04/cloud", label: "Ubuntu 22.04 LTS" },
  { value: "images:debian/12/cloud", label: "Debian 12" },
  { value: "images:rockylinux/9/cloud", label: "Rocky Linux 9" },
] as const;

export const DEFAULT_OS_IMAGE = OS_IMAGES[0].value;

interface OsImagePickerProps extends Omit<SelectHTMLAttributes<HTMLSelectElement>, "onChange" | "value"> {
  value: string;
  onChange: (v: string) => void;
}

export function OsImagePicker({ value, onChange, className, ...rest }: OsImagePickerProps) {
  return (
    <select
      {...rest}
      value={value}
      onChange={(e) => onChange(e.target.value)}
      className={className ?? "w-full px-2 py-1.5 text-xs rounded border border-border bg-card"}
    >
      {OS_IMAGES.map((img) => (
        <option key={img.value} value={img.value}>{img.label}</option>
      ))}
    </select>
  );
}

export function getOsImageLabel(value: string): string | undefined {
  return OS_IMAGES.find((i) => i.value === value)?.label;
}
