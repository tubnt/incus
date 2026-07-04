import { useMutation, useQuery } from "@tanstack/react-query";
import { clusterKeys } from "@/features/clusters/api";
import { vmKeys } from "@/features/vms/api";
import { http } from "@/shared/lib/http";
import { queryClient } from "@/shared/lib/query-client";

export interface ClusterNode {
  cluster: string;
  server_name: string;
  url: string;
  status: string;
  message: string;
  architecture: string;
  roles: string[];
  description: string;
}

export interface NodeInstance {
  name: string;
  status: string;
  type: string;
  location: string;
}

export interface NodeDetail {
  node: Record<string, unknown>;
  instances: NodeInstance[];
}

export interface SSHResult {
  status: string;
  output: string;
  error?: string;
}

export interface HANodeInfo {
  server_name: string;
  url: string;
  status: string;
  message: string;
  roles: string;
}

export interface HAStatus {
  cluster: string;
  healing_threshold: number;
  storage: string;
  ha_enabled: boolean;
  nodes: HANodeInfo[];
}

export const nodeKeys = {
  all: ["node"] as const,
  list: () => [...nodeKeys.all, "list"] as const,
  detail: (cluster: string, name: string) => [...nodeKeys.all, "detail", cluster, name] as const,
  ha: (cluster: string) => [...nodeKeys.all, "ha", cluster] as const,
  topology: (cluster: string) => [...nodeKeys.all, "topology", cluster] as const,
  imbalance: (cluster: string) => [...nodeKeys.all, "imbalance", cluster] as const,
};

// PLAN-037 / OPS-040 节点拓扑（admin/nodes 顶部 strip + RebalancePanel 共用）。
// PLAN-039 / OPS-042 扩字段：load_5min / cpu_free_ratio / disk_free_ratio / score
export interface NodeTopologyEntry {
  server_name: string;
  status: string;
  message?: string;
  cpu_total: number;
  mem_total: number;
  mem_used: number;
  mem_free: number;
  free_ratio: number;
  load_5min: number;       // 5min load average
  cpu_free_ratio: number;  // 1 - min(load/cpu_total, 1)
  disk_free_ratio: number; // cluster-wide（Ceph 共享存储）
  score: number;           // 综合调度得分 0..1
  vm_count: number;
  vm_running: number;
  vm_stopped: number;
  vm_other: number;
  maintenance: boolean;
  evacuated: boolean;
}

export function useNodeTopologyQuery(clusterName: string) {
  return useQuery({
    queryKey: nodeKeys.topology(clusterName),
    queryFn: () => http.get<{ cluster: string; nodes: NodeTopologyEntry[] }>(
      `/admin/clusters/${clusterName}/nodes/topology`,
    ),
    enabled: !!clusterName,
    refetchInterval: 30_000,
  });
}

// PLAN-037 不均衡建议（按需点开 RebalancePanel 时拉，不轮询）。
export interface ImbalanceSuggestion {
  vm_name: string;
  project: string;
  source_node: string;
  target_node: string;
  reason: string;
  score: number;
}

export interface ImbalancePlan {
  stats: {
    mean_util: number;
    stddev: number;
    max_diff: number;
    hot_node?: string;
    cold_node?: string;
    imbalanced: boolean;
  };
  suggestions: ImbalanceSuggestion[];
}

export function useImbalanceSuggestionsQuery(clusterName: string, enabled: boolean) {
  return useQuery({
    queryKey: nodeKeys.imbalance(clusterName),
    queryFn: () => http.get<ImbalancePlan>(
      `/admin/clusters/${clusterName}/imbalance-suggestions`,
    ),
    enabled: enabled && !!clusterName,
    staleTime: 30_000,
  });
}

// PLAN-039 / OPS-044：watchdog 告警拉取 + dismiss
export interface SystemAlert {
  id: number;
  kind: string;
  cluster: string;
  severity: "info" | "warning" | "error";
  payload: { stats?: { mean_util: number; stddev: number; max_diff: number; hot_node?: string; cold_node?: string }; suggestion_count?: number; persistent_ticks?: number };
  created_at: string;
  updated_at: string;
}

