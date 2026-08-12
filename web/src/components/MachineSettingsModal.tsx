"use client";

import { FormEvent, useState } from "react";
import { api, ApiError, Client } from "@/lib/api";

const gib = 1024 * 1024 * 1024;

function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  if (n < gib) return `${(n / 1024 / 1024).toFixed(1)} MB`;
  return `${(n / gib).toFixed(2)} GB`;
}

export function MachineSettingsModal({
  client,
  totalBytes,
  onClose,
  onUpdated,
}: {
  client: Client;
  totalBytes: number;
  onClose: () => void;
  onUpdated: () => void;
}) {
  const [limitGB, setLimitGB] = useState(
    client.traffic_limit_bytes ? String(client.traffic_limit_bytes / gib) : ""
  );
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  const limitBytesPreview = limitGB.trim() ? Math.round(Number(limitGB) * gib) : null;
  const usagePct =
    limitBytesPreview && limitBytesPreview > 0 ? Math.min(100, (totalBytes / limitBytesPreview) * 100) : null;

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
      await api.updateClientSettings(client.id, { traffic_limit_bytes: limitBytes });
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
      <div className="w-full max-w-md animate-scale-in rounded-xl border border-border bg-surface p-6 shadow-lg">
        <h2 className="text-lg font-semibold">Settings for {client.name}</h2>
        <p className="mt-1 text-sm text-foreground-secondary">
          {formatBytes(totalBytes)} used across all of this machine&apos;s tunnels.
        </p>

        <form onSubmit={onSubmit} className="mt-4 space-y-4">
          <div>
            <label className="block text-xs font-medium mb-1 text-foreground-secondary">
              Machine-wide traffic limit (GB)
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
                    ? "Limit reached — every tunnel on this machine disables itself automatically once this happens."
                    : `${usagePct.toFixed(1)}% of the limit used.`}
                </p>
              </div>
            )}
            <p className="mt-1 text-xs text-foreground-muted">
              Applies across every tunnel on this machine combined, on top of
              any limit set on an individual tunnel. Leave blank for no limit.
            </p>
          </div>

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
