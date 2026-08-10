export function StatusDot({ connected }: { connected: boolean }) {
  return (
    <span className="inline-flex items-center gap-1.5 text-sm">
      <span
        className="h-2 w-2 shrink-0 rounded-full"
        style={{
          background: connected
            ? "var(--status-good)"
            : "var(--status-critical)",
        }}
        aria-hidden
      />
      <span
        style={{
          color: connected
            ? "var(--status-good-text)"
            : "var(--status-critical)",
        }}
      >
        {connected ? "Online" : "Offline"}
      </span>
    </span>
  );
}
