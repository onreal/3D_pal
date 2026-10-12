import { describe, expect, test } from "vitest";
import type { Estimate } from "./api";
import { compareEstimates, formatCost, formatMinutes, formatSize, timeAgo } from "./format";

const est = (o: Partial<Estimate>): Estimate => ({
  project: "cube", version: 1, printer_id: "a1", status: "done", minutes: 21, grams: 7.4, metres: 2.48,
  filament: "PLA", layers: 100, layer_height_mm: 0.2, supports: false, computed_at: "", key: "k", ...o,
});

describe("format", () => {
  test("size with one decimal", () => {
    expect(formatSize({ X: 20, Y: 20, Z: 20 })).toBe("20.0 × 20.0 × 20.0 mm");
  });
  test("minutes", () => {
    expect(formatMinutes(21)).toBe("21 min");
    expect(formatMinutes(125)).toBe("2 h 5 min");
  });
  test("cost only when set", () => {
    expect(formatCost({ cost: 0 })).toBe("");
    expect(formatCost({ cost: 0.19, currency: "EUR" })).toMatch(/0[.,]19/);
  });
  test("time ago", () => {
    const now = Date.parse("2026-10-12T12:00:00Z");
    expect(timeAgo("2026-10-12T11:59:30Z", now)).toBe("just now");
    expect(timeAgo("2026-10-12T11:45:00Z", now)).toBe("15 min ago");
  });
  test("compare estimates marks the cheaper and faster one", () => {
    const got = compareEstimates(est({ minutes: 20, grams: 7, cost: 0.2 }), est({ minutes: 30, grams: 7, cost: 0.3 }));
    expect(got).toEqual({ minutes: "lower", grams: "same", cost: "lower", layers: "same" });
  });
});
