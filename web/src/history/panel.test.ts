import { expect, test } from "vitest";
import type { Estimate, VersionInfo } from "../api";
import { HistoryPanel, changedParams, estimateFor } from "./panel";

const est = (printer: string, minutes: number, grams: number): Estimate => ({
  project: "cube", version: 1, printer_id: printer, status: "done", minutes, grams, metres: 1, filament: "PLA",
  layers: 100, layer_height_mm: 0.2, supports: false, computed_at: "", key: printer + minutes,
});
const v = (n: number, params: Record<string, unknown>, estimates: Estimate[] = []): VersionInfo => ({
  version: n, params, created_at: new Date().toISOString(), size_mm: { X: 20, Y: 20, Z: 20 }, estimates,
});

test("changed parameters are named", () => {
  expect(changedParams(v(2, { size: 25, wall: 2 }), v(1, { size: 20, wall: 2 }))).toEqual(["size"]);
  expect(changedParams(v(1, { size: 20 }), undefined)).toEqual([]);
});

test("the estimate shown is the selected printer's", () => {
  const ver = v(1, {}, [est("a1", 21, 7.4), est("sim", 10, 5)]);
  expect(estimateFor(ver, "sim")?.minutes).toBe(10);
  expect(estimateFor(ver, "")?.minutes).toBe(21);
});

test("comparing two versions highlights the faster, lighter one", () => {
  const panel = new HistoryPanel();
  (panel as unknown as { versions: VersionInfo[] }).versions = [v(1, { size: 20 }, [est("a1", 21, 7.4)]), v(2, { size: 30 }, [est("a1", 40, 20)])];
  (panel as unknown as { picked: number[] }).picked = [1, 2];
  (panel as unknown as { render: () => void }).render();
  const better = [...panel.el.querySelectorAll("td.better")].map((td) => td.textContent);
  expect(better).toEqual(["21 min", "7.4 g"]);
  expect(panel.el.querySelectorAll("td.diff").length).toBe(2); // the size row, both cells
});
