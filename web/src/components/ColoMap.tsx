import { ComposableMap, Geographies, Geography, Marker } from "react-simple-maps";
import land from "world-atlas/land-110m.json";
import { COLO_GEO } from "../lib/colos";

type Props = {
  buckets: Array<[string, number]>; // [colo, workerCount]
};

// ColoMap renders one orange pin per distinct Cloudflare colo, sized by
// worker count. Uses react-simple-maps + natural-earth 110m land polygons
// bundled via world-atlas. Zero runtime CDN fetch — all data is embedded
// in the bundle, consistent with our `default-src 'self'` CSP.
export default function ColoMap({ buckets }: Props) {
  const maxN = buckets.reduce((acc, [, n]) => Math.max(acc, n), 1);

  return (
    <div className="relative w-full overflow-hidden rounded border bg-background">
      <ComposableMap
        projection="geoEqualEarth"
        projectionConfig={{ scale: 155, center: [0, 10] }}
        width={960}
        height={440}
        style={{ width: "100%", height: "auto" }}
      >
        <Geographies geography={land}>
          {({ geographies }) =>
            geographies.map((geo) => (
              <Geography
                key={geo.rsmKey}
                geography={geo}
                fill="hsl(var(--muted-foreground))"
                fillOpacity={0.22}
                stroke="hsl(var(--border))"
                strokeWidth={0.5}
                style={{
                  default: { outline: "none" },
                  hover: { outline: "none", fillOpacity: 0.3 },
                  pressed: { outline: "none" },
                }}
              />
            ))
          }
        </Geographies>

        {buckets.map(([colo, n]) => {
          const g = COLO_GEO[colo];
          if (!g) return null;
          const r = 3 + (n / maxN) * 6;
          return (
            <Marker key={colo} coordinates={[g.lon, g.lat]}>
              <circle r={r * 2.2} fill="#f38020" fillOpacity={0.18} />
              <circle r={r} fill="#f38020" stroke="white" strokeWidth={0.8}>
                <title>{colo} — {g.name} ({n} worker{n > 1 ? "s" : ""})</title>
              </circle>
              {r > 5 && (
                <text
                  textAnchor="middle"
                  y={-r - 4}
                  fontSize={10}
                  fontWeight={600}
                  fill="hsl(var(--foreground))"
                  style={{ paintOrder: "stroke", stroke: "hsl(var(--background))", strokeWidth: 2.5 }}
                >
                  {colo}
                </text>
              )}
            </Marker>
          );
        })}
      </ComposableMap>
    </div>
  );
}
