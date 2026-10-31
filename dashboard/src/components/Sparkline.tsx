import { useId } from "react";

export interface SparklineProps {
  /** Raw series; rendered normalized to the frame. */
  values: number[];
  /** Stroke color (CSS). */
  stroke?: string;
  width?: number;
  height?: number;
}

/** Minimal SVG sparkline with gradient fill and a glowing end dot. */
export function Sparkline({ values, stroke = "var(--primary)", width = 220, height = 40 }: SparklineProps) {
  const id = useId().replace(/[^a-zA-Z0-9]/g, "");
  if (values.length < 2) {
    return (
      <svg className="spark" viewBox={`0 0 ${width} ${height}`} aria-hidden="true" preserveAspectRatio="none">
        <line x1="0" y1={height / 2} x2={width} y2={height / 2} stroke="var(--border-strong)" strokeWidth="1.5" strokeDasharray="3 3" />
      </svg>
    );
  }
  const max = Math.max(...values);
  const min = Math.min(...values);
  const span = max - min || 1;
  const step = width / (values.length - 1);
  const points = values.map((v, i) => {
    const x = i * step;
    const y = height - 4 - ((v - min) / span) * (height - 10);
    return [x, y] as const;
  });
  const line = points.map(([x, y], i) => `${i === 0 ? "M" : "L"}${x.toFixed(1)},${y.toFixed(1)}`).join(" ");
  const area = `${line} L${width},${height} L0,${height} Z`;
  const [endX, endY] = points[points.length - 1];

  return (
    <svg className="spark" viewBox={`0 0 ${width} ${height}`} aria-hidden="true" preserveAspectRatio="none">
      <defs>
        <linearGradient id={`spark-${id}`} x1="0" y1="0" x2="0" y2="1">
          <stop offset="0%" stopColor={stroke} stopOpacity="0.35" />
          <stop offset="100%" stopColor={stroke} stopOpacity="0" />
        </linearGradient>
      </defs>
      <path d={area} fill={`url(#spark-${id})`} />
      <path d={line} fill="none" stroke={stroke} strokeWidth="1.8" strokeLinejoin="round" strokeLinecap="round" />
      <circle cx={endX} cy={endY} r="3" fill={stroke}>
        <animate attributeName="r" values="2.4;3.4;2.4" dur="2s" repeatCount="indefinite" />
      </circle>
    </svg>
  );
}
