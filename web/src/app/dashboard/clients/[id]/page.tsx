"use client";

import { use, useCallback, useEffect, useState } from "react";
import Link from "next/link";
import { AppShell } from "@/components/AppShell";
import { StatusDot } from "@/components/StatusDot";
import { AddTunnelForm } from "@/components/AddTunnelForm";
import { TrafficChart } from "@/components/TrafficChart";
import {
  api,
  Client,
  connectLiveWS,
  LiveTunnelStat,
  Tunnel,
  TrafficSample,
} from "@/lib/api";

function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  if (n < 1024 * 1024 * 1024) return `${(n / 1024 / 1024).toFixed(1)} MB`;
  return `${(n / 1024 / 1024 / 1024).toFixed(2)} GB`;
}

function formatRate(bps: number): string {
  if (bps < 1024) return `${bps} B/s`;
  if (bps < 1024 * 1024) return `${(bps / 1024).toFixed(1)} KB/s`;
  return `${(bps / 1024 / 1024).toFixed(1)} MB/s`;
}

function ClientDetail({ clientId }: { clientId: string }) {
  const [client, setClient] = useState<Client | null>(null);
  const [tunnels, setTunnels] = useState<Tunnel[]>([]);
  const [liveByTunnel, setLiveByTunnel] = useState<Record<string, LiveTunnelStat>>({});
  const [showAddTunnel, setShowAddTunnel] = useState(false);
  const [selectedTunnelId, setSelectedTunnelId] = useState<string | null>(null);
  const [samples, setSamples] = useState<TrafficSample[]>([]);

  const load = useCallback(async () => {
    const [clients, tunnelList] = await Promise.all([
      api.listClients(),
      api.listTunnels(clientId),
    ]);
    setClient(clients.find((c) => c.id === clientId) ?? null);
    setTunnels(tunnelList);
    setSelectedTunnelId((prev) => prev ?? tunnelList[0]?.id ?? null);
  }, [clientId]);

  useEffect(() => {
    load();
    const interval = setInterval(load, 5000);
    return () => clearInterval(interval);
  }, [load]);

  useEffect(() => {
    const disconnect = connectLiveWS((tick) => {
      const byId: Record<string, LiveTunnelStat> = {};
      for (const t of tick.tunnels) byId[t.id] = t;
      setLiveByTunnel(byId);
    });
    return disconnect;
  }, []);

  useEffect(() => {
    if (!selectedTunnelId) {
      setSamples([]);
      return;
    }
    const since = Math.floor(Date.now() / 1000) - 3600;
    const fetchSamples = () =>
      api
        .tunnelTraffic(selectedTunnelId, since)
        .then((s) => setSamples(s ?? []))
        .catch(() => setSamples([]));
    fetchSamples();
    const interval = setInterval(fetchSamples, 10000);
    return () => clearInterval(interval);
  }, [selectedTunnelId]);

  async function toggleTunnel(t: Tunnel) {
    await api.setTunnelEnabled(t.id, !t.enabled);
    load();
  }

  async function removeTunnel(id: string) {
    if (!confirm("Delete this tunnel?")) return;
    await api.deleteTunnel(id);
    if (selectedTunnelId === id) setSelectedTunnelId(null);
    load();
  }

  return (
    <div>
      <Link href="/dashboard" className="text-sm text-foreground-muted hover:text-accent">
        ← Machines
      </Link>

      <div className="mt-2 flex items-center justify-between">
        <div>
          <h1 className="text-xl font-semibold tracking-tight">
            {client?.name ?? "…"}
          </h1>
          {client && (
            <div className="mt-1">
              <StatusDot connected={client.connected} />
            </div>
          )}
        </div>
        <button
          onClick={() => setShowAddTunnel((v) => !v)}
          className="rounded-lg bg-accent px-4 py-2 text-sm font-medium text-white hover:bg-[color:var(--accent-hover)]"
        >
          {showAddTunnel ? "Close" : "Add tunnel"}
        </button>
      </div>

      {showAddTunnel && (
        <div className="mt-4">
          <AddTunnelForm
            clientId={clientId}
            onCreated={load}
            onClose={() => setShowAddTunnel(false)}
          />
        </div>
      )}

      <div className="mt-6 overflow-hidden rounded-xl border border-border bg-surface">
        {tunnels.length === 0 ? (
          <p className="p-6 text-sm text-foreground-secondary">
            No tunnels yet for this machine.
          </p>
        ) : (
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-border text-left text-xs uppercase tracking-wide text-foreground-muted">
                <th className="px-4 py-3 font-medium">Name</th>
                <th className="px-4 py-3 font-medium">Local target</th>
                <th className="px-4 py-3 font-medium">Public port</th>
                <th className="px-4 py-3 font-medium">Live rate</th>
                <th className="px-4 py-3 font-medium">Total</th>
                <th className="px-4 py-3 font-medium">Enabled</th>
                <th className="px-4 py-3 font-medium"></th>
              </tr>
            </thead>
            <tbody>
              {tunnels.map((t) => {
                const live = liveByTunnel[t.id];
                return (
                  <tr
                    key={t.id}
                    onClick={() => setSelectedTunnelId(t.id)}
                    className={`cursor-pointer border-b border-border transition last:border-0 hover:bg-surface-raised ${
                      selectedTunnelId === t.id ? "bg-surface-raised" : ""
                    }`}
                  >
                    <td className="px-4 py-3 font-medium">{t.name}</td>
                    <td className="px-4 py-3 text-foreground-secondary">
                      {t.local_host}:{t.local_port}
                    </td>
                    <td className="px-4 py-3 text-foreground-secondary">:{t.public_port}</td>
                    <td className="px-4 py-3 text-xs">
                      <span style={{ color: "var(--series-1)" }}>
                        ↓{formatRate(live?.rate_in_bps ?? 0)}
                      </span>{" "}
                      <span style={{ color: "var(--series-2)" }}>
                        ↑{formatRate(live?.rate_out_bps ?? 0)}
                      </span>
                    </td>
                    <td className="px-4 py-3 text-foreground-secondary">
                      {formatBytes(t.bytes_in_total + t.bytes_out_total)}
                    </td>
                    <td className="px-4 py-3" onClick={(e) => e.stopPropagation()}>
                      <button
                        onClick={() => toggleTunnel(t)}
                        className={`rounded-full px-2.5 py-1 text-xs font-medium ${
                          t.enabled
                            ? "bg-[color:var(--status-good)]/15 text-[color:var(--status-good-text)]"
                            : "bg-foreground-muted/15 text-foreground-muted"
                        }`}
                      >
                        {t.enabled ? "Enabled" : "Disabled"}
                      </button>
                    </td>
                    <td className="px-4 py-3 text-right" onClick={(e) => e.stopPropagation()}>
                      <button
                        onClick={() => removeTunnel(t.id)}
                        className="text-xs text-foreground-muted hover:text-[color:var(--status-critical)]"
                      >
                        Delete
                      </button>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        )}
      </div>

      {selectedTunnelId && (
        <div className="mt-6">
          <h2 className="mb-2 text-sm font-medium text-foreground-secondary">
            Traffic — last hour ({tunnels.find((t) => t.id === selectedTunnelId)?.name})
          </h2>
          <TrafficChart samples={samples} />
        </div>
      )}
    </div>
  );
}

export default function ClientDetailPage({
  params,
}: {
  params: Promise<{ id: string }>;
}) {
  const { id } = use(params);
  return (
    <AppShell>
      <ClientDetail clientId={id} />
    </AppShell>
  );
}
