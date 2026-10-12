import type { Vec } from "../api";

export interface Box { min: [number, number, number]; max: [number, number, number] }

/** Bounding box of a flat xyz position array. */
export function bounds(pos: ArrayLike<number>): Box {
  const min: [number, number, number] = [Infinity, Infinity, Infinity];
  const max: [number, number, number] = [-Infinity, -Infinity, -Infinity];
  for (let i = 0; i < pos.length; i += 3) {
    for (let k = 0; k < 3; k++) {
      const v = pos[i + k]!;
      if (v < min[k]!) min[k] = v;
      if (v > max[k]!) max[k] = v;
    }
  }
  return { min, max };
}

export const size = (b: Box): Vec => ({ X: b.max[0] - b.min[0], Y: b.max[1] - b.min[1], Z: b.max[2] - b.min[2] });

/** The offset that centres the part on the bed in X and Y and puts its bottom at Z = 0. */
export function placeOnBedOffset(b: Box): [number, number, number] {
  return [-(b.min[0] + b.max[0]) / 2, -(b.min[1] + b.max[1]) / 2, -b.min[2]];
}

/** Whether a part fits the build volume, allowing a 90° turn on the bed (as the slicer may). */
export function fitsVolume(part: Vec, vol: Vec, bed: "rect" | "round" = "rect"): boolean {
  const eps = 1e-6;
  if (part.Z > vol.Z + eps) return false;
  if (bed === "round") return part.X * part.X + part.Y * part.Y <= vol.X * vol.X + eps;
  return (part.X <= vol.X + eps && part.Y <= vol.Y + eps) || (part.Y <= vol.X + eps && part.X <= vol.Y + eps);
}

/** The axes on which a part exceeds the volume, for the warning text. */
export function overflowAxes(part: Vec, vol: Vec): string[] {
  const out: string[] = [];
  const turned = part.X <= vol.Y && part.Y <= vol.X;
  if (part.X > vol.X && !turned) out.push("width");
  if (part.Y > vol.Y && !turned) out.push("depth");
  if (part.Z > vol.Z) out.push("height");
  return out;
}

/** The vertex of a triangle nearest to a hit point, for snapping measurements (PRD V-6). */
export function nearestVertex(tri: [number, number, number][], p: [number, number, number]): [number, number, number] {
  let best = tri[0]!;
  let bestD = Infinity;
  for (const v of tri) {
    const d = (v[0] - p[0]) ** 2 + (v[1] - p[1]) ** 2 + (v[2] - p[2]) ** 2;
    if (d < bestD) {
      bestD = d;
      best = v;
    }
  }
  return best;
}

/** Snap to the nearest vertex when it is within `radius` of the hit point, else keep the hit point. */
export function snap(tri: [number, number, number][], p: [number, number, number], radius: number): [number, number, number] {
  const v = nearestVertex(tri, p);
  return distance(v, p) <= radius ? v : p;
}

export const distance = (a: [number, number, number], b: [number, number, number]) =>
  Math.hypot(a[0] - b[0], a[1] - b[1], a[2] - b[2]);

/** Section plane range for an axis: from the part's minimum to maximum on that axis. */
export function sectionRange(b: Box, axis: "x" | "y" | "z"): [number, number] {
  const k = axis === "x" ? 0 : axis === "y" ? 1 : 2;
  return [b.min[k]!, b.max[k]!];
}