export const alertKeys = {
  all: ["system-alerts"] as const,
  active: () => [...alertKeys.all, "active"] as const,
};

export function useSystemAlertsQuery() {
  return useQuery({
    queryKey: alertKeys.active(),
    queryFn: () => http.get<{ alerts: SystemAlert[] }>("/admin/system-alerts"),
    refetchInterval: 60_000,
  });
}

export function useDismissAlertMutation() {
  return useMutation({
    mutationFn: (id: number) =>
      http.post<{ status: string }>(
        `/admin/system-alerts/${id}/dismiss`,
        undefined,
        {
          intent: {
            action: "system_alert.dismiss",
            args: { id },
            description: `忽略告警 #${id}`,
          },
        },
      ),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: alertKeys.all }),
  });
}

// PLAN-037 批量冷迁移；PLAN-039 / OPS-043 加 mode 字段。
export type MigrateMode = "auto" | "live" | "cold";

export interface MigrateBatchItem {
  vm_name: string;
  project?: string;
  source_node?: string;
  target_node: string;
  mode?: MigrateMode;
}

export function useMigrateBatchMutation(clusterName: string) {
  return useMutation({
    mutationFn: (body: {
      items: MigrateBatchItem[];
      mode?: MigrateMode;
      concurrency_per_source?: number;
      global_concurrency?: number;
    }) =>
      http.post<{ job_id: number }>(
        "/admin/vms:migrate-batch",
        { cluster: clusterName, ...body },
        {
          intent: {
            action: "vm.migrate-batch",
            args: { cluster: clusterName, count: body.items.length, mode: body.mode ?? "auto" },
            description: `批量迁移 ${body.items.length} 台 VM 到指定节点（${body.mode ?? "auto"}）`,
          },
        },
      ),
    onSuccess: () => {
      // PLAN-051 §2-D / Session-2 F-56：节点拓扑 + 不均衡 + cluster VMs 列表 + my list
      queryClient.invalidateQueries({ queryKey: nodeKeys.topology(clusterName) });
      queryClient.invalidateQueries({ queryKey: nodeKeys.imbalance(clusterName) });
      queryClient.invalidateQueries({ queryKey: vmKeys.clusterList(clusterName) });
      queryClient.invalidateQueries({ queryKey: vmKeys.myList() });
    },
  });
}

// PLAN-039 / OPS-043 启用 stateful（live migration 前置；会重启 VM）
export function useEnableStatefulMutation() {
  return useMutation({
    mutationFn: (params: { name: string; cluster: string; project?: string }) =>
      http.post<{ status: string }>(
        `/admin/vms/${params.name}/enable-stateful`,
        { cluster: params.cluster, project: params.project ?? "customers" },
        {
          intent: {
            action: "vm.enable_stateful",
            args: params,
            description: `启用 ${params.name} 的 live migration（需重启）`,
          },
        },
      ),
    // Session-2 F-54：原版无 invalidate，UI 显示旧 migration.stateful → 用户重复点击
    onSuccess: (_data, params) => {
      queryClient.invalidateQueries({ queryKey: vmKeys.clusterList(params.cluster) });
      queryClient.invalidateQueries({
        queryKey: vmKeys.clusterDetail(params.cluster, params.name, params.project ?? "customers"),
      });
    },
  });
}

export function useEnableStatefulBatchMutation() {
  return useMutation({
    mutationFn: (params: { cluster: string; project?: string; names: string[] }) =>
      http.post<{ total: number; succeeded: number; results: Array<{ name: string; ok: boolean; error?: string }> }>(
        "/admin/vms:enable-stateful-batch",
        { cluster: params.cluster, project: params.project ?? "customers", names: params.names },
        {
          intent: {
            action: "vm.enable_stateful_batch",
            args: { count: params.names.length, cluster: params.cluster },
            description: `批量启用 ${params.names.length} 台 VM 的 live migration（每台重启 30s）`,
          },
        },
      ),
    onSuccess: (_data, params) => {
      queryClient.invalidateQueries({ queryKey: vmKeys.clusterList(params.cluster) });
    },
  });
}

