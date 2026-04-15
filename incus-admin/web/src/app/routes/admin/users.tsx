import { createFileRoute } from "@tanstack/react-router";
import { useMutation, useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { http } from "@/shared/lib/http";
import { queryClient } from "@/shared/lib/query-client";
import type { User } from "@/shared/lib/auth";

export const Route = createFileRoute("/admin/users")({
  component: UsersPage,
});

function UsersPage() {
  const { data, isLoading } = useQuery({
    queryKey: ["adminUsers"],
    queryFn: () => http.get<{ users: User[] }>("/admin/users"),
  });

  const users = data?.users ?? [];

  return (
    <div>
      <h1 className="text-2xl font-bold mb-6">Users ({users.length})</h1>
      {isLoading ? (
        <div className="text-muted-foreground">Loading...</div>
      ) : (
        <div className="border border-border rounded-lg overflow-hidden">
          <table className="w-full text-sm">
            <thead className="bg-muted/30">
              <tr>
                <th className="text-left px-4 py-3 font-medium">ID</th>
                <th className="text-left px-4 py-3 font-medium">Email</th>
                <th className="text-left px-4 py-3 font-medium">Role</th>
                <th className="text-right px-4 py-3 font-medium">Balance</th>
                <th className="text-right px-4 py-3 font-medium">Actions</th>
              </tr>
            </thead>
            <tbody>
              {users.map((u) => (
                <UserRow key={u.id} user={u} />
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

function UserRow({ user }: { user: User }) {
  const [showTopUp, setShowTopUp] = useState(false);
  const [amount, setAmount] = useState("");

  const roleMutation = useMutation({
    mutationFn: (role: string) => http.put(`/admin/users/${user.id}/role`, { role }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["adminUsers"] }),
  });

  const topUpMutation = useMutation({
    mutationFn: (amt: number) => http.post(`/admin/users/${user.id}/balance`, { amount: amt, description: "Admin top-up" }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["adminUsers"] });
      setShowTopUp(false);
      setAmount("");
    },
  });

  return (
    <>
      <tr className="border-t border-border">
        <td className="px-4 py-3">{user.id}</td>
        <td className="px-4 py-3 font-mono text-xs">{user.email}</td>
        <td className="px-4 py-3">
          <select
            value={user.role}
            onChange={(e) => roleMutation.mutate(e.target.value)}
            disabled={roleMutation.isPending}
            className="px-2 py-1 rounded text-xs border border-border bg-card"
          >
            <option value="customer">customer</option>
            <option value="admin">admin</option>
          </select>
        </td>
        <td className="px-4 py-3 text-right font-mono">${user.balance.toFixed(2)}</td>
        <td className="px-4 py-3 text-right">
          <button
            onClick={() => setShowTopUp(!showTopUp)}
            className="px-2 py-1 rounded text-xs bg-primary/20 text-primary hover:bg-primary/30"
          >
            + Top Up
          </button>
        </td>
      </tr>
      {showTopUp && (
        <tr className="border-t border-border bg-card/50">
          <td colSpan={5} className="px-4 py-3">
            <div className="flex items-center gap-2 max-w-md">
              <span className="text-sm">$</span>
              <input
                type="number"
                value={amount}
                onChange={(e) => setAmount(e.target.value)}
                placeholder="Amount"
                className="flex-1 px-3 py-1.5 rounded border border-border bg-card text-sm"
              />
              <button
                onClick={() => {
                  const amt = parseFloat(amount);
                  if (amt > 0) topUpMutation.mutate(amt);
                }}
                disabled={topUpMutation.isPending || !amount}
                className="px-3 py-1.5 rounded text-xs bg-primary text-primary-foreground disabled:opacity-50"
              >
                {topUpMutation.isPending ? "..." : "Confirm"}
              </button>
              <button
                onClick={() => setShowTopUp(false)}
                className="px-3 py-1.5 rounded text-xs bg-muted text-muted-foreground"
              >
                Cancel
              </button>
            </div>
          </td>
        </tr>
      )}
    </>
  );
}
