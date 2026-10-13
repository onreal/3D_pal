import { expect, test } from "vitest";
import type { PrinterStatus } from "../api";
import { LayerClock, duration, followRange, isActive, liveLabel } from "./follow";

const st = (o: Partial<PrinterStatus>): PrinterStatus => ({ state: "printing", progress: 0.42, layer: 37, total_layers: 100, ...o });

test("the in-layer estimate comes from the previous layer's time and resets on each layer", () => {
  const c = new LayerClock();
  expect(c.observe(5, 1000)).toBe(true);
  expect(c.fraction(2000)).toBeNull(); // first sight: no layer measured yet
  expect(c.observe(5, 3000)).toBe(false);
  c.observe(6, 11_000); // layer 5 took 10 s
  expect(c.fraction(16_000)).toBeCloseTo(0.5);
  expect(c.fraction(60_000)).toBe(0.97); // never claims the layer is done
  c.observe(9, 20_000); // a jump measures nothing
  expect(c.fraction(21_000)).toBeNull();
  c.reset();
  expect(c.fraction(21_000)).toBeNull();
});

test("labels and ranges", () => {
  expect(liveLabel(st({ remaining_s: 3900 }))).toBe("Printing · layer 37 / 100 · 42% · about 1 h 05 min left");
  expect(liveLabel(st({ state: "finished", layer: 100, progress: 1 }))).toBe("Finished · layer 100 / 100");
  expect(duration(600)).toBe("10 min");
  expect(followRange(st({}), 100)).toEqual([0, 36]);
  expect(followRange(st({ layer: 0 }), 100)).toEqual([0, 99]);
  expect(followRange(st({ layer: 250 }), 100)).toEqual([0, 99]);
  expect(followRange(st({ state: "finished" }), 100)).toEqual([0, 99]);
  expect(isActive(st({ state: "paused" }))).toBe(true);
  expect(isActive(st({ state: "idle" }))).toBe(false);
  expect(isActive(null)).toBe(false);
});
