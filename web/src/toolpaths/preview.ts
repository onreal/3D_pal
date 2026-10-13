import type { PrinterStatus, Vec } from "../api";
import { h, replace } from "../dom";
import { LayerClock, followRange, isActive, liveLabel } from "./follow";

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
  observeProgress(obs: ProgressObservation): { band: [number, number] | null } | null;
  clearProgress(): void;
  controls: { setScrubPosition(segIndex: number | null): void };
}

/** The element's live-progress input (toolpath-core ProgressObservation v1). */
interface ProgressObservation {
  v: 1;
  timestampMs: number;
  position?: { layer?: number; totalLayers?: number; percent?: number; percentBasis?: "job" };
  state?: "printing" | "paused" | "complete" | "cancelled" | "unknown";
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
  private followBox = h("input", { type: "checkbox", checked: true, "aria-label": "Follow the print", "data-testid": "follow-print" });
  private followWrap = h("label", { class: "follow-print", hidden: true }, this.followBox, " Follow print");
  private live = h("p", { class: "live small", "data-testid": "follow-live", hidden: true });
  private element: PreviewElement | null = null;
  private loadedUrl = "";
  private printUrl: string | null = null;
  private printStatus: PrinterStatus | null = null;
  private clock = new LayerClock();
  private timer = 0;
  /** Called when following turns on or off, so the page can load the right file. */
  onFollowChange: () => void = () => {};
  private fallback: { setLayers: (n: number) => void; setTravel: (on: boolean) => void } | null = null;
  private count = 0;

  constructor() {
    this.layer.addEventListener("input", () => {
      if (this.following) { // moving the slider by hand stops following
        this.followBox.checked = false;
        this.stopFollow(true);
      }
      this.applyLayer();
    });
    this.followBox.addEventListener("change", () => {
      if (!this.followBox.checked) this.stopFollow();
      this.onFollowChange();
    });
    this.travel.addEventListener("change", () => this.applyTravel());
    this.supports.addEventListener("change", () => this.applySupports());
    replace(this.el,
      h("div", { class: "toolpath-controls" },
        h("label", { class: "slider" }, "Layer ", this.layer, this.layerLabel),
        h("label", {}, this.travel, " Travel moves"),
        h("label", {}, this.supports, " Supports ", h("span", { class: "swatch support", "aria-hidden": "true" })),
        this.followWrap),
      this.live, this.stage, this.status);
  }

  /** True while the view tracks a running (or just finished) print. */
  get following() {
    return this.followBox.checked && this.printUrl !== null;
  }

  /** Turns following on (the status bar's Watch). */
  follow() {
    this.followBox.checked = true;
  }

  /** The job file of the print being followed. */
  get followUrl() {
    return this.printUrl;
  }

  /**
   * The selected printer's print: its job file and latest status (null when it prints nothing
   * we sent). Returns true when the page should load the job file to follow it.
   */
  setPrint(url: string | null, status: PrinterStatus | null): boolean {
    if (url !== this.printUrl) this.clock.reset();
    const wasFollowing = this.following;
    this.printUrl = url && status && status.state !== "idle" && status.state !== "offline" ? url : null;
    this.printStatus = status;
    this.followWrap.hidden = this.printUrl === null;
    if (!this.following) {
      if (wasFollowing) this.stopFollow();
      return false;
    }
    if (this.loadedUrl !== this.printUrl) return true;
    this.applyFollow();
    return false;
  }

  async load(url: string, volume: Vec) {
    if (url === this.loadedUrl && this.count > 0) { // already showing this file
      if (this.following) this.applyFollow();
      return;
    }
    this.loadedUrl = "";
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
    this.loadedUrl = url;
    if (this.following && url === this.printUrl) this.applyFollow();
    else this.stopFollow();
  }

  /** Shows the printed layers and marks the current one; animates within it when it can. */
  private applyFollow() {
    const st = this.printStatus;
    if (!st || !this.count) return;
    const [, last] = followRange(st, this.count);
    this.layer.value = String(last + 1);
    this.layerLabel.textContent = `${last + 1} / ${this.count}`;
    if (this.element) this.element.layerRange = [0, last];
    this.fallback?.setLayers(last + 1);
    this.live.hidden = false;
    this.live.textContent = liveLabel(st) + (this.clock.fraction(performance.now()) !== null && isActive(st) ? " · position within the layer is estimated" : "");
    if (this.clock.observe(st.layer, performance.now())) this.element?.controls.setScrubPosition(null);
    if (isActive(st) && !this.timer) this.timer = window.setInterval(() => this.tick(), 500);
    if (!isActive(st)) {
      this.stopTimer();
      this.element?.controls.setScrubPosition(null);
    }
    this.tick();
  }

  private observation(): ProgressObservation | null {
    const st = this.printStatus;
    if (!st) return null;
    const state = st.state === "printing" ? "printing" : st.state === "paused" ? "paused" : st.state === "finished" ? "complete" : st.state === "failed" ? "cancelled" : "unknown";
    return { v: 1, timestampMs: Date.now(), state,
      position: { layer: Math.max(0, st.layer - 1), totalLayers: st.total_layers || undefined, percent: st.progress, percentBasis: "job" } };
  }

  /** Moves the cut inside the current layer to the estimated position. */
  private tick() {
    const el = this.element, obs = this.observation();
    if (!el || !obs) return;
    const mapped = el.observeProgress(obs);
    const frac = this.clock.fraction(performance.now());
    if (mapped?.band && frac !== null && isActive(this.printStatus)) {
      const [lo, hi] = mapped.band;
      el.controls.setScrubPosition(Math.round(lo + frac * (hi - lo)));
    }
  }

  private stopTimer() {
    window.clearInterval(this.timer);
    this.timer = 0;
  }

  /** keepLayer leaves the slider where the user put it. */
  private stopFollow(keepLayer = false) {
    this.stopTimer();
    this.live.hidden = true;
    if (this.element) {
      this.element.clearProgress();
      this.element.controls.setScrubPosition(null);
    }
    if (this.count && !keepLayer) {
      this.layer.value = String(this.count);
      this.applyLayer();
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
