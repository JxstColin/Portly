"use client";

import { useCallback, useEffect, useState } from "react";
import Link from "next/link";
import { AppShell } from "@/components/AppShell";
import { AddMachineModal } from "@/components/AddMachineModal";
import { ConfirmModal } from "@/components/ConfirmModal";
import { StatusDot } from "@/components/StatusDot";
import { api, Client, SetupStatus, UpdateStatus } from "@/lib/api";

// How often the dashboard re-polls the (cheap, cached) update-status
// endpoint. The panel's own background check against GitHub runs every 15
// minutes server-side (see cmd/portly-server/main.go) — polling this local
// cache once a minute is enough to reflect that promptly without adding
// any real load, since it never itself reaches out to GitHub.
const updateStatusPollInterval = 60_000;

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
  const [reissueClient, setReissueClient] = useState<Client | null>(null);
  const [deleteClient, setDeleteClient] = useState<Client | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [setup, setSetup] = useState<SetupStatus | null>(null);
  const [updateStatus, setUpdateStatus] = useState<UpdateStatus | null>(null);

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

  useEffect(() => {
    const poll = () => api.getUpdateStatus().then(setUpdateStatus).catch(() => {});
    poll();
    const interval = setInterval(poll, updateStatusPollInterval);
    return () => clearInterval(interval);
  }, []);

  async function removeClient(id: string) {
    await api.deleteClient(id);
    load();
  }

  return (
    <div>
      {updateStatus?.update_available && (
        <Link
          href="/settings?tab=updates"
          className="mb-4 flex items-center justify-between rounded-lg border border-[color:var(--accent)]/40 bg-accent/10 px-4 py-2.5 text-sm hover:bg-accent/15"
        >
          <span className="text-foreground-secondary">
            A Portly update is available.
          </span>
          <span className="font-medium text-accent">View →</span>
        </Link>
      )}

      {setup && !setup.domain && (
        <Link
          href="/settings?tab=domain"
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
                    {c.connected ? "just now" : timeAgo(c.last_seen)}
                  </td>
                  <td className="px-4 py-3 text-right">
                    <div className="flex items-center justify-end gap-3">
                      {!c.last_seen && (
                        <button
                          onClick={() => setReissueClient(c)}
                          className="text-xs text-foreground-muted hover:text-accent"
                        >
                          Get install command
                        </button>
                      )}
                      <button
                        onClick={() => setDeleteClient(c)}
                        className="text-xs text-foreground-muted hover:text-[color:var(--status-critical)]"
                      >
                        Delete
                      </button>
                    </div>
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
      {reissueClient && (
        <AddMachineModal
          onClose={() => setReissueClient(null)}
          onCreated={load}
          reissueFor={reissueClient}
        />
      )}
      {deleteClient && (
        <ConfirmModal
          title="Delete machine"
          message={`Delete "${deleteClient.name}" and all its tunnels? This can't be undone.`}
          confirmLabel="Delete"
          destructive
          onConfirm={() => removeClient(deleteClient.id)}
          onClose={() => setDeleteClient(null)}
        />
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
