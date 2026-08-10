"use client";

import { FormEvent, useState } from "react";
import { api, ApiError, TunnelProtocol } from "@/lib/api";

export function AddTunnelForm({
  clientId,
  onCreated,
  onClose,
}: {
  clientId: string;
  onCreated: () => void;
  onClose: () => void;
}) {
  const [name, setName] = useState("");
  const [protocol, setProtocol] = useState<TunnelProtocol>("tcp");
  const [localHost, setLocalHost] = useState("127.0.0.1");
  const [localPort, setLocalPort] = useState("");
  const [publicPort, setPublicPort] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    setError(null);
    setSubmitting(true);
    try {
      await api.createTunnel({
        client_id: clientId,
        name: name.trim() || `${localHost}:${localPort}`,
        protocol,
        local_host: localHost.trim() || "127.0.0.1",
        local_port: Number(localPort),
        public_port: Number(publicPort),
      });
      onCreated();
      onClose();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Failed to create tunnel");
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <form
      onSubmit={onSubmit}
      className="rounded-xl border border-border bg-surface p-4"
    >
      <div className="grid grid-cols-2 gap-3 sm:grid-cols-5">
        <div className="col-span-2 sm:col-span-1">
          <label className="block text-xs font-medium mb-1 text-foreground-secondary">Name</label>
          <input
            className="w-full rounded-lg border border-border bg-surface-raised px-2.5 py-1.5 text-sm outline-none focus:ring-2 focus:ring-accent"
            placeholder="minecraft"
            value={name}
            onChange={(e) => setName(e.target.value)}
          />
        </div>
        <div>
          <label className="block text-xs font-medium mb-1 text-foreground-secondary">Protocol</label>
          <select
            className="w-full rounded-lg border border-border bg-surface-raised px-2.5 py-1.5 text-sm outline-none focus:ring-2 focus:ring-accent"
            value={protocol}
            onChange={(e) => setProtocol(e.target.value as TunnelProtocol)}
          >
            <option value="tcp">TCP</option>
            <option value="udp">UDP</option>
          </select>
        </div>
        <div>
          <label className="block text-xs font-medium mb-1 text-foreground-secondary">Local host</label>
          <input
            className="w-full rounded-lg border border-border bg-surface-raised px-2.5 py-1.5 text-sm outline-none focus:ring-2 focus:ring-accent"
            value={localHost}
            onChange={(e) => setLocalHost(e.target.value)}
          />
        </div>
        <div>
          <label className="block text-xs font-medium mb-1 text-foreground-secondary">Local port</label>
          <input
            type="number"
            min={1}
            max={65535}
            required
            className="w-full rounded-lg border border-border bg-surface-raised px-2.5 py-1.5 text-sm outline-none focus:ring-2 focus:ring-accent"
            value={localPort}
            onChange={(e) => setLocalPort(e.target.value)}
          />
        </div>
        <div>
          <label className="block text-xs font-medium mb-1 text-foreground-secondary">Public port</label>
          <input
            type="number"
            min={1}
            max={65535}
            required
            className="w-full rounded-lg border border-border bg-surface-raised px-2.5 py-1.5 text-sm outline-none focus:ring-2 focus:ring-accent"
            value={publicPort}
            onChange={(e) => setPublicPort(e.target.value)}
          />
        </div>
      </div>

      {error && <p className="mt-2 text-sm text-[color:var(--status-critical)]">{error}</p>}

      <div className="mt-3 flex justify-end gap-2">
        <button
          type="button"
          onClick={onClose}
          className="rounded-lg border border-border px-3 py-1.5 text-sm text-foreground-secondary hover:bg-surface-raised"
        >
          Cancel
        </button>
        <button
          type="submit"
          disabled={submitting}
          className="rounded-lg bg-accent px-3 py-1.5 text-sm font-medium text-white hover:bg-[color:var(--accent-hover)] disabled:opacity-50"
        >
          {submitting ? "Creating…" : "Create tunnel"}
        </button>
      </div>
    </form>
  );
}
