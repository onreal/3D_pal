import { expect, test } from "vitest";
import { overhangColors, overhangFaces, overhangShare } from "./overhangs";

const tri = (z: number, nz: number) => ({
  pos: [0, 0, z, 1, 0, z, 0, 1, z],
  nrm: [0, 0, nz, 0, 0, nz, 0, 0, nz],
});

test("a ceiling is an overhang; the face on the bed is not", () => {
  const floor = tri(0, -1), ceiling = tri(10, -1);
  const got = overhangFaces(
    new Float32Array([...floor.pos, ...ceiling.pos]),
    new Float32Array([...floor.nrm, ...ceiling.nrm]),
  );
  expect(got).toEqual([false, true]);
});

test("walls up to 45° from vertical are fine", () => {
  const at = (deg: number) => tri(5, -Math.sin((deg * Math.PI) / 180));
  const faces = [at(0), at(30), at(60)];
  const got = overhangFaces(
    new Float32Array([...tri(0, 1).pos, ...faces.flatMap((f) => f.pos)]),
    new Float32Array([...tri(0, 1).nrm, ...faces.flatMap((f) => f.nrm)]),
  );
  expect(got).toEqual([false, false, false, true]);
});

test("the angle setting moves the limit", () => {
  const f = tri(5, -Math.sin((50 * Math.PI) / 180));
  const pos = new Float32Array([...tri(0, 1).pos, ...f.pos]);
  const nrm = new Float32Array([...tri(0, 1).nrm, ...f.nrm]);
  expect(overhangFaces(pos, nrm, 45)[1]).toBe(true);
  expect(overhangFaces(pos, nrm, 60)[1]).toBe(false);
});

test("colours and share", () => {
  const c = overhangColors([false, true], [1, 1, 1], [1, 0, 0]);
  expect(Array.from(c.slice(0, 3))).toEqual([1, 1, 1]);
  expect(Array.from(c.slice(9, 12))).toEqual([1, 0, 0]);
  expect(overhangShare([false, true, true, false])).toBe(0.5);
});
