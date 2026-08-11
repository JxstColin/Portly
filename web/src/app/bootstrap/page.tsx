"use client";

import { FormEvent, useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import { useAuth } from "@/lib/auth-context";
import { api, ApiError } from "@/lib/api";

export default function BootstrapPage() {
  const { user, loading, refresh } = useAuth();
  const router = useRouter();

  const [checking, setChecking] = useState(true);
  const [setupCode, setSetupCode] = useState("");
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  // Already set up (or already logged in)? This page has nothing to do.
  useEffect(() => {
    if (loading) return;
    if (user) {
      router.replace("/dashboard");
      return;
    }
    api
      .bootstrapStatus()
      .then((s) => {
        if (!s.needs_setup) router.replace("/login");
        else setChecking(false);
      })
      .catch(() => setChecking(false));
  }, [loading, user, router]);

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    setError(null);

    if (password !== confirmPassword) {
      setError("Passwords do not match");
      return;
    }
    if (password.length < 8) {
      setError("Password must be at least 8 characters");
      return;
    }

    setSubmitting(true);
    try {
      await api.bootstrapClaim({
        setup_code: setupCode.trim(),
        username: username.trim(),
        password,
      });
      await refresh();
      router.push("/dashboard");
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Setup failed");
    } finally {
      setSubmitting(false);
    }
  }

  if (checking) {
    return (
      <main className="flex flex-1 items-center justify-center">
        <p className="text-sm text-foreground-muted">Loading…</p>
      </main>
    );
  }

  return (
    <main className="flex flex-1 items-center justify-center px-4">
      <div className="w-full max-w-sm">
        <div className="mb-8 text-center">
          <h1 className="text-2xl font-semibold tracking-tight">Portly</h1>
          <p className="mt-1 text-sm text-foreground-secondary">
            First time here — let&apos;s create your admin account
          </p>
        </div>

        <form
          onSubmit={onSubmit}
          className="rounded-xl border border-border bg-surface p-6 shadow-sm"
        >
          <div className="space-y-4">
            <div>
              <label className="block text-sm font-medium mb-1.5" htmlFor="setup-code">
                Setup code
              </label>
              <input
                id="setup-code"
                className="w-full rounded-lg border border-border bg-surface-raised px-3 py-2 text-sm font-mono outline-none focus:ring-2 focus:ring-accent"
                value={setupCode}
                onChange={(e) => setSetupCode(e.target.value)}
                placeholder="printed by portly-server on first run"
                autoComplete="off"
                required
              />
              <p className="mt-1.5 text-xs text-foreground-muted">
                Find it via <code className="font-mono">journalctl -u portly-server</code>{" "}
                or <code className="font-mono">cat /var/lib/portly/setup-code.txt</code>{" "}
                on the server.
              </p>
            </div>
            <div>
              <label className="block text-sm font-medium mb-1.5" htmlFor="username">
                Username
              </label>
              <input
                id="username"
                className="w-full rounded-lg border border-border bg-surface-raised px-3 py-2 text-sm outline-none focus:ring-2 focus:ring-accent"
                value={username}
                onChange={(e) => setUsername(e.target.value)}
                autoComplete="username"
                required
              />
            </div>
            <div>
              <label className="block text-sm font-medium mb-1.5" htmlFor="password">
                Password
              </label>
              <input
                id="password"
                type="password"
                className="w-full rounded-lg border border-border bg-surface-raised px-3 py-2 text-sm outline-none focus:ring-2 focus:ring-accent"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                autoComplete="new-password"
                required
              />
            </div>
            <div>
              <label className="block text-sm font-medium mb-1.5" htmlFor="confirm-password">
                Confirm password
              </label>
              <input
                id="confirm-password"
                type="password"
                className="w-full rounded-lg border border-border bg-surface-raised px-3 py-2 text-sm outline-none focus:ring-2 focus:ring-accent"
                value={confirmPassword}
                onChange={(e) => setConfirmPassword(e.target.value)}
                autoComplete="new-password"
                required
              />
            </div>
          </div>

          {error && (
            <p className="mt-4 text-sm text-[color:var(--status-critical)]">{error}</p>
          )}

          <button
            type="submit"
            disabled={submitting}
            className="mt-6 w-full rounded-lg bg-accent px-4 py-2 text-sm font-medium text-white transition hover:bg-[color:var(--accent-hover)] disabled:opacity-50"
          >
            {submitting ? "Setting up…" : "Create admin account"}
          </button>
        </form>
      </div>
    </main>
  );
}
