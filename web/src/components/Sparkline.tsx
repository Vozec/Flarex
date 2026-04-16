type Props = {
  values: number[];
  width?: number;
  height?: number;
  className?: string;
};

// Sparkline draws a minimal inline SVG polyline. No library, no axes.
// `values` is the raw series — min/max are derived for Y-scaling.
export default function Sparkline({ values, width = 60, height = 16, className }: Props) {
  if (values.length < 2) {
    return (
      <svg width={width} height={height} className={className} role="img" aria-label="no data">
        <line x1="0" y1={height / 2} x2={width} y2={height / 2} stroke="currentColor" strokeOpacity="0.2" strokeWidth="1" />
      </svg>
    );
  }
  const min = Math.min(...values);
  const max = Math.max(...values);
  const range = max - min || 1;
  const stepX = width / (values.length - 1);
  const points = values
    .map((v, i) => `${(i * stepX).toFixed(1)},${(height - ((v - min) / range) * (height - 2) - 1).toFixed(1)}`)
    .join(" ");
  return (
    <svg width={width} height={height} className={className} role="img" aria-label="sparkline">
      <polyline
        fill="none"
        stroke="currentColor"
        strokeWidth="1.2"
        strokeLinecap="round"
        strokeLinejoin="round"
        points={points}
      />
    </svg>
  );
}
