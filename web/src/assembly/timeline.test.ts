import { describe, expect, test } from "vitest";
import { applyOrder, approachDirection, currentStep, easeOutCubic, move, offsetAt, partState } from "./timeline";

describe("assembly timeline", () => {
  test("each part enters during its own step", () => {
    expect(partState(0, 0)).toEqual({ kind: "hidden" });
    expect(partState(0, 0.25)).toEqual({ kind: "entering", progress: 0.25 });
    expect(partState(0, 1)).toEqual({ kind: "placed" });
    expect(partState(2, 1.5)).toEqual({ kind: "hidden" });
  });
  test("parts drop in from above, side parts from their side", () => {
    expect(approachDirection([0, 0, 5], [0, 0, 0])).toEqual([0, 0, 1]);
    const d = approachDirection([10, 0, 0], [0, 0, 0]);
    expect(d[0]).toBeGreaterThan(0.4);
    expect(d[2]).toBeGreaterThan(0.8);
    expect(Math.hypot(...d)).toBeCloseTo(1);
  });
  test("offset shrinks to zero as the part lands", () => {
    const dir: [number, number, number] = [0, 0, 1];
    expect(offsetAt(0, 0, dir, 50)[2]).toBe(50);
    expect(offsetAt(0, 0.5, dir, 50)[2]).toBeCloseTo(50 * (1 - easeOutCubic(0.5)));
    expect(offsetAt(0, 1, dir, 50)[2]).toBe(0);
  });
  test("current step for highlighting", () => {
    expect(currentStep(0, 3)).toBe(-1);
    expect(currentStep(0.2, 3)).toBe(0);
    expect(currentStep(1, 3)).toBe(0);
    expect(currentStep(2.4, 3)).toBe(2);
    expect(currentStep(9, 3)).toBe(2);
  });
  test("saved order and moving steps", () => {
    const parts = [{ id: "a" }, { id: "b" }, { id: "c" }, { id: "new" }];
    expect(applyOrder(parts, ["c", "a", "gone", "b"]).map((p) => p.id)).toEqual(["c", "a", "b", "new"]);
    expect(applyOrder(parts, null)).toBe(parts);
    expect(move(["a", "b", "c"], 2, 0)).toEqual(["c", "a", "b"]);
    expect(move(["a", "b"], 0, 5)).toEqual(["a", "b"]);
  });
});
