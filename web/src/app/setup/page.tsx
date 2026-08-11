"use client";

import { FormEvent, useCallback, useEffect, useState } from "react";
import { AppShell } from "@/components/AppShell";
import { api, ApiError, SetupStatus } from "@/lib/api";

function CertBadge({ status }: { status: SetupStatus }) {
  if (!status.domain) return null;

  const map: Record<string, { label: string; color: string }> = {
    pending: { label: "Requesting certificate…", color: "var(--status-warning)" },
    ready: { label: "HTTPS active", color: "var(--status-good)" },
    error: { label: "Certificate failed", color: "var(--status-critical)" },
  };
  const info = map[status.cert_state ?? ""] ?? null;
  if (!info) return null;

  return (
    <span className="inline-flex items-center gap-1.5 text-sm">
      <span
        className={`h-2 w-2 rounded-full ${status.cert_state === "pending" ? "animate-pulse" : ""}`}
        style={{ background: info.color }}
      />
      <span style={{ color: info.color }}>{info.label}</span>
    </span>
  );
}

function SetupContent() {
  const [status, setStatus] = useState<SetupStatus | null>(null);
  const [domainInput, setDomainInput] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  const load = useCallback(async () => {
    try {
      const s = await api.getSetup();
      setStatus(s);
      setDomainInput((prev) => (prev ? prev : s.domain ?? ""));
    } catch {
      // transient — next poll will retry
    }
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  useEffect(() => {
    if (status?.cert_state !== "pending") return;
    const interval = setInterval(load, 3000);
    return () => clearInterval(interval);
  }, [status?.cert_state, load]);

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    setError(null);
    setSubmitting(true);
    try {
      await api.setDomain(domainInput.trim());
      await load();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Failed to save domain");
    } finally {
      setSubmitting(false);
    }
  }

  async function onClear() {
    setSubmitting(true);
    try {
      await api.setDomain("");
      setDomainInput("");
      await load();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Failed to clear domain");
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div className="mx-auto max-w-xl">
      <h1 className="text-xl font-semibold tracking-tight">Setup</h1>
      <p className="mt-1 text-sm text-foreground-secondary">
        Point a domain at this server for a proper URL and automatic HTTPS,
        or skip this and keep using the plain IP address.
      </p>

      <div className="mt-6 rounded-xl border border-border bg-surface p-6">
        <div className="text-sm">
          <span className="text-foreground-secondary">Detected public IPv4: </span>
          <code className="font-mono">{status?.public_ip ?? "…"}</code>
        </div>
        {status?.public_ipv6 && (
          <div className="mt-1 text-sm">
            <span className="text-foreground-secondary">Detected public IPv6: </span>
            <code className="font-mono">{status.public_ipv6}</code>
          </div>
        )}

        <div className="mt-4 rounded-lg border border-border bg-surface-raised p-3 text-xs text-foreground-secondary">
          Create an <strong>A</strong> record for your domain pointing at{" "}
          <code className="font-mono">{status?.public_ip ?? "the IPv4 address above"}</code> —
          that&apos;s the one that matters, since not every machine you&apos;ll install
          Portly&apos;s client on has IPv6.
          {status?.public_ipv6 && (
            <>
              {" "}Optionally also add an <strong>AAAA</strong> record pointing at{" "}
              <code className="font-mono">{status.public_ipv6}</code> if you want IPv6
              reachability too.
            </>
          )}{" "}
          Portly requests the certificate immediately after you submit, and
          it&apos;ll only succeed once DNS resolves.
        </div>

        <form onSubmit={onSubmit} className="mt-4">
          <label className="block text-sm font-medium mb-1.5">Domain</label>
          <div className="flex gap-2">
            <input
              className="flex-1 rounded-lg border border-border bg-surface-raised px-3 py-2 text-sm outline-none focus:ring-2 focus:ring-accent"
              placeholder="panel.example.com"
              value={domainInput}
              onChange={(e) => setDomainInput(e.target.value)}
            />
            <button
              type="submit"
              disabled={submitting || !domainInput.trim()}
              className="rounded-lg bg-accent px-4 py-2 text-sm font-medium text-white hover:bg-[color:var(--accent-hover)] disabled:opacity-50"
            >
              {submitting ? "Saving…" : "Activate"}
            </button>
          </div>

          {error && <p className="mt-2 text-sm text-[color:var(--status-critical)]">{error}</p>}
          {status?.cert_state === "error" && status.cert_error && (
            <p className="mt-2 text-sm text-[color:var(--status-critical)]">
              {status.cert_error}
            </p>
          )}
        </form>

        {status?.domain && (
          <div className="mt-4 flex items-center justify-between border-t border-border pt-4">
            <div>
              <p className="text-sm">
                Current domain: <code className="font-mono">{status.domain}</code>
              </p>
              <div className="mt-1">
                <CertBadge status={status} />
              </div>
            </div>
            <button
              onClick={onClear}
              disabled={submitting}
              className="text-xs text-foreground-muted hover:text-[color:var(--status-critical)]"
            >
              Remove domain
            </button>
          </div>
        )}

        {status?.cert_state === "ready" && status.domain && (
          <p className="mt-4 text-sm text-foreground-secondary">
            Reachable at{" "}
            <a
              href={`https://${status.domain}`}
              className="text-accent hover:underline"
            >
              https://{status.domain}
            </a>
            .
          </p>
        )}
      </div>
    </div>
  );
}

export default function SetupPage() {
  return (
    <AppShell>
      <SetupContent />
    </AppShell>
  );
}
