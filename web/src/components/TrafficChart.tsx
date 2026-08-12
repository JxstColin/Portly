"use client";

import { useMemo, useState } from "react";
import { TrafficSample } from "@/lib/api";

const WIDTH = 640;
const HEIGHT = 200;
const PAD_LEFT = 44;
const PAD_BOTTOM = 24;
const PAD_TOP = 12;
const PAD_RIGHT = 12;

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

  const maxVal = Math.max(1, ...buckets.map((b) => Math.max(b.in, b.out)));
  const plotW = WIDTH - PAD_LEFT - PAD_RIGHT;
  const plotH = HEIGHT - PAD_TOP - PAD_BOTTOM;
  const groupW = plotW / buckets.length;
  const barW = Math.max(2, groupW * 0.32);

  const yTicks = [0, 0.5, 1].map((f) => ({
    y: PAD_TOP + plotH * (1 - f),
    label: formatBytes(Math.round(maxVal * f)),
  }));

  return (
    <div className="rounded-xl border border-border bg-surface p-4">
      <div className="mb-2 flex items-center gap-4 text-xs">
        <span className="flex items-center gap-1.5">
          <span
            className="h-2 w-2 rounded-full"
            style={{ background: "var(--series-1)" }}
          />
          <span className="text-foreground-secondary">In</span>
        </span>
        <span className="flex items-center gap-1.5">
          <span
            className="h-2 w-2 rounded-full"
            style={{ background: "var(--series-2)" }}
          />
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
            <text x={PAD_LEFT - 6} y={t.y + 3} textAnchor="end" fontSize={10} fill="var(--foreground-muted)">
              {t.label}
            </text>
          </g>
        ))}
        <line
          x1={PAD_LEFT}
          x2={WIDTH - PAD_RIGHT}
          y1={HEIGHT - PAD_BOTTOM}
          y2={HEIGHT - PAD_BOTTOM}
          stroke="var(--baseline)"
          strokeWidth={1}
        />

        {buckets.map((b, i) => {
          const gx = PAD_LEFT + i * groupW;
          const inH = (b.in / maxVal) * plotH;
          const outH = (b.out / maxVal) * plotH;
          const isHover = hover === i;
          return (
            <g key={i}>
              <rect
                x={gx}
                y={PAD_TOP}
                width={groupW}
                height={plotH}
                fill="transparent"
                onMouseEnter={() => setHover(i)}
                onMouseLeave={() => setHover((h) => (h === i ? null : h))}
              />
              <rect
                className="transition-all duration-300 ease-out"
                x={gx + groupW / 2 - barW - 1}
                y={HEIGHT - PAD_BOTTOM - inH}
                width={barW}
                height={inH}
                rx={2}
                fill="var(--series-1)"
                opacity={isHover ? 1 : 0.85}
              />
              <rect
                className="transition-all duration-300 ease-out"
                x={gx + groupW / 2 + 1}
                y={HEIGHT - PAD_BOTTOM - outH}
                width={barW}
                height={outH}
                rx={2}
                fill="var(--series-2)"
                opacity={isHover ? 1 : 0.85}
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
          <div className="mt-1 flex gap-4">
            <span style={{ color: "var(--series-1)" }}>In: {formatBytes(buckets[hover].in)}</span>
            <span style={{ color: "var(--series-2)" }}>Out: {formatBytes(buckets[hover].out)}</span>
          </div>
        </div>
      )}
    </div>
  );
}
