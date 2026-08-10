"use client";

import { FormEvent, Suspense, useState } from "react";
import { useSearchParams } from "next/navigation";
import { AppShell } from "@/components/AppShell";
import { useAuth } from "@/lib/auth-context";
import { api, ApiError } from "@/lib/api";

function AccountForm() {
  const { user, refresh } = useAuth();
  const searchParams = useSearchParams();
  const firstLogin = searchParams.get("first-login") === "1";

  const [currentPassword, setCurrentPassword] = useState("");
  const [newUsername, setNewUsername] = useState(user?.username ?? "");
  const [newPassword, setNewPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [success, setSuccess] = useState(false);
  const [submitting, setSubmitting] = useState(false);

  async function onSubmit(e: FormEvent) {
    e.preventDefault();
    setError(null);
    setSuccess(false);

    if (newPassword !== confirmPassword) {
      setError("Passwords do not match");
      return;
    }
    if (newPassword.length < 8) {
      setError("Password must be at least 8 characters");
      return;
    }

    setSubmitting(true);
    try {
      await api.changeCredentials({
        current_password: currentPassword,
        new_username: newUsername !== user?.username ? newUsername : undefined,
        new_password: newPassword,
      });
      await refresh();
      setSuccess(true);
      setCurrentPassword("");
      setNewPassword("");
      setConfirmPassword("");
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Failed to update credentials");
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div className="mx-auto max-w-md">
      <h1 className="text-xl font-semibold tracking-tight">Account</h1>

      {firstLogin && (
        <div className="mt-4 rounded-lg border border-[color:var(--status-warning)]/40 bg-[color:var(--status-warning)]/10 px-4 py-3 text-sm">
          You&apos;re using the default password. Choose a new username and
          password before continuing.
        </div>
      )}

      <form
        onSubmit={onSubmit}
        className="mt-6 space-y-4 rounded-xl border border-border bg-surface p-6"
      >
        <div>
          <label className="block text-sm font-medium mb-1.5">Current password</label>
          <input
            type="password"
            className="w-full rounded-lg border border-border bg-surface-raised px-3 py-2 text-sm outline-none focus:ring-2 focus:ring-accent"
            value={currentPassword}
            onChange={(e) => setCurrentPassword(e.target.value)}
            autoComplete="current-password"
            required
          />
        </div>
        <div>
          <label className="block text-sm font-medium mb-1.5">Username</label>
          <input
            className="w-full rounded-lg border border-border bg-surface-raised px-3 py-2 text-sm outline-none focus:ring-2 focus:ring-accent"
            value={newUsername}
            onChange={(e) => setNewUsername(e.target.value)}
            autoComplete="username"
            required
          />
        </div>
        <div>
          <label className="block text-sm font-medium mb-1.5">New password</label>
          <input
            type="password"
            className="w-full rounded-lg border border-border bg-surface-raised px-3 py-2 text-sm outline-none focus:ring-2 focus:ring-accent"
            value={newPassword}
            onChange={(e) => setNewPassword(e.target.value)}
            autoComplete="new-password"
            required
          />
        </div>
        <div>
          <label className="block text-sm font-medium mb-1.5">Confirm new password</label>
          <input
            type="password"
            className="w-full rounded-lg border border-border bg-surface-raised px-3 py-2 text-sm outline-none focus:ring-2 focus:ring-accent"
            value={confirmPassword}
            onChange={(e) => setConfirmPassword(e.target.value)}
            autoComplete="new-password"
            required
          />
        </div>

        {error && <p className="text-sm text-[color:var(--status-critical)]">{error}</p>}
        {success && (
          <p className="text-sm text-[color:var(--status-good-text)]">
            Credentials updated.
          </p>
        )}

        <button
          type="submit"
          disabled={submitting}
          className="w-full rounded-lg bg-accent px-4 py-2 text-sm font-medium text-white transition hover:bg-[color:var(--accent-hover)] disabled:opacity-50"
        >
          {submitting ? "Saving…" : "Save changes"}
        </button>
      </form>
    </div>
  );
}

export default function AccountPage() {
  return (
    <AppShell enforceCredentialChange={false}>
      <Suspense>
        <AccountForm />
      </Suspense>
    </AppShell>
  );
}
