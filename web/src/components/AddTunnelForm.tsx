"use client";

import { FormEvent, useEffect, useState } from "react";
import { api, ApiError, Device, TunnelProtocol } from "@/lib/api";
import { Combobox, ComboboxOption, Select } from "@/components/Dropdown";

const devicePollInterval = 15_000;
// Sanity cap on how many ports a single range can expand to, so a typo
// like "1-65000" doesn't silently fire off tens of thousands of requests.
const maxPortsPerRange = 100;

// Accepts either a single port ("25565") or an inclusive range
// ("25565-25570"). Returns null if the spec doesn't parse or is out of
// bounds, rather than throwing, so callers can turn it into a form error.
function parsePortSpec(spec: string): number[] | null {
  const s = spec.trim();
  const rangeMatch = s.match(/^(\d+)\s*-\s*(\d+)$/);
  if (rangeMatch) {
    const start = Number(rangeMatch[1]);
    const end = Number(rangeMatch[2]);
    if (start < 1 || end > 65535 || start > end) return null;
    if (end - start + 1 > maxPortsPerRange) return null;
    const ports: number[] = [];
    for (let p = start; p <= end; p++) ports.push(p);
    return ports;
  }
  const single = Number(s);
  if (!Number.isInteger(single) || single < 1 || single > 65535) return null;
  return [single];
}

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
  // Starts empty rather than pre-filled with "127.0.0.1" so the Local host
  // combobox shows every suggestion up front instead of immediately
  // filtering them down to whatever matches the default value. The
  // placeholder communicates the default; submit and the backend both
  // still fall back to 127.0.0.1 if this is left blank.
  const [localHost, setLocalHost] = useState("");
  const [localPort, setLocalPort] = useState("");
  const [publicPort, setPublicPort] = useState("");
  // Public port mirrors local port as it's typed — the common case (a
  // Minecraft server on 25565 also wants public 25565) needs no extra
  // typing — but only until the admin actually edits public port
  // themselves, at which point their choice sticks even if they go back
  // and change local port again.
  const [publicPortTouched, setPublicPortTouched] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [devices, setDevices] = useState<Device[]>([]);

  useEffect(() => {
    let cancelled = false;
    async function load() {
      try {
        const d = await api.listDevices(clientId);
        if (!cancelled) setDevices(d);
      } catch {
        // transient — next poll will retry
      }
    }
    load();
    const interval = setInterval(load, devicePollInterval);
    return () => {
      cancelled = true;
      clearInterval(interval);
    };
  }, [clientId]);

  const localHostOptions: ComboboxOption[] = [
    { value: "127.0.0.1", label: "localhost", sublabel: "127.0.0.1" },
    ...devices.map((d) => ({
      value: d.ip,
      label: d.hostname || d.ip,
      sublabel: d.hostname ? d.ip : d.mac,
    })),
  ];

  function onLocalPortChange(v: string) {
    setLocalPort(v);
    if (!publicPortTouched) setPublicPort(v);
  }

  function onPublicPortChange(v: string) {
    setPublicPortTouched(true);
    setPublicPort(v);
  }

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    setError(null);

    const localPorts = parsePortSpec(localPort);
    if (!localPorts) {
      setError(`Local port must be a number (1-65535) or a range like 25565-25570 (max ${maxPortsPerRange} ports)`);
      return;
    }
    const publicPorts = parsePortSpec(publicPort);
    if (!publicPorts) {
      setError(`Public port must be a number (1-65535) or a range like 25565-25570 (max ${maxPortsPerRange} ports)`);
      return;
    }
    if (localPorts.length !== publicPorts.length) {
      setError(
        `Local port range has ${localPorts.length} port(s) but public port range has ${publicPorts.length} — they must be the same length`
      );
      return;
    }

    setSubmitting(true);
    const host = localHost.trim() || "127.0.0.1";
    let createdCount = 0;
    const failures: string[] = [];
    for (let i = 0; i < localPorts.length; i++) {
      const lp = localPorts[i];
      const pp = publicPorts[i];
      const tunnelName = name.trim()
        ? localPorts.length > 1
          ? `${name.trim()} (${lp})`
          : name.trim()
        : `${host}:${lp}`;
      try {
        await api.createTunnel({
          client_id: clientId,
          name: tunnelName,
          protocol,
          local_host: host,
          local_port: lp,
          public_port: pp,
        });
        createdCount++;
      } catch (err) {
        failures.push(`${pp}: ${err instanceof ApiError ? err.message : "failed"}`);
      }
    }
    setSubmitting(false);

    if (createdCount > 0) onCreated();
    if (failures.length === 0) {
      onClose();
    } else if (localPorts.length === 1) {
      setError(failures[0].replace(/^\d+: /, ""));
    } else {
      setError(`Created ${createdCount}/${localPorts.length} tunnels. Failed — ${failures.join("; ")}`);
    }
  }

  return (
    <div className="fixed inset-0 z-50 flex animate-fade-in items-center justify-center bg-black/40 px-4">
      <div className="w-full max-w-2xl animate-scale-in rounded-xl border border-border bg-surface p-6 shadow-lg">
        <h2 className="text-lg font-semibold">Add tunnel</h2>
        <p className="mt-1 text-sm text-foreground-secondary">
          Expose a port on this machine through the tunnel server. Local and
          public port both accept a range (e.g. <code className="font-mono">25565-25570</code>) to
          open several ports at once.
        </p>
        <form onSubmit={onSubmit} className="mt-4">
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
              <Select
                value={protocol}
                onChange={setProtocol}
                options={[
                  { value: "tcp", label: "TCP" },
                  { value: "udp", label: "UDP" },
                ]}
              />
            </div>
            <div>
              <label className="block text-xs font-medium mb-1 text-foreground-secondary">Local host</label>
              <Combobox
                value={localHost}
                onChange={setLocalHost}
                options={localHostOptions}
                placeholder="127.0.0.1"
              />
            </div>
            <div>
              <label className="block text-xs font-medium mb-1 text-foreground-secondary">Local port</label>
              <input
                type="text"
                inputMode="numeric"
                required
                placeholder="25565"
                className="w-full rounded-lg border border-border bg-surface-raised px-2.5 py-1.5 text-sm outline-none focus:ring-2 focus:ring-accent"
                value={localPort}
                onChange={(e) => onLocalPortChange(e.target.value)}
              />
            </div>
            <div>
              <label className="block text-xs font-medium mb-1 text-foreground-secondary">Public port</label>
              <input
                type="text"
                inputMode="numeric"
                required
                placeholder="25565"
                className="w-full rounded-lg border border-border bg-surface-raised px-2.5 py-1.5 text-sm outline-none focus:ring-2 focus:ring-accent"
                value={publicPort}
                onChange={(e) => onPublicPortChange(e.target.value)}
              />
            </div>
          </div>

          {error && <p className="mt-2 text-sm text-[color:var(--status-critical)]">{error}</p>}

          <div className="mt-5 flex justify-end gap-2">
            <button
              type="button"
              onClick={onClose}
              className="rounded-lg border border-border px-4 py-2 text-sm text-foreground-secondary transition-colors hover:bg-surface-raised"
            >
              Cancel
            </button>
            <button
              type="submit"
              disabled={submitting}
              className="rounded-lg bg-accent px-4 py-2 text-sm font-medium text-white transition-colors hover:bg-[color:var(--accent-hover)] disabled:opacity-50"
            >
              {submitting ? "Creating…" : "Create tunnel"}
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}
