"use client";

import { FormEvent, useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import { useAuth } from "@/lib/auth-context";
import { api, ApiError } from "@/lib/api";

export default function LoginPage() {
  const { login } = useAuth();
  const router = useRouter();
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  // A fresh install has no admin account yet — send it to the setup-code
  // bootstrap flow instead of a login form with nothing to log into.
  useEffect(() => {
    api
      .bootstrapStatus()
      .then((s) => {
        if (s.needs_setup) router.replace("/bootstrap");
      })
      .catch(() => {});
  }, [router]);

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    setError(null);
    setSubmitting(true);
    try {
      const me = await login(username, password);
      router.push(me.must_change_password ? "/settings?tab=account&first-login=1" : "/dashboard");
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Login failed");
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <main className="flex flex-1 items-center justify-center px-4">
      <div className="w-full max-w-sm">
        <div className="mb-8 text-center">
          <h1 className="text-2xl font-semibold tracking-tight">Portly</h1>
          <p className="mt-1 text-sm text-foreground-secondary">
            Sign in to manage your tunnels
          </p>
        </div>

        <form
          onSubmit={onSubmit}
          className="rounded-xl border border-border bg-surface p-6 shadow-sm"
        >
          <div className="space-y-4">
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
                autoComplete="current-password"
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
            {submitting ? "Signing in…" : "Sign in"}
          </button>
        </form>
      </div>
    </main>
  );
}
