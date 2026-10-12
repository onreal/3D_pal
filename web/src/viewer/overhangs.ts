/** One flag per triangle: true when it faces down more steeply than maxAngleDeg from vertical (PRD V-8). */
export function overhangFaces(pos: ArrayLike<number>, nrm: ArrayLike<number>, maxAngleDeg = 45): boolean[] {
  let minZ = Infinity;
  for (let i = 2; i < pos.length; i += 3) minZ = Math.min(minZ, pos[i]!);
  const limit = -Math.sin((maxAngleDeg * Math.PI) / 180);
  const flags: boolean[] = [];
  for (let f = 0; f < pos.length / 9; f++) {
    const onBed = [2, 5, 8].every((k) => pos[f * 9 + k]! - minZ < 0.01);
    flags.push(nrm[f * 9 + 2]! < limit && !onBed);
  }
  return flags;
}

/** Vertex colours (r, g, b per vertex) painting overhang faces in the warning colour. */
export function overhangColors(flags: boolean[], base: [number, number, number], warn: [number, number, number]): Float32Array {
  const out = new Float32Array(flags.length * 9);
  flags.forEach((over, f) => {
    const c = over ? warn : base;
    for (let v = 0; v < 3; v++) out.set(c, f * 9 + v * 3);
  });
  return out;
}

/** Share of faces flagged, for the badge ("12% overhang"). */
export const overhangShare = (flags: boolean[]) => (flags.length ? flags.filter(Boolean).length / flags.length : 0);
