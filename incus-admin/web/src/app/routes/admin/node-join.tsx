import { createFileRoute } from "@tanstack/react-router";
import { useMutation } from "@tanstack/react-query";
import { useState } from "react";
import { toast } from "sonner";
import { http } from "@/shared/lib/http";
import { useTranslation } from "react-i18next";

export const Route = createFileRoute("/admin/node-join")({
  component: NodeJoinWizard,
});

type Step = "prepare" | "ssh-test" | "join" | "verify";

function NodeJoinWizard() {
  const { t } = useTranslation();
  const [step, setStep] = useState<Step>("prepare");
  const [host, setHost] = useState("");
  const [nodeName, setNodeName] = useState("");
  const [sshOutput, setSSHOutput] = useState("");
  const [joinToken, setJoinToken] = useState("");

  const sshTestMutation = useMutation({
    mutationFn: () =>
      http.post<{ status: string; output: string; error?: string }>(
        "/admin/nodes/test-ssh",
        { host },
      ),
    onSuccess: (data) => {
      setSSHOutput(data.output + (data.error ? `\nError: ${data.error}` : ""));
      if (data.status === "ok") {
        toast.success("SSH 连接成功");
        setStep("join");
      } else {
        toast.error("SSH 连接失败");
      }
    },
  });

  const generateTokenMutation = useMutation({
    mutationFn: () =>
      http.post<{ status: string; output: string }>(
        "/admin/nodes/exec",
        { host, command: `incus cluster add ${nodeName}` },
      ),
    onSuccess: (data) => {
      if (data.output) {
        setJoinToken(data.output.trim());
        toast.success("Join token 已生成");
        setStep("verify");
      }
    },
    onError: () => toast.error("生成 token 失败"),
  });

  return (
    <div>
      <h1 className="text-2xl font-bold mb-2">
        {t("admin.nodeJoin.title", "节点加入向导")}
      </h1>
      <p className="text-sm text-muted-foreground mb-6">
        {t(
          "admin.nodeJoin.desc",
          "按步骤将新节点加入 Incus 集群",
        )}
      </p>

      {/* 步骤指示器 */}
      <div className="flex gap-2 mb-8">
        {(
          [
            ["prepare", "1. 准备"],
            ["ssh-test", "2. SSH 测试"],
            ["join", "3. 生成 Token"],
            ["verify", "4. 验证"],
          ] as const
        ).map(([s, label]) => (
          <button
            key={s}
            onClick={() => setStep(s)}
            className={`px-3 py-1.5 rounded text-xs font-medium transition ${
              step === s
                ? "bg-primary text-primary-foreground"
                : "bg-muted/50 text-muted-foreground"
            }`}
          >
            {label}
          </button>
        ))}
      </div>

      {/* Step 1: 准备 */}
      {step === "prepare" && (
        <div className="border border-border rounded-lg bg-card p-6 space-y-4">
          <h3 className="font-semibold">准备工作</h3>
          <div className="text-sm text-muted-foreground space-y-2">
            <p>在新节点上完成以下准备：</p>
            <ol className="list-decimal list-inside space-y-1 ml-2">
              <li>
                确保新节点已安装 Ubuntu 22.04/24.04 或 Debian 12
              </li>
              <li>
                安装 Incus：
                <code className="ml-1 px-1.5 py-0.5 rounded bg-muted text-xs font-mono">
                  curl -fsSL https://pkgs.zabbly.com/get/incus-stable | sudo bash
                </code>
              </li>
              <li>确保新节点与现有集群网络互通</li>
              <li>配置 SSH key 免密登录</li>
            </ol>
          </div>
          <div className="grid grid-cols-2 gap-3">
            <div>
              <label className="text-xs text-muted-foreground">
                新节点 IP
              </label>
              <input
                value={host}
                onChange={(e) => setHost(e.target.value)}
                placeholder="10.100.0.20"
                className="w-full mt-1 px-3 py-2 rounded border border-border bg-card text-sm font-mono"
              />
            </div>
            <div>
              <label className="text-xs text-muted-foreground">
                节点名称
              </label>
              <input
                value={nodeName}
                onChange={(e) => setNodeName(e.target.value)}
                placeholder="node-05"
                className="w-full mt-1 px-3 py-2 rounded border border-border bg-card text-sm font-mono"
              />
            </div>
          </div>
          <button
            onClick={() => setStep("ssh-test")}
            disabled={!host || !nodeName}
            className="px-4 py-2 bg-primary text-primary-foreground rounded text-sm font-medium disabled:opacity-50"
          >
            下一步：测试 SSH
          </button>
        </div>
      )}

      {/* Step 2: SSH 测试 */}
      {step === "ssh-test" && (
        <div className="border border-border rounded-lg bg-card p-6 space-y-4">
          <h3 className="font-semibold">SSH 连接测试</h3>
          <p className="text-sm text-muted-foreground">
            测试到 <code className="font-mono">{host}</code> 的 SSH
            连接
          </p>
          <button
            onClick={() => sshTestMutation.mutate()}
            disabled={sshTestMutation.isPending}
            className="px-4 py-2 bg-primary text-primary-foreground rounded text-sm font-medium disabled:opacity-50"
          >
            {sshTestMutation.isPending ? "测试中..." : "测试 SSH 连接"}
          </button>
          {sshOutput && (
            <pre className="p-3 text-xs font-mono bg-black text-green-400 rounded overflow-x-auto whitespace-pre-wrap max-h-48">
              {sshOutput}
            </pre>
          )}
        </div>
      )}

      {/* Step 3: 生成 Join Token */}
      {step === "join" && (
        <div className="border border-border rounded-lg bg-card p-6 space-y-4">
          <h3 className="font-semibold">生成 Join Token</h3>
          <p className="text-sm text-muted-foreground">
            在现有集群节点上执行{" "}
            <code className="font-mono">
              incus cluster add {nodeName}
            </code>{" "}
            生成 join token。
          </p>
          <div className="flex gap-2">
            <button
              onClick={() => generateTokenMutation.mutate()}
              disabled={generateTokenMutation.isPending}
              className="px-4 py-2 bg-primary text-primary-foreground rounded text-sm font-medium disabled:opacity-50"
            >
              {generateTokenMutation.isPending
                ? "生成中..."
                : "通过 SSH 生成 Token"}
            </button>
            <span className="text-xs text-muted-foreground self-center">
              或手动粘贴 token:
            </span>
          </div>
          <textarea
            value={joinToken}
            onChange={(e) => setJoinToken(e.target.value)}
            placeholder="粘贴 join token..."
            rows={3}
            className="w-full px-3 py-2 rounded border border-border bg-card text-xs font-mono"
          />
          {joinToken && (
            <button
              onClick={() => setStep("verify")}
              className="px-4 py-2 bg-primary text-primary-foreground rounded text-sm font-medium"
            >
              下一步：验证
            </button>
          )}
        </div>
      )}

      {/* Step 4: 验证 */}
      {step === "verify" && (
        <div className="border border-border rounded-lg bg-card p-6 space-y-4">
          <h3 className="font-semibold">在新节点上执行加入命令</h3>
          <p className="text-sm text-muted-foreground">
            在新节点{" "}
            <code className="font-mono">{host}</code> 上执行以下命令：
          </p>
          <div className="relative">
            <pre className="p-3 text-xs font-mono bg-black text-green-400 rounded overflow-x-auto whitespace-pre-wrap">
              {`sudo incus admin init --preseed <<EOF
cluster:
  enabled: true
  server_name: ${nodeName}
  cluster_token: "${joinToken}"
EOF`}
            </pre>
            <button
              onClick={() => {
                navigator.clipboard.writeText(
                  `sudo incus admin init --preseed <<EOF\ncluster:\n  enabled: true\n  server_name: ${nodeName}\n  cluster_token: "${joinToken}"\nEOF`,
                );
                toast.success("命令已复制到剪贴板");
              }}
              className="absolute top-2 right-2 px-2 py-1 text-xs bg-muted/80 rounded hover:bg-muted"
            >
              复制
            </button>
          </div>
          <div className="text-sm text-muted-foreground">
            <p>加入完成后，在集群节点上确认：</p>
            <code className="block mt-1 px-3 py-2 bg-muted rounded text-xs font-mono">
              incus cluster list
            </code>
          </div>
          <a
            href="/admin/nodes"
            className="inline-block px-4 py-2 bg-primary text-primary-foreground rounded text-sm font-medium hover:opacity-90"
          >
            查看节点列表
          </a>
        </div>
      )}
    </div>
  );
}
