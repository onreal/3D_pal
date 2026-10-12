import type { Estimate } from "../api";
import { h } from "../dom";
import { formatCost, formatMinutes } from "../format";

/** Time, filament, layers, supports and cost of one estimate. */
export function estimateFacts(e: Estimate): HTMLElement {
  if (e.status === "running") return h("p", { class: "muted", role: "status" }, h("span", { class: "spinner", "aria-hidden": "true" }), " Slicing…");
  if (e.status === "failed") return h("p", { class: "error" }, "Slicing failed: ", e.error ?? "unknown error");
  const cost = formatCost(e);
  return h("dl", { class: "facts" },
    h("div", {}, h("dt", {}, "Print time"), h("dd", { "data-field": "time" }, formatMinutes(e.minutes))),
    h("div", {}, h("dt", {}, "Filament"), h("dd", { "data-field": "grams" }, `${e.grams.toFixed(1)} g`, e.filament ? ` ${e.filament}` : "")),
    h("div", {}, h("dt", {}, "Length"), h("dd", {}, `${e.metres.toFixed(2)} m`)),
    h("div", {}, h("dt", {}, "Layers"), h("dd", {}, String(e.layers), e.layer_height_mm ? ` × ${e.layer_height_mm} mm` : "")),
    h("div", {}, h("dt", {}, "Supports"), h("dd", { "data-field": "supports" }, e.supports ? "Yes" : "None needed")),
    cost ? h("div", {}, h("dt", {}, "Cost"), h("dd", { "data-field": "cost" }, cost)) : null,
  );
}
