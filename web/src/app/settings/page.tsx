"use client";

import { FormEvent, Suspense, useCallback, useEffect, useRef, useState } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { AppShell } from "@/components/AppShell";
import { useAuth } from "@/lib/auth-context";
import { api, ApiError, SetupStatus, UpdateStatus } from "@/lib/api";

type Tab = "account" | "domain" | "updates";

function TabLink({
  tab,
  active,
  onClick,
  children,
}: {
  tab: Tab;
  active: boolean;
  onClick: (tab: Tab) => void;
  children: React.ReactNode;
}) {
  return (
    <button
      type="button"
      onClick={() => onClick(tab)}
      className={`border-b-2 px-1 pb-3 text-sm font-medium transition ${
        active
          ? "border-accent text-foreground"
          : "border-transparent text-foreground-secondary hover:text-foreground"
      }`}
    >
      {children}
    </button>
  );
}

function AccountTab({ firstLogin }: { firstLogin: boolean }) {
  const { user, refresh } = useAuth();

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
      {firstLogin && (
        <div className="mb-6 rounded-lg border border-[color:var(--status-warning)]/40 bg-[color:var(--status-warning)]/10 px-4 py-3 text-sm">
          You&apos;re using the default password. Choose a new username and
          password before continuing.
        </div>
      )}

      <form
        onSubmit={onSubmit}
        className="space-y-4 rounded-xl border border-border bg-surface p-6"
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

      <DangerZone />
    </div>
  );
}

function DangerZone() {
  const router = useRouter();
  const { refresh } = useAuth();
  const [open, setOpen] = useState(false);
  const [confirmText, setConfirmText] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  async function onReset() {
    setError(null);
    setSubmitting(true);
    try {
      await api.factoryReset(confirmText.trim());
      // The admin account (and this session) is gone server-side, but the
      // auth context doesn't know that yet — refresh it (this 401s and
      // clears the cached user) before bouncing through the root page,
      // which only sends us to /bootstrap once it sees no user.
      await refresh();
      router.push("/");
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Reset failed");
      setSubmitting(false);
    }
  }

  return (
    <div className="mt-8 rounded-xl border border-[color:var(--status-critical)]/40 p-6">
      <h2 className="text-sm font-semibold text-[color:var(--status-critical)]">Danger zone</h2>
      <p className="mt-1.5 text-sm text-foreground-secondary">
        Factory reset permanently deletes every machine, tunnel, and traffic
        history, and removes the admin account — connected machines are
        told to uninstall themselves first. This server goes back to a
        blank first-run state; you&apos;ll set up a new admin account with a
        fresh setup code afterwards.
      </p>

      {!open ? (
        <button
          onClick={() => setOpen(true)}
          className="mt-4 rounded-lg border border-[color:var(--status-critical)]/40 px-3 py-1.5 text-sm text-[color:var(--status-critical)] hover:bg-[color:var(--status-critical)]/10"
        >
          Factory reset…
        </button>
      ) : (
        <div className="mt-4 rounded-lg border border-border bg-surface-raised p-3">
          <label className="block text-xs font-medium mb-1.5">
            Type <code className="font-mono">RESET</code> to confirm
          </label>
          <input
            className="w-full rounded-lg border border-border bg-surface px-3 py-2 text-sm outline-none focus:ring-2 focus:ring-[color:var(--status-critical)]"
            value={confirmText}
            onChange={(e) => setConfirmText(e.target.value)}
            autoComplete="off"
          />
          {error && <p className="mt-2 text-sm text-[color:var(--status-critical)]">{error}</p>}
          <div className="mt-3 flex justify-end gap-2">
            <button
              onClick={() => {
                setOpen(false);
                setConfirmText("");
                setError(null);
              }}
              className="rounded-lg border border-border px-3 py-1.5 text-sm text-foreground-secondary hover:bg-surface"
            >
              Cancel
            </button>
            <button
              onClick={onReset}
              disabled={confirmText !== "RESET" || submitting}
              className="rounded-lg bg-[color:var(--status-critical)] px-3 py-1.5 text-sm font-medium text-white hover:opacity-90 disabled:opacity-50"
            >
              {submitting ? "Resetting…" : "Factory reset"}
            </button>
          </div>
        </div>
      )}
    </div>
  );
}

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

