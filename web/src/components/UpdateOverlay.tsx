"use client";

import { useEffect, useRef, useState } from "react";
import { api, UpdateProgress } from "@/lib/api";

const pollIntervalMs = 2000;

// Global, unauthenticated-by-necessity "Portly is updating" screen. It has
// to work without a session because an update restarts portly-server
// partway through, which wipes every in-memory session (see
// internal/api/server.go's Server.sessions) — without this, refreshing the
// panel mid-update would just bounce the admin to the login page instead of
// showing them what's actually happening. Mounted once in the root layout
// so it covers every page, including /login.
export function UpdateOverlay() {
  const [progress, setProgress] = useState<UpdateProgress | null>(null);
  const [dismissed, setDismissed] = useState(false);
  const reloadingRef = useRef(false);
  const logRef = useRef<HTMLPreElement>(null);

  useEffect(() => {
    let cancelled = false;
    let timer: ReturnType<typeof setTimeout>;

    async function poll() {
      try {
        const next = await api.getUpdateProgress();
        if (cancelled) return;
        setProgress((prev) => {
          if (prev?.status === "running" && next.status === "done" && !reloadingRef.current) {
            reloadingRef.current = true;
            // A fresh load re-runs auth (the session is gone) and picks up
            // whatever the update just shipped, instead of trying to
            // reconcile in-memory state against a server that changed out
            // from under this page.
            setTimeout(() => window.location.reload(), 1200);
          }
          return next;
        });
      } catch {
        // portly-server is very likely mid-restart right now — that's the
        // whole reason this screen exists. Keep whatever we last knew and
        // just try again rather than treating this as "no update running".
      } finally {
        if (!cancelled) timer = setTimeout(poll, pollIntervalMs);
      }
    }

    poll();
    return () => {
      cancelled = true;
      clearTimeout(timer);
    };
  }, []);

  useEffect(() => {
    if (logRef.current) logRef.current.scrollTop = logRef.current.scrollHeight;
  }, [progress?.log_tail]);

  if (dismissed || !progress) return null;
  if (progress.status !== "running" && progress.status !== "failed") return null;

  const pct =
    progress.stage && progress.total_stages
      ? Math.min(100, Math.round((progress.stage / progress.total_stages) * 100))
      : 0;

  return (
    <div className="fixed inset-0 z-[200] flex animate-fade-in items-center justify-center bg-black/70 px-4 backdrop-blur-sm">
      <div className="w-full max-w-lg animate-scale-in rounded-xl border border-border bg-surface p-6 shadow-2xl">
        <h1 className="text-lg font-semibold">
          {progress.status === "failed" ? "Update failed" : "Portly is updating"}
        </h1>
        <p className="mt-1 text-sm text-foreground-secondary">
          {progress.status === "failed"
            ? "Something went wrong partway through. Check the output below, or SSH in and check journalctl -u portly-update."
            : "The panel will be briefly unreachable while this finishes, then this page reloads automatically."}
        </p>

        {progress.status === "running" && (
          <>
            <div className="mt-4 h-2 w-full overflow-hidden rounded-full bg-surface-raised">
              <div
                className="h-full rounded-full bg-accent transition-all duration-500"
                style={{ width: `${pct}%` }}
              />
            </div>
            <p className="mt-2 text-xs text-foreground-muted">
              {progress.label ?? "Working…"}
              {progress.stage && progress.total_stages
                ? ` (${progress.stage}/${progress.total_stages})`
                : ""}
            </p>
          </>
        )}

        {progress.log_tail && (
          <pre
            ref={logRef}
            className="mt-4 max-h-56 overflow-auto whitespace-pre-wrap break-all rounded-lg bg-surface-raised p-3 font-mono text-xs text-foreground-secondary"
          >
            {progress.log_tail}
          </pre>
        )}

        {progress.status === "failed" && (
          <button
            onClick={() => setDismissed(true)}
            className="mt-4 rounded-lg border border-border px-3 py-1.5 text-sm text-foreground-secondary transition-colors hover:bg-surface-raised"
          >
            Dismiss
          </button>
        )}
      </div>
    </div>
  );
}