export function useAdminNodesQuery() {
  return useQuery({
    queryKey: nodeKeys.list(),
    queryFn: () => http.get<{ nodes: ClusterNode[] }>("/admin/nodes"),
    refetchInterval: 15_000,
  });
}

export function useAdminNodeDetailQuery(clusterName: string, nodeName: string) {
  return useQuery({
    queryKey: nodeKeys.detail(clusterName, nodeName),
    queryFn: () => http.get<NodeDetail>(`/admin/nodes/${nodeName}?cluster=${clusterName}`),
    enabled: !!clusterName && !!nodeName,
  });
}

export function useNodeEvacuateMutation(clusterName: string, nodeName: string) {
  return useMutation({
    mutationFn: () =>
      http.post(`/admin/nodes/${nodeName}/evacuate?cluster=${clusterName}`, {}),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: nodeKeys.all });
      queryClient.invalidateQueries({ queryKey: clusterKeys.all });
    },
    // WP-F：HA 疏散成功/失败统一由全局 MutationCache 兜底提示。
    meta: { globalErrorToast: true, successToast: "ha.evacuateStartedToast" },
  });
}

export function useNodeRestoreMutation(clusterName: string, nodeName: string) {
  return useMutation({
    mutationFn: () =>
      http.post(`/admin/nodes/${nodeName}/restore?cluster=${clusterName}`, {}),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: nodeKeys.all });
      queryClient.invalidateQueries({ queryKey: clusterKeys.all });
    },
    meta: { globalErrorToast: true, successToast: "ha.nodeRestoredToast" },
  });
}

export function useHAStatusQuery(clusterName: string) {
  return useQuery({
    queryKey: nodeKeys.ha(clusterName),
    queryFn: () => http.get<HAStatus>(`/admin/clusters/${clusterName}/ha`),
    enabled: !!clusterName,
    refetchInterval: 15_000,
  });
}

export function useHAEvacuateMutation(clusterName: string) {
  return useMutation({
    mutationFn: (nodeName: string) =>
      http.post(
        `/admin/clusters/${clusterName}/nodes/${nodeName}/evacuate`,
        undefined,
        {
          intent: {
            action: "node.evacuate",
            args: { cluster: clusterName, node: nodeName },
            description: `迁移节点 ${nodeName} 上所有 VM`,
          },
        },
      ),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: nodeKeys.all });
      queryClient.invalidateQueries({ queryKey: clusterKeys.all });
    },
    // WP-F：HA 页疏散成功/失败统一由全局 MutationCache 兜底提示。
    meta: { globalErrorToast: true, successToast: "ha.evacuateStartedToast" },
  });
}

export function useTestSSHMutation() {
  return useMutation({
    mutationFn: (host: string) => http.post<SSHResult>("/admin/nodes/test-ssh", { host }),
  });
}

export function useExecSSHMutation() {
  return useMutation({
    mutationFn: (params: { host: string; command: string }) =>
      http.post<SSHResult>("/admin/nodes/exec", params),
  });
}

/**
 * PLAN-026 / INFRA-002 节点 add/remove 走 jobs runtime 异步流。
 * 返回 202 + job_id，前端用 useJobStream 监听 SSE。
 *
 * PLAN-033 / OPS-039 wizard 字段：probe_id / accepted_host_key_sha256 /
 * credential_id / inline_*。新字段全部可选；不传 = 兼容旧 ssh_key_file 路径。
 */
export interface AddNodeParams {
  node_name: string;
  public_ip: string;
  ssh_user?: string;
  ssh_key_file?: string;
  role?: "osd" | "mon-mgr-osd";
  // OPS-026 / PLAN-028 advanced：bonded NIC / 异构拓扑
  nic_primary?: string;
  nic_cluster?: string;
  bridge_name?: string;
  mgmt_ip?: string;
  ceph_pub_ip?: string;
  ceph_cluster_ip?: string;
  skip_network?: boolean;
  // PLAN-033 wizard
  probe_id?: string;
  accepted_host_key_sha256?: string;
  credential_id?: number;
  inline_kind?: "password" | "private_key";
  inline_password?: string;
  inline_key_data?: string;
}

