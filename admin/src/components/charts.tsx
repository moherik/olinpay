import { cn } from "@/lib/utils";

/**
 * Stacked hourly bars. Coordinates are percentages so the chart fills its
 * container at any width without distorting strokes.
 */
export function HourlyBars({
  paid,
  failed,
  height = 180,
}: {
  paid: number[];
  failed: number[];
  height?: number;
}) {
  const maxRaw = Math.max(...paid.map((value, index) => value + failed[index]));
  // An all-zero series still needs a usable axis; without the fallback,
  // bar heights divide by zero and the rects get NaN geometry.
  const max = Math.max(Math.ceil(maxRaw / 10) * 10, 10);
  const slot = 100 / paid.length;
  const barWidth = slot * 0.6;

  return (
    <div>
      <div className="mb-3 flex items-center gap-4 text-[12px] text-secondary">
        <span className="inline-flex items-center gap-1.5">
          <span aria-hidden="true" className="h-2 w-2 rounded-[1px] bg-accent" />
          Paid or in flight
        </span>
        <span className="inline-flex items-center gap-1.5">
          <span
            aria-hidden="true"
            className="h-2 w-2 rounded-[1px] bg-failed"
          />
          Failed
        </span>
      </div>
      <div className="flex gap-2">
        <div
          className="num flex w-6 flex-col justify-between pb-5 text-right text-[11px] text-muted"
          style={{ height }}
        >
          <span>{max}</span>
          <span>{max / 2}</span>
          <span>0</span>
        </div>
        <div className="flex-1">
          <div className="relative" style={{ height }}>
            <svg
              className="absolute inset-0 h-full w-full"
              viewBox="0 0 100 100"
              preserveAspectRatio="none"
              role="img"
              aria-label="Payments per hour over the last 24 hours"
            >
              {[0, 25, 50, 75].map((y) => (
                <line
                  key={y}
                  x1="0"
                  x2="100"
                  y1={y}
                  y2={y}
                  stroke="var(--color-border-subtle)"
                  strokeWidth="1"
                  vectorEffect="non-scaling-stroke"
                />
              ))}
              <line
                x1="0"
                x2="100"
                y1="100"
                y2="100"
                stroke="var(--color-border)"
                strokeWidth="1"
                vectorEffect="non-scaling-stroke"
              />
              {paid.map((value, index) => {
                const failedValue = failed[index];
                const total = value + failedValue;
                const totalHeight = (total / max) * 100;
                const failedHeight = (failedValue / max) * 100;
                const paidHeight = totalHeight - failedHeight;
                const x = index * slot + (slot - barWidth) / 2;
                return (
                  <g key={index}>
                    <title>{`${String(index).padStart(2, "0")}:00, ${value} paid or in flight, ${failedValue} failed`}</title>
                    <rect
                      x={x}
                      y={100 - paidHeight}
                      width={barWidth}
                      height={Math.max(paidHeight, 0.6)}
                      fill="var(--color-accent)"
                    />
                    <rect
                      x={x}
                      y={100 - totalHeight}
                      width={barWidth}
                      height={Math.max(failedHeight, failedValue > 0 ? 0.6 : 0)}
                      fill="var(--color-failed)"
                    />
                  </g>
                );
              })}
            </svg>
          </div>
          <div className="mt-1 flex justify-between pb-1 text-[11px] text-muted">
            <span className="num">00:00</span>
            <span className="num">06:00</span>
            <span className="num">12:00</span>
            <span className="num">18:00</span>
            <span className="num">23:00</span>
          </div>
        </div>
      </div>
    </div>
  );
}

export function MiniBar({
  value,
  max,
  tone = "accent",
}: {
  value: number;
  max: number;
  tone?: "accent" | "danger" | "muted";
}) {
  const width = max === 0 ? 0 : Math.round((value / max) * 100);
  const tones = {
    accent: "bg-accent",
    danger: "bg-failed",
    muted: "bg-border",
  } as const;
  return (
    <div className="h-1.5 w-full rounded-[2px] bg-surface-muted">
      <div
        className={cn("h-full rounded-[2px]", tones[tone])}
        style={{ width: `${Math.max(width, value > 0 ? 3 : 0)}%` }}
      />
    </div>
  );
}
