import { useQuery } from "@tanstack/react-query";
import { http } from "@/shared/lib/http";

export type JobStatus = "queued" | "running" | "succeeded" | "failed" | "partial";
// OPS-051 / PLAN-052：'warning' = smoke test 软失败（cloud-init 超时 / 22 端口
// 未监听），job 仍 succeeded，UI 以橙色提示用户重试。
export type StepStatus = "pending" | "running" | "succeeded" | "failed" | "skipped" | "warning";

export interface ProvisioningJobStep {
  id: number;
  job_id: number;
  seq: number;
  name: string;
  status: StepStatus;
  detail?: string;
  started_at?: string;
  completed_at?: string;
}

export interface ProvisioningJob {
  id: number;
  kind: "vm.create" | "vm.reinstall";
  user_id: number;
  cluster_id: number;
  order_id?: number;
  vm_id?: number;
  target_name: string;
  status: JobStatus;
  error?: string;
  refund_done_at?: string;
  created_at: string;
  started_at?: string;
  completed_at?: string;
  steps?: ProvisioningJobStep[];
}

export interface JobResult {
  vm_id: number;
  vm_name: string;
  node?: string;
  ip?: string;
  username?: string;
  password?: string;
}

export interface JobResponse {
  job: ProvisioningJob;
  result?: JobResult;
}

export const jobKeys = {
  all: ["jobs"] as const,
  detail: (id: number) => [...jobKeys.all, "detail", id] as const,
};

/** 一次性拉 job 详情：状态 + 全部 step + 完成态结果（含密码 / IP）。SSE 完成
 *  收到 done 后客户端调一次此 hook 取最终 result —— 让 SSE 流不携带 secret。 */
export function useJobQuery(jobID: number | null) {
  return useQuery({
    queryKey: jobKeys.detail(jobID ?? 0),
    queryFn: () => http.get<JobResponse>(`/portal/jobs/${jobID}`),
    enabled: jobID != null && jobID > 0,
  });
}

/** 进度展示用的人类可读 step 名称映射。后端 name 是 snake_case，前端按 i18n key
 *  渲染。新增 step 必须在这里加 key 否则 UI 会显示原生 name（fallback OK 但不美观）。 */
export const stepLabelKey: Record<string, string> = {
  // vm.create
  submit_instance: "jobs.step.submitInstance",
  wait_create: "jobs.step.waitCreate",
  start_instance: "jobs.step.startInstance",
  wait_start: "jobs.step.waitStart",
  // OPS-051 / PLAN-052
  wait_cloud_init: "jobs.step.waitCloudInit",
  verify_ready: "jobs.step.verifyReady",
  finalize: "jobs.step.finalize",
  // vm.reinstall
  fetch_instance: "jobs.step.fetchInstance",
  stop: "jobs.step.stop",
  delete: "jobs.step.delete",
  recreate: "jobs.step.recreate",
  wait_recreate: "jobs.step.waitRecreate",
  start_after_reinstall: "jobs.step.startAfterReinstall",
};