/* ============================================================
 *  PLAN-033 / OPS-039 — node credentials + probe APIs
 * ============================================================ */

export interface NodeCredential {
  id: number;
  name: string;
  kind: "password" | "private_key";
  fingerprint?: string;
  created_by: number;
  created_at: string;
  last_used_at?: string;
}

export const credentialKeys = {
  all: ["node-credentials"] as const,
  list: () => [...credentialKeys.all, "list"] as const,
};

export function useNodeCredentialsQuery() {
  return useQuery({
    queryKey: credentialKeys.list(),
    queryFn: () => http.get<{ credentials: NodeCredential[] }>("/admin/node-credentials"),
  });
}

export interface CreateCredentialBody {
  name: string;
  kind: "password" | "private_key";
  password?: string;
  key_data?: string;
}

export function useCreateCredentialMutation() {
  return useMutation({
    mutationFn: (body: CreateCredentialBody) =>
      http.post<{ credential: NodeCredential }>("/admin/node-credentials", body, {
        intent: {
          action: "node.credential.create",
          args: { name: body.name, kind: body.kind },
          description: `保存节点凭据 ${body.name}（${body.kind === "password" ? "密码" : "私钥"}）`,
        },
      }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: credentialKeys.all }),
  });
}

export function useDeleteCredentialMutation() {
  return useMutation({
    mutationFn: (id: number) =>
      http.delete<{ status: string; id: number }>(`/admin/node-credentials/${id}`, undefined, {
        intent: {
          action: "node.credential.delete",
          args: { id },
          description: `删除节点凭据 #${id}`,
        },
      }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: credentialKeys.all }),
  });
}

export interface ProbeHostKeyParams {
  host: string;
  port?: number;
  user?: string;
}

export interface ProbeHostKeyResult {
  host: string;
  port: number;
  key_type: string;
  fingerprint: string;
}

export function useProbeHostKeyMutation(clusterName: string) {
  return useMutation({
    mutationFn: (body: ProbeHostKeyParams) =>
      http.post<ProbeHostKeyResult>(
        `/admin/clusters/${clusterName}/nodes/probe-host-key`,
        body,
        {
          intent: {
            action: "node.probe.host_key",
            args: { cluster: clusterName, host: body.host },
            description: `探测 ${body.host} SSH 主机密钥`,
          },
        },
      ),
  });
}

export interface ProbeNodeParams {
  host: string;
  port?: number;
  user?: string;
  credential_id?: number;
  inline_kind?: "password" | "private_key";
  inline_password?: string;
  inline_key_data?: string;
  accepted_host_key_sha256: string;
}

export interface NodeInfo {
  hostname: string;
  os: { id?: string; version?: string; kernel?: string };
  cpu: { model?: string; cores?: number; threads?: number };
  memory_kb: number;
  interfaces: Array<{
    name: string;
    kind: string;
    mac?: string;
    speed_mbps?: number;
    slaves?: string[];
    master?: string;
    addresses?: string[];
    is_default_route?: boolean;
    // PLAN-038 / OPS-041 Phase A 增字段
    driver?: string;
    pci_bus_id?: string;
    link_up?: boolean;
    duplex?: string;
  }>;
  default_route?: { interface: string; gateway?: string; source?: string };
  public_ip_observed?: string;
  disks: Array<{ name: string; size_bytes?: number; rotational?: boolean; model?: string }>;
  incus_installed: boolean;
  ceph_installed: boolean;
  // PLAN-038 / OPS-041 Phase A
  pci_devices?: Array<{ slot: string; class?: string; vendor?: string; device?: string }>;
  numa?: { sockets?: number; numa_nodes?: number };
}

// PLAN-038 / OPS-041 Phase A：Tier 1 ranked 候选（4 角色 × top-3）。
export type NICRole = "bridge_source" | "mgmt" | "ceph_public" | "ceph_cluster";

