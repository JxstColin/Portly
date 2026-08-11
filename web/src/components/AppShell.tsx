"use client";

import { useEffect } from "react";
import Link from "next/link";
import { usePathname, useRouter } from "next/navigation";
import { useAuth } from "@/lib/auth-context";

export function AppShell({
  children,
  enforceCredentialChange = true,
}: {
  children: React.ReactNode;
  enforceCredentialChange?: boolean;
}) {
  const { user, loading, logout } = useAuth();
  const router = useRouter();
  const pathname = usePathname();

  useEffect(() => {
    if (loading) return;
    if (!user) {
      router.replace("/login");
      return;
    }
    if (enforceCredentialChange && user.must_change_password && pathname !== "/settings") {
      router.replace("/settings?tab=account&first-login=1");
    }
  }, [loading, user, enforceCredentialChange, pathname, router]);

  if (loading || !user) {
    return (
      <main className="flex flex-1 items-center justify-center">
        <p className="text-sm text-foreground-muted">Loading…</p>
      </main>
    );
  }

  const navItem = (href: string, label: string) => (
    <Link
      href={href}
      className={`rounded-lg px-3 py-1.5 text-sm font-medium transition ${
        pathname === href || pathname.startsWith(href + "/")
          ? "bg-accent text-white"
          : "text-foreground-secondary hover:bg-surface-raised"
      }`}
    >
      {label}
    </Link>
  );

  return (
    <div className="flex min-h-screen flex-1 flex-col">
      <header className="border-b border-border bg-surface">
        <div className="mx-auto flex max-w-6xl items-center justify-between px-4 py-3">
          <div className="flex items-center gap-6">
            <span className="text-base font-semibold tracking-tight">Portly</span>
            <nav className="flex items-center gap-1">
              {navItem("/dashboard", "Dashboard")}
              {navItem("/settings", "Settings")}
            </nav>
          </div>
          <div className="flex items-center gap-3">
            <span className="text-sm text-foreground-muted">{user.username}</span>
            <button
              onClick={() => logout().then(() => router.replace("/login"))}
              className="rounded-lg border border-border px-3 py-1.5 text-sm text-foreground-secondary transition hover:bg-surface-raised"
            >
              Sign out
            </button>
          </div>
        </div>
      </header>
      <main className="mx-auto w-full max-w-6xl flex-1 px-4 py-6">{children}</main>
    </div>
  );
}
