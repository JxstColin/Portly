"use client";

import { useEffect } from "react";
import { useRouter } from "next/navigation";
import { useAuth } from "@/lib/auth-context";

export default function Home() {
  const { user, loading } = useAuth();
  const router = useRouter();

  useEffect(() => {
    if (loading) return;
    if (!user) {
      router.replace("/login");
    } else if (user.must_change_password) {
      router.replace("/account?first-login=1");
    } else {
      router.replace("/dashboard");
    }
  }, [loading, user, router]);

  return (
    <main className="flex flex-1 items-center justify-center">
      <p className="text-sm text-foreground-muted">Loading…</p>
    </main>
  );
}