export interface RankedCandidate {
  nic: string;
  score: number;
  confidence: number;
  reasons: string[];
}

export interface RankedRole {
  role: NICRole;
  candidates: RankedCandidate[];
}

export interface RankedResult {
  roles: RankedRole[];
  overall_confidence: number;
}

export interface ProbeNodeResult {
  probe_id: string;
  node: NodeInfo;
  fingerprint: string;
  ranked?: RankedResult;
}

// PLAN-038 / OPS-041 Phase B Tier 2: AI 角色推荐
export interface AIRoleRecommendation {
  role: NICRole;
  nic: string;
  confidence: number;
  rationale: string;
}

export interface AISuggestResult {
  recommendations: AIRoleRecommendation[];
  warnings: string[];
  tier1: RankedResult;
  provider: string;
  model: string;
}

// PLAN-038 / OPS-041 + pma-cr F2：用 useQuery 缓存（key=probeID）避免重复付费。
// `enabled: false` + 手动 `refetch()` 触发；query cache 让 5min 内重复展开 panel
// 不会再次打 LLM。
export const aiKeys = {
  all: ["ai"] as const,
  suggestRoles: (cluster: string, probeID: string) =>
    [...aiKeys.all, "suggest", cluster, probeID] as const,
  diagnose: (jobID: number) =>
    [...aiKeys.all, "diagnose", jobID] as const,
};

export function useAISuggestRolesQuery(
  clusterName: string,
  probeID: string,
  expectedRole: "osd" | "mon-mgr-osd" = "osd",
) {
  return useQuery({
    queryKey: aiKeys.suggestRoles(clusterName, probeID),
    queryFn: () =>
      http.post<AISuggestResult>(
        `/admin/clusters/${clusterName}/nodes/ai-suggest`,
        { probe_id: probeID, expected_role: expectedRole },
        {
          intent: {
            action: "ai.role_mapping",
            args: { cluster: clusterName, role: expectedRole },
            description: `AI 推荐网卡角色（cluster=${clusterName}）`,
          },
        },
      ),
    enabled: false,           // 手动 refetch() 触发
    staleTime: 5 * 60 * 1000, // 5min 内不重新付费
    gcTime: 10 * 60 * 1000,
    retry: false,
  });
}

// PLAN-038 / OPS-041 Phase C Tier 3: AI 失败诊断
export interface AIDiagnosis {
  category: string;
  root_cause: string;
  suggested_fix_steps: Array<{ step: string; command_template?: string }>;
  safe_to_auto_retry: boolean;
  requires_manual: string;
}

export interface AIDiagnoseResult {
  diagnosis: AIDiagnosis;
  provider: string;
  model: string;
}

// PLAN-038 / OPS-041 + pma-cr F2：诊断结果按 jobID 缓存。
// 折叠 / 展开 / 翻页 / 重渲染都不再付费；用户主动点"重新分析"才 invalidate。
export function useAIDiagnoseQuery(jobID: number | null) {
  return useQuery({
    queryKey: jobID ? aiKeys.diagnose(jobID) : ["ai", "diagnose", "noop"],
    queryFn: () =>
      http.post<AIDiagnoseResult>(
        `/admin/jobs/${jobID}/ai-diagnose`,
        undefined,
        {
          intent: {
            action: "ai.diagnose",
            args: { job_id: jobID },
            description: `AI 诊断 job #${jobID} 失败原因`,
          },
        },
      ),
    enabled: false,           // 手动 refetch() 触发
    staleTime: 5 * 60 * 1000,
    gcTime: 10 * 60 * 1000,
    retry: false,
  });
}

export function useProbeNodeMutation(clusterName: string) {
  return useMutation({
    mutationFn: (body: ProbeNodeParams) =>
      http.post<ProbeNodeResult>(`/admin/clusters/${clusterName}/nodes/probe`, body, {
        intent: {
          action: "node.probe",
          args: { cluster: clusterName, host: body.host },
          description: `探测节点 ${body.host} 的 OS/CPU/内存/网卡`,
        },
      }),
  });
}

