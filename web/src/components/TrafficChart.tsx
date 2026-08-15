"use client";

import { useEffect, useMemo, useState } from "react";
import { TrafficSample } from "@/lib/api";

const WIDTH = 640;
const HEIGHT = 200;
const PAD_LEFT = 54;
const PAD_BOTTOM = 24;
const PAD_TOP = 12;
const PAD_RIGHT = 12;
const BAR_RADIUS = 4;
const BAR_MAX_WIDTH = 24;

function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  return `${(n / 1024 / 1024).toFixed(1)} MB`;
}

function bucketSamples(samples: TrafficSample[], rangeSeconds: number, bucketCount: number) {
  const now = Math.floor(Date.now() / 1000);
  const start = now - rangeSeconds;
  const bucketSize = rangeSeconds / bucketCount;

  const buckets = Array.from({ length: bucketCount }, (_, i) => ({
    t: start + i * bucketSize,
    in: 0,
    out: 0,
  }));

  for (const s of samples) {
    if (s.ts < start) continue;
    const idx = Math.min(bucketCount - 1, Math.floor((s.ts - start) / bucketSize));
    if (idx < 0) continue;
    buckets[idx].in += s.bytes_in;
    buckets[idx].out += s.bytes_out;
  }
  return buckets;
}

// A bar with square corners at the baseline and rounded corners at its data
// end, per the mark spec — never a uniformly rounded rect, which floats the
// baseline edge and looks unanchored.
//
// Always emits the same command sequence, degenerating to a zero-height
// rect at the baseline rather than an empty string when h is 0. That's what
// lets the browser interpolate `d` smoothly on entrance/hover — animating
// between structurally different paths (or from "") doesn't tween, it snaps.
function barPath(x: number, yTop: number, w: number, h: number, baseline: number): string {
  const r = Math.min(BAR_RADIUS, w / 2, Math.max(h, 0));
  return [
    `M ${x} ${baseline}`,
    `L ${x} ${yTop + r}`,
    `Q ${x} ${yTop} ${x + r} ${yTop}`,
    `L ${x + w - r} ${yTop}`,
    `Q ${x + w} ${yTop} ${x + w} ${yTop + r}`,
    `L ${x + w} ${baseline}`,
    "Z",
  ].join(" ");
}

