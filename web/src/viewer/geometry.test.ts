import { describe, expect, test } from "vitest";
import { bounds, distance, fitsVolume, nearestVertex, overflowAxes, placeOnBedOffset, sectionRange, size, snap } from "./geometry";

const A1 = { X: 256, Y: 256, Z: 256 };

describe("geometry", () => {
  test("bounds and size", () => {
    const b = bounds([0, 0, 5, 20, 10, 5, 10, 30, 25]);
    expect(b).toEqual({ min: [0, 0, 5], max: [20, 30, 25] });
    expect(size(b)).toEqual({ X: 20, Y: 30, Z: 20 });
  });
  test("place on bed centres X and Y and drops to Z = 0", () => {
    expect(placeOnBedOffset({ min: [10, 10, 5], max: [30, 50, 25] })).toEqual([-20, -30, -5]);
  });
  test("fits the A1", () => {
    expect(fitsVolume({ X: 20, Y: 20, Z: 20 }, A1)).toBe(true);
    expect(fitsVolume({ X: 256, Y: 256, Z: 256 }, A1)).toBe(true);
    expect(fitsVolume({ X: 300, Y: 300, Z: 300 }, A1)).toBe(false);
    expect(fitsVolume({ X: 50, Y: 280, Z: 10 }, { X: 300, Y: 100, Z: 100 })).toBe(true);
    expect(fitsVolume({ X: 150, Y: 150, Z: 10 }, { X: 200, Y: 200, Z: 100 }, "round")).toBe(false);
  });
  test("overflow axes name what is too big", () => {
    expect(overflowAxes({ X: 300, Y: 20, Z: 300 }, A1)).toEqual(["width", "height"]);
  });
  test("snap to the nearest vertex within the radius", () => {
    const tri: [number, number, number][] = [[0, 0, 0], [10, 0, 0], [0, 10, 0]];
    expect(nearestVertex(tri, [9, 1, 0])).toEqual([10, 0, 0]);
    expect(snap(tri, [9, 1, 0], 2)).toEqual([10, 0, 0]);
    expect(snap(tri, [5, 5, 0], 2)).toEqual([5, 5, 0]);
    expect(distance([0, 0, 0], [3, 4, 0])).toBe(5);
  });
  test("section range per axis", () => {
    expect(sectionRange({ min: [0, -5, 0], max: [10, 5, 20] }, "y")).toEqual([-5, 5]);
  });
});