export interface NodeJobResponse {
  status: string;
  job_id: number;
  node: string;
}

export function useAddNodeMutation(clusterName: string) {
  return useMutation({
    mutationFn: (params: AddNodeParams) =>
      http.post<NodeJobResponse>(`/admin/clusters/${clusterName}/nodes`, params, {
        intent: {
          action: "node.add",
          args: { cluster: clusterName, name: params.node_name, ip: params.public_ip },
          description: `添加节点 ${params.node_name} (${params.public_ip}) 到集群 ${clusterName}`,
        },
      }),
  });
}

// OPS-024 D2 maintenance mode toggle（Incus scheduler.instance = manual / all）
export function useNodeMaintenanceMutation(clusterName: string, nodeName: string) {
  return useMutation({
    mutationFn: (enabled: boolean) =>
      http.post(`/admin/clusters/${clusterName}/nodes/${nodeName}/maintenance`, { enabled }, {
        intent: {
          action: "node.maintenance",
          args: { cluster: clusterName, node: nodeName, enabled },
          description: enabled
            ? `把节点 ${nodeName} 设为维护模式（防新放置，保留现 VM）`
            : `恢复节点 ${nodeName} 的常规调度`,
        },
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: nodeKeys.all });
      queryClient.invalidateQueries({ queryKey: clusterKeys.all });
    },
  });
}

// OPS-024 C2：cluster-env.sh 下载（通过 step-up 鉴权）
export function clusterEnvScriptURL(clusterName: string): string {
  return `/api/admin/clusters/${clusterName}/env-script`;
}

// OPS-028 P3.4：fetch 走 step-up 401 拦截 → 跳 OIDC；避免浏览器直链落到
// step-up JSON 裸页。返回 ok=true 时浏览器开始下载；ok=false 时已被
// step-up 接管或抛出错误。
//
// Session-2 F-55 / PLAN-051 §2-K：接受可选 AbortSignal 让调用方在 unmount
// 时取消请求；blob URL 用 try/finally 保证任何分支都 revoke（含异常路径）。
export async function downloadClusterEnvScript(
  clusterName: string,
  signal?: AbortSignal,
): Promise<void> {
  const url = clusterEnvScriptURL(clusterName);
  const resp = await fetch(url, { credentials: "same-origin", signal });
  if (resp.status === 401) {
    const body: unknown = await resp.json().catch(() => null);
    if (
      body
      && typeof body === "object"
      && (body as Record<string, unknown>).error === "step_up_required"
    ) {
      const redirect = (body as Record<string, unknown>).redirect;
      if (typeof redirect === "string" && redirect.startsWith("/api/auth/stepup/")) {
        window.location.href = redirect;
        return;
      }
    }
    throw new Error("unauthorized");
  }
  if (!resp.ok) {
    throw new Error(`download failed: HTTP ${resp.status}`);
  }
  const blob = await resp.blob();
  const objectURL = URL.createObjectURL(blob);
  try {
    const a = document.createElement("a");
    a.href = objectURL;
    a.download = "cluster-env.sh";
    document.body.appendChild(a);
    try {
      a.click();
    } finally {
      document.body.removeChild(a);
    }
  } finally {
    // 即使 a.click 抛错也保证回收 blob URL —— 浏览器对 blob URL 的内存
    // 持有引用直到 revoke，泄漏会持续到 tab close。
    URL.revokeObjectURL(objectURL);
  }
}

export function useRemoveNodeMutation(clusterName: string) {
  return useMutation({
    mutationFn: (params: { nodeName: string; leader?: string }) => {
      const qs = params.leader ? `?leader=${encodeURIComponent(params.leader)}` : "";
      return http.delete<NodeJobResponse>(
        `/admin/clusters/${clusterName}/nodes/${params.nodeName}${qs}`,
        undefined,
        {
          intent: {
            action: "node.remove",
            args: { cluster: clusterName, node: params.nodeName },
            description: `从集群 ${clusterName} 移除节点 ${params.nodeName}（会先 evacuate 所有 VM）`,
          },
        },
      );
    },
  });
}