export function TrafficChart({
  samples,
  rangeSeconds = 3600,
  bucketCount = 12,
}: {
  samples: TrafficSample[];
  rangeSeconds?: number;
  bucketCount?: number;
}) {
  const buckets = useMemo(
    () => bucketSamples(samples, rangeSeconds, bucketCount),
    [samples, rangeSeconds, bucketCount]
  );
  const [hover, setHover] = useState<number | null>(null);

  // Bars grow up from the baseline on first paint instead of popping in at
  // full height — the entrance is what makes the chart read as alive rather
  // than a static image.
  const [revealed, setRevealed] = useState(false);
  useEffect(() => {
    const id = requestAnimationFrame(() => setRevealed(true));
    return () => cancelAnimationFrame(id);
  }, []);

  const maxVal = Math.max(1, ...buckets.map((b) => Math.max(b.in, b.out)));
  const plotW = WIDTH - PAD_LEFT - PAD_RIGHT;
  const plotH = HEIGHT - PAD_TOP - PAD_BOTTOM;
  const baseline = HEIGHT - PAD_BOTTOM;
  const groupW = plotW / buckets.length;
  const barW = Math.min(BAR_MAX_WIDTH, Math.max(2, groupW * 0.32));

  const yTicks = [0, 0.5, 1].map((f) => ({
    y: PAD_TOP + plotH * (1 - f),
    label: formatBytes(Math.round(maxVal * f)),
  }));

  return (
    <div className="rounded-xl border border-border bg-surface p-4">
      <div className="mb-2 flex items-center gap-4 text-xs">
        <span className="flex items-center gap-1.5">
          <span className="h-2 w-3 rounded-[2px]" style={{ background: "var(--series-1)" }} />
          <span className="text-foreground-secondary">In</span>
        </span>
        <span className="flex items-center gap-1.5">
          <span className="h-2 w-3 rounded-[2px]" style={{ background: "var(--series-2)" }} />
          <span className="text-foreground-secondary">Out</span>
        </span>
      </div>

      <svg
        viewBox={`0 0 ${WIDTH} ${HEIGHT}`}
        className="w-full"
        role="img"
        aria-label="Traffic over time, bytes in and out per interval"
      >
        {yTicks.map((t, i) => (
          <g key={i}>
            <line
              x1={PAD_LEFT}
              x2={WIDTH - PAD_RIGHT}
              y1={t.y}
              y2={t.y}
              stroke="var(--gridline)"
              strokeWidth={1}
            />
            <text
              x={PAD_LEFT - 6}
              y={t.y + 3}
              textAnchor="end"
              fontSize={10}
              fill="var(--foreground-muted)"
              style={{ fontVariantNumeric: "tabular-nums" }}
            >
              {t.label}
            </text>
          </g>
        ))}
        <line
          x1={PAD_LEFT}
          x2={WIDTH - PAD_RIGHT}
          y1={baseline}
          y2={baseline}
          stroke="var(--baseline)"
          strokeWidth={1}
        />

        {buckets.map((b, i) => {
          const gx = PAD_LEFT + i * groupW;
          const inH = revealed ? (b.in / maxVal) * plotH : 0;
          const outH = revealed ? (b.out / maxVal) * plotH : 0;
          const isHover = hover === i;
          const delay = `${Math.min(i * 15, 250)}ms`;
          return (
            <g
              key={i}
              className="transition-transform duration-150 ease-out"
              style={{ transform: isHover ? "translateY(-2px)" : "translateY(0)" }}
            >
              <rect
                x={gx}
                y={PAD_TOP}
                width={groupW}
                height={plotH}
                fill="transparent"
                onMouseEnter={() => setHover(i)}
                onMouseLeave={() => setHover((h) => (h === i ? null : h))}
              />
              <path
                d={barPath(gx + groupW / 2 - barW - 1, baseline - inH, barW, inH, baseline)}
                fill="var(--series-1)"
                opacity={isHover ? 1 : 0.9}
                className="transition-all duration-500 ease-out"
                style={{ transitionDelay: delay }}
              />
              <path
                d={barPath(gx + groupW / 2 + 1, baseline - outH, barW, outH, baseline)}
                fill="var(--series-2)"
                opacity={isHover ? 1 : 0.9}
                className="transition-all duration-500 ease-out"
                style={{ transitionDelay: delay }}
              />
              {i % Math.ceil(buckets.length / 6) === 0 && (
                <text
                  x={gx + groupW / 2}
                  y={HEIGHT - 6}
                  textAnchor="middle"
                  fontSize={9}
                  fill="var(--foreground-muted)"
                >
                  {new Date(b.t * 1000).toLocaleTimeString([], {
                    hour: "2-digit",
                    minute: "2-digit",
                  })}
                </text>
              )}
            </g>
          );
        })}
      </svg>

      {hover !== null && (
        <div className="mt-2 animate-fade-in rounded-lg border border-border bg-surface-raised px-3 py-2 text-xs">
          <div className="font-medium text-foreground-secondary">
            {new Date(buckets[hover].t * 1000).toLocaleTimeString()}
          </div>
          <div className="mt-1.5 flex gap-4">
            <span className="flex items-center gap-1.5">
              <span className="h-2 w-3 rounded-[2px]" style={{ background: "var(--series-1)" }} />
              <span className="font-semibold text-foreground">{formatBytes(buckets[hover].in)}</span>
              <span className="text-foreground-muted">in</span>
            </span>
            <span className="flex items-center gap-1.5">
              <span className="h-2 w-3 rounded-[2px]" style={{ background: "var(--series-2)" }} />
              <span className="font-semibold text-foreground">{formatBytes(buckets[hover].out)}</span>
              <span className="text-foreground-muted">out</span>
            </span>
          </div>
        </div>
      )}
    </div>
  );
}
