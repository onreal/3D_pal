import type { Estimate, Vec } from "./api";

export const mm = (n: number) => n.toFixed(1);

export const formatSize = (v: Vec) => `${mm(v.X)} × ${mm(v.Y)} × ${mm(v.Z)} mm`;

export function formatMinutes(min: number): string {
  const h = Math.floor(min / 60);
  const m = min % 60;
  return h > 0 ? `${h} h ${m} min` : `${m} min`;
}

export function formatCost(e: Pick<Estimate, "cost" | "currency">): string {
  if (!e.cost) return "";
  try {
    return new Intl.NumberFormat(undefined, { style: "currency", currency: e.currency || "EUR" }).format(e.cost);
  } catch {
    return `${e.cost.toFixed(2)} ${e.currency ?? ""}`.trim();
  }
}

export function timeAgo(iso: string, now = Date.now()): string {
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return "";
  const s = Math.max(0, Math.round((now - t) / 1000));
  if (s < 60) return "just now";
  if (s < 3600) return `${Math.floor(s / 60)} min ago`;
  if (s < 86400) return `${Math.floor(s / 3600)} h ago`;
  return new Date(t).toLocaleDateString();
}

export type Better = "lower" | "higher" | "same";

/** Compares two estimates field by field; "lower" means a is cheaper/faster/lighter than b. */
export function compareEstimates(a: Estimate, b: Estimate): Record<"minutes" | "grams" | "cost" | "layers", Better> {
  const cmp = (x: number, y: number): Better => (Math.abs(x - y) < 1e-9 ? "same" : x < y ? "lower" : "higher");
  return {
    minutes: cmp(a.minutes, b.minutes),
    grams: cmp(a.grams, b.grams),
    cost: cmp(a.cost ?? 0, b.cost ?? 0),
    layers: cmp(a.layers, b.layers),
  };
}
