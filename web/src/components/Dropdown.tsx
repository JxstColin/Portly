"use client";

import { useEffect, useRef, useState } from "react";

function ChevronIcon({ open }: { open: boolean }) {
  return (
    <svg
      className={`h-3.5 w-3.5 shrink-0 text-foreground-muted transition-transform duration-150 ${open ? "rotate-180" : ""}`}
      viewBox="0 0 12 12"
      fill="none"
    >
      <path
        d="M2.5 4.5L6 8l3.5-3.5"
        stroke="currentColor"
        strokeWidth="1.5"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  );
}

function CheckIcon() {
  return (
    <svg className="h-3.5 w-3.5 shrink-0" viewBox="0 0 12 12" fill="none">
      <path
        d="M2 6.5L4.5 9L10 3"
        stroke="currentColor"
        strokeWidth="1.5"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  );
}

// Closes an open dropdown on an outside click or Escape — shared by Select
// and Combobox below.
function useDismiss(open: boolean, onDismiss: () => void) {
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!open) return;
    function onPointerDown(e: PointerEvent) {
      if (ref.current && !ref.current.contains(e.target as Node)) onDismiss();
    }
    function onKeyDown(e: KeyboardEvent) {
      if (e.key === "Escape") onDismiss();
    }
    document.addEventListener("pointerdown", onPointerDown);
    document.addEventListener("keydown", onKeyDown);
    return () => {
      document.removeEventListener("pointerdown", onPointerDown);
      document.removeEventListener("keydown", onKeyDown);
    };
  }, [open, onDismiss]);
  return ref;
}

const triggerClasses =
  "w-full rounded-lg border border-border bg-surface-raised text-sm outline-none transition-colors hover:border-foreground-muted focus-within:ring-2 focus-within:ring-accent";
// min-w-full keeps the panel at least as wide as its trigger, but w-max lets
// it grow past that instead of truncating a longer option (e.g. a discovered
// device's IP + MAC) into illegibility — capped so it can't blow out past a
// narrow form column's siblings.
const panelClasses =
  "absolute left-0 z-20 mt-1 max-h-56 min-w-full w-max max-w-[18rem] overflow-auto rounded-lg border border-border bg-surface-raised py-1 shadow-lg";
const optionClasses =
  "flex w-full items-center justify-between gap-2 px-3 py-1.5 text-left text-sm hover:bg-surface";

export interface SelectOption<T extends string = string> {
  value: T;
  label: string;
}

// A strict, styled replacement for a native <select> — pick one of a fixed
// set of options, nothing else.
export function Select<T extends string>({
  value,
  onChange,
  options,
  className = "",
}: {
  value: T;
  onChange: (v: T) => void;
  options: SelectOption<T>[];
  className?: string;
}) {
  const [open, setOpen] = useState(false);
  const ref = useDismiss(open, () => setOpen(false));
  const current = options.find((o) => o.value === value);

  return (
    <div ref={ref} className={`relative ${className}`}>
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className={`${triggerClasses} flex items-center justify-between gap-2 px-2.5 py-1.5`}
      >
        <span>{current?.label ?? value}</span>
        <ChevronIcon open={open} />
      </button>
      {open && (
        <ul className={panelClasses}>
          {options.map((o) => (
            <li key={o.value}>
              <button
                type="button"
                onClick={() => {
                  onChange(o.value);
                  setOpen(false);
                }}
                className={`${optionClasses} ${o.value === value ? "text-accent" : "text-foreground"}`}
              >
                {o.label}
                {o.value === value && <CheckIcon />}
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

export interface ComboboxOption {
  value: string;
  label: string;
  sublabel?: string;
}

// A free-text input with a suggestions dropdown — for fields like "local
// host" where we can offer good guesses (localhost, discovered LAN
// devices) without limiting the admin to only what we found.
export function Combobox({
  value,
  onChange,
  options,
  placeholder,
  className = "",
}: {
  value: string;
  onChange: (v: string) => void;
  options: ComboboxOption[];
  placeholder?: string;
  className?: string;
}) {
  const [open, setOpen] = useState(false);
  const ref = useDismiss(open, () => setOpen(false));

  const q = value.trim().toLowerCase();
  const filtered = q
    ? options.filter(
        (o) =>
          o.value.toLowerCase().includes(q) ||
          o.label.toLowerCase().includes(q) ||
          o.sublabel?.toLowerCase().includes(q)
      )
    : options;

  return (
    <div ref={ref} className={`relative ${className}`}>
      <div className={`${triggerClasses} flex items-center pr-1.5`}>
        <input
          className="w-full bg-transparent px-2.5 py-1.5 outline-none"
          value={value}
          placeholder={placeholder}
          onFocus={() => setOpen(true)}
          onChange={(e) => {
            onChange(e.target.value);
            setOpen(true);
          }}
        />
        <button
          type="button"
          tabIndex={-1}
          onClick={() => setOpen((v) => !v)}
          className="rounded p-1 text-foreground-muted hover:text-foreground"
        >
          <ChevronIcon open={open} />
        </button>
      </div>
      {open && filtered.length > 0 && (
        <ul className={panelClasses}>
          {filtered.map((o) => (
            <li key={o.value}>
              <button
                type="button"
                onClick={() => {
                  onChange(o.value);
                  setOpen(false);
                }}
                className={optionClasses}
              >
                <span className="truncate">{o.label}</span>
                {o.sublabel && (
                  <span className="shrink-0 font-mono text-xs text-foreground-muted">{o.sublabel}</span>
                )}
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
