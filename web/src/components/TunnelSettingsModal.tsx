"use client";

import { FormEvent, useEffect, useState } from "react";
import { api, ApiError, ServerInfo, Tunnel } from "@/lib/api";

const gib = 1024 * 1024 * 1024;
const ipv4Pattern = /^\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}$/;
// Loose hostname check — this is just gating whether we show DNS setup
// instructions at all, the real validation is "does it resolve", which
// only the admin's DNS provider can tell them.
const hostnamePattern = /^[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)+$/;

function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  if (n < gib) return `${(n / 1024 / 1024).toFixed(1)} MB`;
  return `${(n / gib).toFixed(2)} GB`;
}

export function TunnelSettingsModal({
  tunnel,
  onClose,
  onUpdated,
}: {
  tunnel: Tunnel;
  onClose: () => void;
  onUpdated: () => void;
}) {
  const [limitGB, setLimitGB] = useState(
    tunnel.traffic_limit_bytes ? String(tunnel.traffic_limit_bytes / gib) : ""
  );
  const [hostname, setHostname] = useState(tunnel.public_hostname ?? "");
  const [serverInfo, setServerInfo] = useState<ServerInfo | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  useEffect(() => {
    api.serverInfo().then(setServerInfo).catch(() => {});
  }, []);

  const usedBytes = tunnel.bytes_in_total + tunnel.bytes_out_total;
  const limitBytesPreview = limitGB.trim() ? Math.round(Number(limitGB) * gib) : null;
  const usagePct =
    limitBytesPreview && limitBytesPreview > 0 ? Math.min(100, (usedBytes / limitBytesPreview) * 100) : null;

  const trimmedHostname = hostname.trim().toLowerCase();
  const showDNSHelp = trimmedHostname !== "" && hostnamePattern.test(trimmedHostname);
  const recordType = serverInfo && ipv4Pattern.test(serverInfo.advertise_host) ? "A" : "CNAME";

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    setError(null);

    let limitBytes: number | null = null;
    if (limitGB.trim()) {
      const gb = Number(limitGB);
      if (!Number.isFinite(gb) || gb <= 0) {
        setError("Traffic limit must be a positive number of GB, or left blank for unlimited");
        return;
      }
      limitBytes = Math.round(gb * gib);
    }

    setSubmitting(true);
    try {
      await api.updateTunnelSettings(tunnel.id, {
        traffic_limit_bytes: limitBytes,
        public_hostname: trimmedHostname,
      });
      onUpdated();
      onClose();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Failed to save settings");
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div className="fixed inset-0 z-50 flex animate-fade-in items-center justify-center bg-black/40 px-4">
      <div className="w-full max-w-lg animate-scale-in rounded-xl border border-border bg-surface p-6 shadow-lg">
        <h2 className="text-lg font-semibold">Settings for {tunnel.name}</h2>
        <p className="mt-1 text-sm text-foreground-secondary">
          {formatBytes(usedBytes)} used so far.
        </p>

        <form onSubmit={onSubmit} className="mt-4 space-y-4">
          <div>
            <label className="block text-xs font-medium mb-1 text-foreground-secondary">
              Traffic limit (GB)
            </label>
            <input
              type="text"
              inputMode="decimal"
              placeholder="unlimited"
              className="w-full rounded-lg border border-border bg-surface-raised px-2.5 py-1.5 text-sm outline-none focus:ring-2 focus:ring-accent"
              value={limitGB}
              onChange={(e) => setLimitGB(e.target.value)}
            />
            {usagePct !== null && (
              <div className="mt-2">
                <div className="h-1.5 w-full overflow-hidden rounded-full bg-surface-raised">
                  <div
                    className="h-full rounded-full transition-all duration-300"
                    style={{
                      width: `${usagePct}%`,
                      background: usagePct >= 100 ? "var(--status-critical)" : "var(--accent)",
                    }}
                  />
                </div>
                <p className="mt-1 text-xs text-foreground-muted">
                  {usagePct >= 100
                    ? "Limit reached — the tunnel disables itself automatically once this happens."
                    : `${usagePct.toFixed(1)}% of the limit used.`}
                </p>
              </div>
            )}
            <p className="mt-1 text-xs text-foreground-muted">
              Once reached, this tunnel is disabled automatically. Leave blank for no limit.
            </p>
          </div>

          <div>
            <label className="block text-xs font-medium mb-1 text-foreground-secondary">
              Public hostname (optional)
            </label>
            <input
              type="text"
              placeholder="minecraft.example.com"
              className="w-full rounded-lg border border-border bg-surface-raised px-2.5 py-1.5 text-sm outline-none focus:ring-2 focus:ring-accent"
              value={hostname}
              onChange={(e) => setHostname(e.target.value)}
            />
            <p className="mt-1 text-xs text-foreground-muted">
              Portly doesn&apos;t manage DNS — set this to get the exact records to
              create yourself, so people can connect via a domain instead of{" "}
              <code className="font-mono">{serverInfo?.advertise_host ?? "this server"}:{tunnel.public_port}</code>.
            </p>
          </div>

          {showDNSHelp && serverInfo && (
            <div className="animate-fade-in rounded-lg border border-border bg-surface-raised p-3 text-xs">
              <p className="text-foreground-secondary">
                1. Point a <strong>{recordType}</strong> record for{" "}
                <code className="font-mono">{trimmedHostname}</code> at{" "}
                <code className="font-mono">{serverInfo.advertise_host}</code>.
              </p>
              {tunnel.protocol === "tcp" && (
                <p className="mt-2 text-foreground-secondary">
                  2. Optional — for protocols with SRV-record support (e.g. Minecraft),
                  also add:
                  <code className="mt-1 block break-all rounded bg-surface px-2 py-1.5 font-mono">
                    {`_minecraft._tcp.${trimmedHostname}. IN SRV 0 5 ${tunnel.public_port} ${trimmedHostname}.`}
                  </code>
                  so people can connect to just{" "}
                  <code className="font-mono">{trimmedHostname}</code>{" "}
                  without adding <code className="font-mono">:{tunnel.public_port}</code>{" "}
                  — swap <code className="font-mono">_minecraft</code> for
                  whatever service name your game/client actually looks up.
                </p>
              )}
            </div>
          )}

          {error && <p className="text-sm text-[color:var(--status-critical)]">{error}</p>}

          <div className="flex justify-end gap-2">
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
              {submitting ? "Saving…" : "Save settings"}
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}
