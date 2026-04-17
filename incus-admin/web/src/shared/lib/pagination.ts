export interface PageParams {
  limit: number;
  offset: number;
}

export function pageQueryString(params?: PageParams): string {
  if (!params) return "";
  return `?limit=${params.limit}&offset=${params.offset}`;
}

export function pageKeyPart(params?: PageParams): PageParams | "all" {
  return params ?? "all";
}
