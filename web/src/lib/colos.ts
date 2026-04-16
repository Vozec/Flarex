// Partial Cloudflare colo code → geo lookup. Covers the most-seen egress
// PoPs. Lat/lon is approximate (city center). Unknown colos fall through
// to a "?" pin at (0,0) so the map still renders.
export type ColoGeo = { lat: number; lon: number; name: string };

export const COLO_GEO: Record<string, ColoGeo> = {
  // Europe
  CDG: { lat: 48.86, lon: 2.35, name: "Paris" },
  MRS: { lat: 43.30, lon: 5.38, name: "Marseille" },
  LHR: { lat: 51.47, lon: -0.45, name: "London" },
  MAN: { lat: 53.48, lon: -2.24, name: "Manchester" },
  AMS: { lat: 52.37, lon: 4.90, name: "Amsterdam" },
  FRA: { lat: 50.11, lon: 8.68, name: "Frankfurt" },
  DUS: { lat: 51.23, lon: 6.77, name: "Dusseldorf" },
  HAM: { lat: 53.55, lon: 10.0, name: "Hamburg" },
  MUC: { lat: 48.14, lon: 11.58, name: "Munich" },
  VIE: { lat: 48.21, lon: 16.37, name: "Vienna" },
  ZRH: { lat: 47.38, lon: 8.54, name: "Zurich" },
  MXP: { lat: 45.46, lon: 9.19, name: "Milan" },
  FCO: { lat: 41.90, lon: 12.50, name: "Rome" },
  MAD: { lat: 40.42, lon: -3.70, name: "Madrid" },
  BCN: { lat: 41.39, lon: 2.17, name: "Barcelona" },
  LIS: { lat: 38.72, lon: -9.14, name: "Lisbon" },
  CPH: { lat: 55.68, lon: 12.57, name: "Copenhagen" },
  OSL: { lat: 59.91, lon: 10.75, name: "Oslo" },
  ARN: { lat: 59.33, lon: 18.06, name: "Stockholm" },
  HEL: { lat: 60.17, lon: 24.94, name: "Helsinki" },
  WAW: { lat: 52.23, lon: 21.01, name: "Warsaw" },
  PRG: { lat: 50.08, lon: 14.44, name: "Prague" },
  BUD: { lat: 47.50, lon: 19.04, name: "Budapest" },
  OTP: { lat: 44.43, lon: 26.10, name: "Bucharest" },
  SOF: { lat: 42.70, lon: 23.32, name: "Sofia" },
  IST: { lat: 41.01, lon: 28.98, name: "Istanbul" },
  ATH: { lat: 37.98, lon: 23.72, name: "Athens" },
  DUB: { lat: 53.35, lon: -6.26, name: "Dublin" },

  // North America
  IAD: { lat: 38.95, lon: -77.46, name: "Ashburn" },
  EWR: { lat: 40.69, lon: -74.17, name: "Newark" },
  JFK: { lat: 40.64, lon: -73.78, name: "New York" },
  BOS: { lat: 42.36, lon: -71.06, name: "Boston" },
  ORD: { lat: 41.88, lon: -87.63, name: "Chicago" },
  ATL: { lat: 33.75, lon: -84.39, name: "Atlanta" },
  MIA: { lat: 25.76, lon: -80.19, name: "Miami" },
  DFW: { lat: 32.78, lon: -96.80, name: "Dallas" },
  DEN: { lat: 39.74, lon: -104.99, name: "Denver" },
  LAX: { lat: 34.05, lon: -118.24, name: "Los Angeles" },
  SJC: { lat: 37.34, lon: -121.89, name: "San Jose" },
  SFO: { lat: 37.77, lon: -122.42, name: "San Francisco" },
  SEA: { lat: 47.61, lon: -122.33, name: "Seattle" },
  YYZ: { lat: 43.65, lon: -79.38, name: "Toronto" },
  YUL: { lat: 45.50, lon: -73.57, name: "Montreal" },
  YVR: { lat: 49.28, lon: -123.12, name: "Vancouver" },

  // Asia
  HKG: { lat: 22.32, lon: 114.17, name: "Hong Kong" },
  NRT: { lat: 35.68, lon: 139.77, name: "Tokyo" },
  KIX: { lat: 34.69, lon: 135.50, name: "Osaka" },
  ICN: { lat: 37.57, lon: 126.98, name: "Seoul" },
  SIN: { lat: 1.35, lon: 103.82, name: "Singapore" },
  BKK: { lat: 13.75, lon: 100.52, name: "Bangkok" },
  KUL: { lat: 3.14, lon: 101.69, name: "Kuala Lumpur" },
  MNL: { lat: 14.60, lon: 120.98, name: "Manila" },
  CGK: { lat: -6.21, lon: 106.85, name: "Jakarta" },
  BOM: { lat: 19.08, lon: 72.88, name: "Mumbai" },
  DEL: { lat: 28.61, lon: 77.21, name: "Delhi" },
  MAA: { lat: 13.08, lon: 80.27, name: "Chennai" },
  DXB: { lat: 25.26, lon: 55.30, name: "Dubai" },
  TLV: { lat: 32.08, lon: 34.78, name: "Tel Aviv" },

  // South America
  GRU: { lat: -23.55, lon: -46.63, name: "São Paulo" },
  GIG: { lat: -22.91, lon: -43.17, name: "Rio de Janeiro" },
  EZE: { lat: -34.61, lon: -58.38, name: "Buenos Aires" },
  SCL: { lat: -33.45, lon: -70.67, name: "Santiago" },
  BOG: { lat: 4.71, lon: -74.07, name: "Bogotá" },
  LIM: { lat: -12.05, lon: -77.04, name: "Lima" },
  MEX: { lat: 19.43, lon: -99.13, name: "Mexico City" },

  // Africa & Oceania
  JNB: { lat: -26.20, lon: 28.05, name: "Johannesburg" },
  CPT: { lat: -33.92, lon: 18.42, name: "Cape Town" },
  NBO: { lat: -1.29, lon: 36.82, name: "Nairobi" },
  LOS: { lat: 6.52, lon: 3.38, name: "Lagos" },
  SYD: { lat: -33.87, lon: 151.21, name: "Sydney" },
  MEL: { lat: -37.81, lon: 144.96, name: "Melbourne" },
  AKL: { lat: -36.85, lon: 174.76, name: "Auckland" },
};

// projectEquirect maps (lon, lat) to SVG (x, y) for the 960x480 viewport
// used by <ColoMap>. Equirectangular projection — simple but fine at this
// zoom level.
export function projectEquirect(lon: number, lat: number, w: number, h: number): [number, number] {
  const x = ((lon + 180) / 360) * w;
  const y = ((90 - lat) / 180) * h;
  return [x, y];
}
