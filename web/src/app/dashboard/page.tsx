"use client";

import { useCallback, useEffect, useState } from "react";
import Link from "next/link";
import { AppShell } from "@/components/AppShell";
import { AddMachineModal } from "@/components/AddMachineModal";
import { StatusDot } from "@/components/StatusDot";
import { api, Client, SetupStatus } from "@/lib/api";

function timeAgo(unixSeconds?: number): string {
  if (!unixSeconds) return "never";
  const seconds = Math.max(0, Date.now() / 1000 - unixSeconds);
  if (seconds < 60) return "just now";
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m ago`;
  if (seconds < 86400) return `${Math.floor(seconds / 3600)}h ago`;
  return `${Math.floor(seconds / 86400)}d ago`;
}

function DashboardContent() {
  const [clients, setClients] = useState<Client[] | null>(null);
  const [showAddModal, setShowAddModal] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [setup, setSetup] = useState<SetupStatus | null>(null);

  const load = useCallback(async () => {
    try {
      const list = await api.listClients();
      setClients(list);
    } catch {
      setError("Could not load clients.");
    }
  }, []);

  useEffect(() => {
    load();
    const interval = setInterval(load, 5000);
    return () => clearInterval(interval);
  }, [load]);

  useEffect(() => {
    api.getSetup().then(setSetup).catch(() => {});
  }, []);

  async function removeClient(id: string) {
    if (!confirm("Delete this machine and all its tunnels?")) return;
    await api.deleteClient(id);
    load();
  }

  return (
    <div>
      {setup && !setup.domain && (
        <Link
          href="/setup"
          className="mb-4 flex items-center justify-between rounded-lg border border-border bg-surface px-4 py-2.5 text-sm hover:bg-surface-raised"
        >
          <span className="text-foreground-secondary">
            You&apos;re on <code className="font-mono">{setup.public_ip}</code> — add a
            domain for a proper URL and automatic HTTPS.
          </span>
          <span className="font-medium text-accent">Set up →</span>
        </Link>
      )}

      <div className="flex items-center justify-between">
        <h1 className="text-xl font-semibold tracking-tight">Machines</h1>
        <button
          onClick={() => setShowAddModal(true)}
          className="rounded-lg bg-accent px-4 py-2 text-sm font-medium text-white hover:bg-[color:var(--accent-hover)]"
        >
          Add machine
        </button>
      </div>

      {error && <p className="mt-4 text-sm text-[color:var(--status-critical)]">{error}</p>}

      <div className="mt-6 overflow-hidden rounded-xl border border-border bg-surface">
        {clients === null ? (
          <p className="p-6 text-sm text-foreground-muted">Loading…</p>
        ) : clients.length === 0 ? (
          <div className="p-10 text-center">
            <p className="text-sm text-foreground-secondary">
              No machines yet. Add one to get a one-command installer.
            </p>
          </div>
        ) : (
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-border text-left text-xs uppercase tracking-wide text-foreground-muted">
                <th className="px-4 py-3 font-medium">Name</th>
                <th className="px-4 py-3 font-medium">Status</th>
                <th className="px-4 py-3 font-medium">Last seen</th>
                <th className="px-4 py-3 font-medium"></th>
              </tr>
            </thead>
            <tbody>
              {clients.map((c) => (
                <tr key={c.id} className="border-b border-border last:border-0">
                  <td className="px-4 py-3">
                    <Link
                      href={`/dashboard/clients/${c.id}`}
                      className="font-medium hover:text-accent"
                    >
                      {c.name}
                    </Link>
                  </td>
                  <td className="px-4 py-3">
                    <StatusDot connected={c.connected} />
                  </td>
                  <td className="px-4 py-3 text-foreground-secondary">
                    {timeAgo(c.last_seen)}
                  </td>
                  <td className="px-4 py-3 text-right">
                    <button
                      onClick={() => removeClient(c.id)}
                      className="text-xs text-foreground-muted hover:text-[color:var(--status-critical)]"
                    >
                      Delete
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      {showAddModal && (
        <AddMachineModal onClose={() => setShowAddModal(false)} onCreated={load} />
      )}
    </div>
  );
}

export default function DashboardPage() {
  return (
    <AppShell>
      <DashboardContent />
    </AppShell>
  );
}
