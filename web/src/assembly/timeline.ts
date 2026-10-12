// The assembly timeline: t runs from 0 (nothing placed) to n (every part placed). Part i
// flies in during [i, i + 1) and sits in place from i + 1 on.

export type PartState = { kind: "hidden" } | { kind: "entering"; progress: number } | { kind: "placed" };

export function partState(i: number, t: number): PartState {
  if (t >= i + 1) return { kind: "placed" };
  if (t <= i) return { kind: "hidden" };
  return { kind: "entering", progress: t - i };
}

export const easeOutCubic = (x: number) => 1 - (1 - Math.max(0, Math.min(1, x))) ** 3;

type V3 = [number, number, number];

/**
 * Where a part comes from: above the assembly and out from its centre, so stacked parts
 * drop in from the top and parts at the side slide in from their side.
 */
export function approachDirection(partCenter: V3, assemblyCenter: V3): V3 {
  const dx = partCenter[0] - assemblyCenter[0];
  const dy = partCenter[1] - assemblyCenter[1];
  const radial = Math.hypot(dx, dy);
  const out: V3 = radial > 1e-6 ? [(dx / radial) * 0.6, (dy / radial) * 0.6, 1] : [0, 0, 1];
  const len = Math.hypot(...out);
  return [out[0] / len, out[1] / len, out[2] / len];
}

/** The offset of part i at time t: the full approach distance before it enters, zero once placed. */
export function offsetAt(i: number, t: number, direction: V3, distance: number): V3 {
  const s = partState(i, t);
  const k = s.kind === "placed" ? 0 : s.kind === "hidden" ? 1 : 1 - easeOutCubic(s.progress);
  return [direction[0] * distance * k, direction[1] * distance * k, direction[2] * distance * k];
}

/** The step a time falls in, for highlighting: -1 before the first part starts. */
export const currentStep = (t: number, n: number) => (t <= 0 ? -1 : Math.min(Math.ceil(t) - 1, n - 1));

/** Applies a saved order (part IDs) to the parts, keeping new parts at the end in their order. */
export function applyOrder<T extends { id: string }>(parts: T[], saved: string[] | null): T[] {
  if (!saved?.length) return parts;
  const byId = new Map(parts.map((p) => [p.id, p]));
  const ordered = saved.map((id) => byId.get(id)).filter((p): p is T => !!p);
  const rest = parts.filter((p) => !saved.includes(p.id));
  return [...ordered, ...rest];
}

export function move<T>(list: T[], from: number, to: number): T[] {
  if (to < 0 || to >= list.length || from === to) return list;
  const out = [...list];
  const [x] = out.splice(from, 1);
  out.splice(to, 0, x!);
  return out;
}
