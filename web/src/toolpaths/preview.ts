import type { Vec } from "../api";
import { h, replace } from "../dom";

// Bambu Studio marks supports with "; FEATURE: Support" (and "Support interface", "Support transition").
const SUPPORT_ROLE = 5; // FeatureRole.Support in @chestnutlabs/toolpath-core

interface PreviewElement extends HTMLElement {
  theme: { background?: string; gridColor?: string; bedColor?: string } | undefined;
  source: ArrayBuffer | null;
  buildVolume: { x: number; y: number; z: number } | undefined;
  layerRange: [number, number] | null;
  showTravel: boolean;
  hiddenFeatureRoles: number[];
  state: { layerCount?: number };
}

/** Layer index from the slider value, clamped to the parsed layer count. */
export const clampLayer = (value: number, count: number) => Math.max(1, Math.min(Math.round(value), Math.max(count, 1)));

/** The plate's G-code inside a .gcode.3mf, for the fallback renderer. */
export async function plateGcode(buf: ArrayBuffer): Promise<string> {
  const { unzipSync, strFromU8 } = await import("fflate");
  const files = unzipSync(new Uint8Array(buf), { filter: (f) => /^Metadata\/plate_\d+\.gcode$/.test(f.name) });
  const name = Object.keys(files).sort()[0];
  if (!name) throw new Error("the sliced file has no plate G-code");
  return strFromU8(files[name]!);
}

/**
 * Toolpaths of a sliced .gcode.3mf with a layer slider, a travel toggle and supports in
 * their own colour (PRD V-10). Uses @chestnutlabs/gcode-preview-element, which reads the
 * .gcode.3mf directly; gcode-preview is the fallback when it can't parse the file.
 */
export class ToolpathPreview {
  readonly el = h("div", { class: "toolpaths" });
  private stage = h("div", { class: "toolpath-stage" });
  private layer = h("input", { type: "range", min: "1", max: "1", value: "1", "aria-label": "Show layers up to", disabled: true });
  private layerLabel = h("output", { class: "small" }, "");
  private travel = h("input", { type: "checkbox", "aria-label": "Show travel moves" });
  private supports = h("input", { type: "checkbox", checked: true, "aria-label": "Show supports" });
  private status = h("p", { class: "muted small", role: "status" });
  private element: PreviewElement | null = null;
  private fallback: { setLayers: (n: number) => void; setTravel: (on: boolean) => void } | null = null;
  private count = 0;

  constructor() {
    this.layer.addEventListener("input", () => this.applyLayer());
    this.travel.addEventListener("change", () => this.applyTravel());
    this.supports.addEventListener("change", () => this.applySupports());
    replace(this.el,
      h("div", { class: "toolpath-controls" },
        h("label", { class: "slider" }, "Layer ", this.layer, this.layerLabel),
        h("label", {}, this.travel, " Travel moves"),
        h("label", {}, this.supports, " Supports ", h("span", { class: "swatch support", "aria-hidden": "true" }))),
      this.stage, this.status);
  }

  async load(url: string, volume: Vec) {
    this.status.textContent = "Loading toolpaths…";
    this.count = 0;
    this.layer.disabled = true;
    const res = await fetch(url);
    if (!res.ok) {
      const body = (await res.json().catch(() => ({}))) as { error?: string };
      this.status.textContent = body.error ?? `Could not load the sliced file (${res.status}).`;
      this.stage.replaceChildren();
      return;
    }
    const buf = await res.arrayBuffer();
    try {
      await this.loadElement(buf.slice(0), volume);
    } catch (e) {
      console.warn("toolpath element failed, using gcode-preview", e);
      await this.loadFallback(buf, volume);
    }
  }

  private async loadElement(buf: ArrayBuffer, volume: Vec) {
    // Register explicitly: the package's side-effect-only "define" entry is tree-shaken away in builds.
    const { defineGcodePreview } = await import("@chestnutlabs/gcode-preview-element");
    defineGcodePreview();
    this.fallback = null;
    const el = document.createElement("gcode-preview") as PreviewElement;
    el.setAttribute("quality", "auto");
    el.setAttribute("show-travel", "false");
    el.setAttribute("view", "iso");
    el.setAttribute("frame-content", "object");
    const css = getComputedStyle(document.body);
    const color = (v: string, d: string) => css.getPropertyValue(v).trim() || d;
    el.theme = { background: color("--viewer-bg", "#f4f5f7"), gridColor: color("--border", "#dde1e6"), bedColor: color("--muted", "#5f6b7a") };
    el.className = "toolpath-canvas";
    el.buildVolume = { x: volume.X, y: volume.Y, z: volume.Z };
    this.stage.replaceChildren(el);
    this.element = el;
    await new Promise<void>((resolve, reject) => {
      const timer = setTimeout(() => reject(new Error("timed out parsing the toolpaths")), 30_000);
      el.addEventListener("ready", () => {
        clearTimeout(timer);
        resolve();
      }, { once: true });
      for (const type of ["parse-error", "error"]) {
        el.addEventListener(type, (e) => {
          clearTimeout(timer);
          reject(new Error(String((e as CustomEvent).detail?.message ?? type)));
        }, { once: true });
      }
      el.source = buf;
    });
    this.setCount(el.state.layerCount ?? 0);
    this.applyTravel();
    this.applySupports();
  }

  private async loadFallback(buf: ArrayBuffer, volume: Vec) {
    const gcode = await plateGcode(buf);
    const { init } = await import("gcode-preview");
    this.element = null;
    const canvas = h("canvas", { class: "toolpath-canvas" });
    this.stage.replaceChildren(canvas);
    const preview = init({
      canvas,
      buildVolume: { x: volume.X, y: volume.Y, z: volume.Z },
      backgroundColor: getComputedStyle(document.body).getPropertyValue("--viewer-bg").trim() || "#f4f5f7",
      extrusionColor: "#F2754E",
      renderTravel: this.travel.checked,
    });
    preview.processGCode(gcode);
    this.fallback = {
      setLayers: (n) => {
        preview.endLayer = n;
        preview.render();
      },
      setTravel: (on) => {
        preview.renderTravel = on;
        preview.render();
      },
    };
    this.supports.disabled = true; // the fallback renderer has no feature roles
    this.setCount(preview.layers.length);
  }

  private setCount(n: number) {
    this.count = n;
    this.layer.max = String(Math.max(n, 1));
    this.layer.value = String(Math.max(n, 1));
    this.layer.disabled = n < 1;
    this.status.textContent = n ? `${n} layers` : "No layers found.";
    this.applyLayer();
  }

  private applyLayer() {
    const n = clampLayer(Number(this.layer.value), this.count);
    this.layerLabel.textContent = this.count ? `${n} / ${this.count}` : "";
    if (this.element) this.element.layerRange = [0, n - 1];
    this.fallback?.setLayers(n);
  }

  private applyTravel() {
    if (this.element) this.element.showTravel = this.travel.checked;
    this.fallback?.setTravel(this.travel.checked);
  }

  private applySupports() {
    if (this.element) this.element.hiddenFeatureRoles = this.supports.checked ? [] : [SUPPORT_ROLE];
  }
}
