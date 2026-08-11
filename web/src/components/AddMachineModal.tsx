"use client";

import { FormEvent, useEffect, useState } from "react";
import { api, ApiError, Client, CreateClientResult } from "@/lib/api";

export function AddMachineModal({
  onClose,
  onCreated,
  reissueFor,
}: {
  onClose: () => void;
  onCreated: () => void;
  // When set, skip the name-entry step and get a fresh install command for
  // this existing (never-yet-connected) machine instead of creating a new
  // one — for when the original code expired or the dialog got closed
  // before it was ever run.
  reissueFor?: Client;
}) {
  const [name, setName] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(!!reissueFor);
  const [result, setResult] = useState<CreateClientResult | null>(null);
  const [copied, setCopied] = useState(false);
  const [connected, setConnected] = useState(false);

  useEffect(() => {
    if (!reissueFor) return;
    api
      .reissueInstall(reissueFor.id)
      .then((res) => {
        setResult(res);
        onCreated();
      })
      .catch((err) => {
        setError(err instanceof ApiError ? err.message : "Failed to get install command");
      })
      .finally(() => setSubmitting(false));
    // Only ever runs once per mount — reissueFor.id is stable for the
    // lifetime of this modal instance.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    setError(null);
    setSubmitting(true);
    try {
      const res = await api.createClient(name.trim());
      setResult(res);
      onCreated();
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Failed to create client");
    } finally {
      setSubmitting(false);
    }
  }

  // Poll for the machine coming online once the install command has been issued.
  useEffect(() => {
    if (!result) return;
    const interval = setInterval(async () => {
      try {
        const clients = await api.listClients();
        const match = clients.find((c) => c.id === result.client.id);
        if (match?.connected) {
          setConnected(true);
          onCreated();
        }
      } catch {
        // ignore transient errors while polling
      }
    }, 2000);
    return () => clearInterval(interval);
  }, [result, onCreated]);

  function copyCommand() {
    if (!result) return;
    navigator.clipboard.writeText(result.install_command).then(() => {
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    });
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 px-4">
      <div className="w-full max-w-lg rounded-xl border border-border bg-surface p-6 shadow-lg">
        {reissueFor && !result ? (
          <>
            <h2 className="text-lg font-semibold">Install command for {reissueFor.name}</h2>
            {error ? (
              <>
                <p className="mt-2 text-sm text-[color:var(--status-critical)]">{error}</p>
                <div className="mt-5 flex justify-end">
                  <button
                    onClick={onClose}
                    className="rounded-lg border border-border px-4 py-2 text-sm text-foreground-secondary hover:bg-surface-raised"
                  >
                    Close
                  </button>
                </div>
              </>
            ) : (
              <p className="mt-1 text-sm text-foreground-secondary">Getting a fresh one-time code…</p>
            )}
          </>
        ) : !result ? (
          <>
            <h2 className="text-lg font-semibold">Add machine</h2>
            <p className="mt-1 text-sm text-foreground-secondary">
              Name the machine that will run the Portly client (e.g. your
              home server).
            </p>
            <form onSubmit={onSubmit} className="mt-4">
              <input
                autoFocus
                className="w-full rounded-lg border border-border bg-surface-raised px-3 py-2 text-sm outline-none focus:ring-2 focus:ring-accent"
                placeholder="homelab"
                value={name}
                onChange={(e) => setName(e.target.value)}
                pattern="[a-zA-Z0-9][a-zA-Z0-9_\-]{0,63}"
                title="Letters, digits, '-', '_' — up to 64 characters"
                required
              />
              {error && (
                <p className="mt-2 text-sm text-[color:var(--status-critical)]">{error}</p>
              )}
              <div className="mt-5 flex justify-end gap-2">
                <button
                  type="button"
                  onClick={onClose}
                  className="rounded-lg border border-border px-4 py-2 text-sm text-foreground-secondary hover:bg-surface-raised"
                >
                  Cancel
                </button>
                <button
                  type="submit"
                  disabled={submitting}
                  className="rounded-lg bg-accent px-4 py-2 text-sm font-medium text-white hover:bg-[color:var(--accent-hover)] disabled:opacity-50"
                >
                  {submitting ? "Creating…" : "Create"}
                </button>
              </div>
            </form>
          </>
        ) : (
          <>
            <h2 className="text-lg font-semibold">Run this on {result.client.name}</h2>
            <p className="mt-1 text-sm text-foreground-secondary">
              One command — it downloads the client, authenticates, writes
              its config, and starts it as a service. Valid for 15 minutes,
              single use.
            </p>

            <div className="mt-4 flex items-start gap-2 rounded-lg border border-border bg-surface-raised p-3">
              <code className="flex-1 break-all font-mono text-xs leading-relaxed">
                {result.install_command}
              </code>
              <button
                onClick={copyCommand}
                className="shrink-0 rounded-md border border-border px-2 py-1 text-xs hover:bg-surface"
              >
                {copied ? "Copied" : "Copy"}
              </button>
            </div>

            <div className="mt-4 flex items-center gap-2 rounded-lg border border-border px-3 py-2.5 text-sm">
              {connected ? (
                <>
                  <span
                    className="h-2 w-2 rounded-full"
                    style={{ background: "var(--status-good)" }}
                  />
                  <span style={{ color: "var(--status-good-text)" }}>
                    Connected! You can close this dialog.
                  </span>
                </>
              ) : (
                <>
                  <span className="h-2 w-2 animate-pulse rounded-full bg-foreground-muted" />
                  <span className="text-foreground-secondary">
                    Waiting for the machine to connect…
                  </span>
                </>
              )}
            </div>

            <div className="mt-5 flex justify-end">
              <button
                onClick={onClose}
                className="rounded-lg bg-accent px-4 py-2 text-sm font-medium text-white hover:bg-[color:var(--accent-hover)]"
              >
                {connected ? "Done" : "Close"}
              </button>
            </div>
          </>
        )}
      </div>
    </div>
  );
}
