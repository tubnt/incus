import { useQuery } from "@tanstack/react-query";
import { http } from "@/shared/lib/http";

export interface IncusProject {
  name: string;
  description?: string;
}

export const projectKeys = {
  all: ["project"] as const,
  list: (clusterName: string) => [...projectKeys.all, "list", clusterName] as const,
};

export function useClusterProjectsQuery(clusterName: string) {
  return useQuery({
    queryKey: projectKeys.list(clusterName),
    queryFn: () =>
      http.get<{ projects: IncusProject[] }>(
        `/admin/clusters/${clusterName}/projects`,
      ),
    enabled: !!clusterName,
    staleTime: 60_000,
  });
}