function DomainTab() {
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
      <p className="text-sm text-foreground-secondary">
        Point a domain at this server for a proper URL and automatic HTTPS,
        or skip this and keep using the plain IP address.
      </p>

      <div className="mt-4 rounded-xl border border-border bg-surface p-6">
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

const updatePollInterval = 5_000;
const updatePollTimeoutMs = 5 * 60_000;
// How often this tab re-polls the (cheap, cached) update-status endpoint in
// the background, so "update available" shows up here without needing a
// manual "Check now" or a page reload. Same cadence as the dashboard
// banner's poll — see web/src/app/dashboard/page.tsx.
const updateStatusPollInterval = 60_000;

function shortCommit(sha?: string): string {
  if (!sha) return "unknown";
  return sha === "dev" ? "dev build" : sha.slice(0, 7);
}

function UpdatesTab() {
  const [status, setStatus] = useState<UpdateStatus | null>(null);
  const [checking, setChecking] = useState(false);
  const [applying, setApplying] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // The background poll below and an explicit "Check now" click both hit
  // the network and can resolve in either order — e.g. a poll tick fired
  // just before a click can still be in flight when the click's own
  // response lands, and land after it. Without this guard that stale poll
  // response clobbers the fresh manual-check result — from the user's
  // side, that looks like "Check now" doing nothing until clicked again.
  // requestSeq tracks the most recently *dispatched* request so a response
  // is only applied if nothing newer has been fired since, regardless of
  // arrival order.
  const requestSeq = useRef(0);
  const applyStatus = useCallback((seq: number, s: UpdateStatus) => {
    if (seq === requestSeq.current) setStatus(s);
  }, []);

  const load = useCallback(async () => {
    const seq = ++requestSeq.current;
    try {
      const s = await api.getUpdateStatus();
      applyStatus(seq, s);
      return s;
    } catch {
      return null;
    }
  }, [applyStatus]);

  useEffect(() => {
    load();
    const interval = setInterval(load, updateStatusPollInterval);
    return () => clearInterval(interval);
  }, [load]);

  async function onCheckNow() {
    setError(null);
    setChecking(true);
    const seq = ++requestSeq.current;
    try {
      const s = await api.checkUpdate();
      applyStatus(seq, s);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Check failed");
    } finally {
      setChecking(false);
    }
  }

  async function onApply() {
    setError(null);
    setApplying(true);
    try {
      const before = status;
      await api.applyUpdate();

      // The server restarts itself partway through — poll until it comes
      // back up reporting a different (i.e. now-current) commit, rather
      // than waiting for this request to "finish" (it never will from the
      // server's side, since the process handling it gets killed).
      const deadline = Date.now() + updatePollTimeoutMs;
      const poll = async (): Promise<void> => {
        if (Date.now() > deadline) {
          setError("Update is taking longer than expected — check journalctl -u portly-server on the VPS.");
          setApplying(false);
          return;
        }
        const s = await load();
        if (s && (!before || s.current_commit !== before.current_commit)) {
          setApplying(false);
          return;
        }
        setTimeout(poll, updatePollInterval);
      };
      setTimeout(poll, updatePollInterval);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Failed to start update");
      setApplying(false);
    }
  }

  return (
    <div className="mx-auto max-w-xl">
      <p className="text-sm text-foreground-secondary">
        Checks GitHub for a newer commit on Portly&apos;s <code className="font-mono">main</code>{" "}
        branch than the one this server was built from.
      </p>

      <div className="mt-4 rounded-xl border border-border bg-surface p-6">
        {!status ? (
          <p className="text-sm text-foreground-muted">Loading…</p>
        ) : (
          <>
            <div className="text-sm">
              <span className="text-foreground-secondary">Running: </span>
              <code className="font-mono">{shortCommit(status.current_commit)}</code>
            </div>
            {status.update_available && status.latest_commit && (
              <div className="mt-1 text-sm">
                <span className="text-foreground-secondary">Latest: </span>
                <code className="font-mono">{shortCommit(status.latest_commit)}</code>
              </div>
            )}
            <div className="mt-1 text-xs text-foreground-muted">
              Last checked {new Date(status.checked_at).toLocaleString()}
            </div>

            {status.check_error && (
              <p className="mt-3 text-sm text-[color:var(--status-critical)]">
                Check failed: {status.check_error}
              </p>
            )}

            {status.current_commit === "dev" && (
              <p className="mt-3 text-xs text-foreground-muted">
                This is a from-source build with no embedded commit, so there&apos;s nothing to
                compare against — build via <code className="font-mono">make build</code> to
                enable update checks.
              </p>
            )}

            <div className="mt-4 flex items-center gap-2">
              <button
                onClick={onCheckNow}
                disabled={checking || applying}
                className="rounded-lg border border-border px-3 py-1.5 text-sm text-foreground-secondary hover:bg-surface-raised disabled:opacity-50"
              >
                {checking ? "Checking…" : "Check now"}
              </button>

              {status.update_available && status.can_apply && (
                <button
                  onClick={onApply}
                  disabled={applying}
                  className="rounded-lg bg-accent px-3 py-1.5 text-sm font-medium text-white hover:bg-[color:var(--accent-hover)] disabled:opacity-50"
                >
                  {applying ? "Updating…" : "Update now"}
                </button>
              )}
            </div>

            {applying && (
              <p className="mt-3 text-sm text-foreground-secondary">
                Update running — this can take a few minutes. The panel will go
                briefly unreachable while the server restarts, then this page
                refreshes automatically.
              </p>
            )}

            {status.update_available && !status.can_apply && (
              <div className="mt-4 rounded-lg border border-border bg-surface-raised p-3 text-xs text-foreground-secondary">
                One-click update isn&apos;t enabled on this server (this is
                the case for custom checkouts, or if it was turned off via{" "}
                <code className="font-mono">--disable-update-button</code>).
                Run this on the VPS instead:
                <code className="mt-2 block break-all rounded bg-surface px-2 py-1.5 font-mono">
                  {'curl -fsSL "https://raw.githubusercontent.com/JxstColin/Portly/main/scripts/quickstart-vps.sh?$(date +%s)" | sudo bash'}
                </code>
              </div>
            )}

            {error && (
              <p className="mt-3 text-sm text-[color:var(--status-critical)]">{error}</p>
            )}
          </>
        )}
      </div>
    </div>
  );
}

function SettingsContent() {
  const router = useRouter();
  const searchParams = useSearchParams();
  const tabParam = searchParams.get("tab");
  const tab: Tab = tabParam === "domain" ? "domain" : tabParam === "updates" ? "updates" : "account";
  const firstLogin = searchParams.get("first-login") === "1";

  function setTab(next: Tab) {
    router.replace(`/settings?tab=${next}`);
  }

  return (
    <div>
      <h1 className="text-xl font-semibold tracking-tight">Settings</h1>

      <div className="mt-4 flex gap-6 border-b border-border">
        <TabLink tab="account" active={tab === "account"} onClick={setTab}>
          Account
        </TabLink>
        <TabLink tab="domain" active={tab === "domain"} onClick={setTab}>
          Domain
        </TabLink>
        <TabLink tab="updates" active={tab === "updates"} onClick={setTab}>
          Updates
        </TabLink>
      </div>

      <div className="mt-6">
        {tab === "account" ? (
          <AccountTab firstLogin={firstLogin} />
        ) : tab === "domain" ? (
          <DomainTab />
        ) : (
          <UpdatesTab />
        )}
      </div>
    </div>
  );
}

export default function SettingsPage() {
  return (
    <AppShell enforceCredentialChange={false}>
      <Suspense>
        <SettingsContent />
      </Suspense>
    </AppShell>
  );
}
