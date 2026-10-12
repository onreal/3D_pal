import { api, urls, type AssemblyPart, type AssemblyResult, type Capabilities } from "../api";
import { download, h, replace } from "../dom";
import { applyOrder, currentStep, move } from "./timeline";
import { AssemblyView } from "./view";

const SPEEDS = [0.5, 1, 2];

function savedOrder(key: string): string[] | null {
  try {
    const v = localStorage.getItem(key);
    return v ? (JSON.parse(v) as string[]) : null;
  } catch {
    return null;
  }
}

/**
 * Assembly mode: the parts added one by one along a timeline (play, pause, step, scrub),
 * flying into place from above, with the steps listed in build order (PRD request:
 * "assembly mode with a timeline where all parts are added together").
 */
export class AssemblyPanel {
  readonly el = h("div", { class: "assembly" });
  private stage = h("div", { class: "assembly-stage", "data-testid": "assembly-stage" });
  private list = h("ol", { class: "assembly-steps", "data-testid": "assembly-steps" });
  private status = h("p", { class: "muted small", role: "status" });
  private scrub = h("input", { type: "range", min: "0", max: "1", step: "0.01", value: "0", "aria-label": "Assembly timeline", disabled: true, "data-testid": "assembly-scrub" });
  private playBtn = h("button", { type: "button", class: "primary", "aria-label": "Play", "data-testid": "assembly-play", disabled: true }, "▶ Play");
  private stepLabel = h("output", { class: "small", "data-testid": "assembly-step" }, "");
  private speed = h("select", { "aria-label": "Speed" }, ...SPEEDS.map((s) => h("option", { value: String(s), selected: s === 1 }, `${s}×`)));
  private ghosts = h("input", { type: "checkbox", checked: true, "aria-label": "Show the parts still to come" });
  private xray = h("input", { type: "checkbox", "aria-label": "See-through parts" });
  private view: AssemblyView | null = null;
  private result: AssemblyResult | null = null;
  private parts: AssemblyPart[] = [];
  private project = "";
  private version = 0;
  private t = 0;
  private playing = false;
  private last = 0;
  private raf = 0;
  private caps: Pick<Capabilities, "build_volume" | "bed_shape"> | null = null;

  constructor() {
    this.playBtn.addEventListener("click", () => (this.playing ? this.pause() : this.play()));
    this.scrub.addEventListener("input", () => {
      this.pause();
      this.setTime(Number(this.scrub.value));
    });
    this.ghosts.addEventListener("change", () => this.view?.setGhosts(this.ghosts.checked));
    this.xray.addEventListener("change", () => this.view?.setXray(this.xray.checked));
    const btn = (label: string, aria: string, fn: () => void) => h("button", { type: "button", "aria-label": aria, onclick: fn }, label);
    const shot = h("button", { type: "button" }, "Screenshot");
    shot.addEventListener("click", async () => {
      if (this.view) download(await this.view.screenshot(), `${this.project}_v${this.version}_assembly.png`);
    });
    replace(this.el,
      h("div", { class: "assembly-controls toolbar", role: "toolbar", "aria-label": "Assembly timeline" },
        h("div", { class: "group" },
          btn("⏮", "Start", () => { this.pause(); this.setTime(0); }),
          btn("◀", "Previous step", () => { this.pause(); this.setTime(Math.max(0, Math.ceil(this.t) - 1)); }),
          this.playBtn,
          btn("▶|", "Next step", () => { this.pause(); this.setTime(Math.min(this.n, Math.floor(this.t) + 1)); }),
          btn("⏭", "End", () => { this.pause(); this.setTime(this.n); })),
        h("label", { class: "group slider grow" }, this.scrub, this.stepLabel),
        h("label", { class: "group inline small" }, "Speed ", this.speed),
        h("label", { class: "group inline small" }, this.ghosts, " Ghosts"),
        h("label", { class: "group inline small" }, this.xray, " X-ray"),
        h("div", { class: "group" }, shot)),
      h("div", { class: "assembly-body" },
        this.stage,
        h("aside", { class: "assembly-side" }, h("h3", {}, "Build order"), this.status, this.list)));
  }

  private get n() {
    return this.view?.count ?? 0;
  }

  setPrinter(caps: Pick<Capabilities, "build_volume" | "bed_shape"> | null) {
    this.caps = caps;
    this.view?.setPrinter(caps);
  }

  /** Shows a version's assembly, building it on the server if needed. */
  async open(project: string, version: number) {
    if (project === this.project && version === this.version && this.result?.status === "done") return;
    this.project = project;
    this.version = version;
    this.pause();
    this.result = null;
    replace(this.list);
    this.status.className = "muted small";
    this.status.textContent = "Building the assembly: each part is rendered on its own…";
    try {
      this.receive(await api.assembly(project, version));
    } catch (e) {
      this.status.className = "error small";
      this.status.textContent = (e as Error).message;
    }
  }

  /** A result from the API or the assembly event. */
  receive(r: AssemblyResult) {
    if (r.project !== this.project || r.version !== this.version) return;
    if (r.status === "running") return;
    if (r.status === "failed") {
      this.status.className = "error small";
      this.status.textContent = `The assembly could not be built: ${r.error ?? "unknown error"}`;
      return;
    }
    this.result = r;
    this.parts = applyOrder(r.parts, savedOrder(this.orderKey()));
    void this.show();
  }

  private orderKey() {
    return `printpipe.assembly.${this.project}`;
  }

  private async show() {
    const r = this.result!;
    if (!this.view) {
      this.view = new AssemblyView(this.stage);
      this.view.setPrinter(this.caps);
    }
    this.status.className = "muted small";
    this.status.textContent = r.note ?? `${r.parts.length} steps from the model's assembly declaration.`;
    await this.view.load(r, this.parts, (p) => urls.assemblyPart(this.project, this.version, p.index));
    this.view.setXray(this.xray.checked);
    this.scrub.max = String(this.n);
    this.scrub.disabled = this.playBtn.disabled = this.n === 0;
    this.renderList();
    this.setTime(this.n); // show the finished assembly; Play starts from the beginning
  }

  private renderList() {
    const cur = currentStep(this.t, this.n);
    let solid = 0;
    replace(this.list, ...this.parts.map((p, i) => {
      const index = p.error ? -1 : solid++;
      const up = h("button", { type: "button", class: "link small", "aria-label": `Move ${p.label} earlier`, disabled: i === 0 }, "↑");
      const down = h("button", { type: "button", class: "link small", "aria-label": `Move ${p.label} later`, disabled: i === this.parts.length - 1 }, "↓");
      up.addEventListener("click", () => this.reorder(i, i - 1));
      down.addEventListener("click", () => this.reorder(i, i + 1));
      const row = h("li", { class: `assembly-step${index === cur ? " current" : ""}${p.error ? " failed" : ""}`, "data-step": p.id },
        h("span", { class: "swatch", style: `background:${p.color || "#8A8F99"}` }),
        h("button", { type: "button", class: "step-name", disabled: !!p.error, onclick: () => { this.pause(); this.setTime(index + 1); } },
          p.label, p.count > 1 ? h("span", { class: "muted small" }, ` ×${p.count}`) : null),
        p.error ? h("span", { class: "error small", title: p.error }, "failed") : null,
        h("span", { class: "reorder" }, up, down));
      row.addEventListener("mouseenter", () => this.view?.highlight(index));
      row.addEventListener("mouseleave", () => this.view?.highlight(currentStep(this.t, this.n)));
      return row;
    }));
  }

  private async reorder(from: number, to: number) {
    this.parts = move(this.parts, from, to);
    try {
      localStorage.setItem(this.orderKey(), JSON.stringify(this.parts.map((p) => p.id)));
    } catch {
      /* the order lasts for this page only */
    }
    await this.show();
  }

  private setTime(t: number) {
    this.t = Math.max(0, Math.min(t, this.n));
    this.scrub.value = String(this.t);
    this.view?.setTime(this.t);
    const cur = currentStep(this.t, this.n);
    const solidParts = this.parts.filter((p) => !p.error);
    this.stepLabel.textContent = this.n ? (cur < 0 ? `0 / ${this.n}` : `${cur + 1} / ${this.n} · ${solidParts[cur]?.label ?? ""}`) : "";
    this.view?.highlight(this.playing ? -1 : cur);
    this.list.querySelectorAll(".assembly-step").forEach((li) => li.classList.remove("current"));
    const row = this.list.querySelector(`[data-step="${CSS.escape(solidParts[cur]?.id ?? "")}"]`);
    row?.classList.add("current");
  }

  play() {
    if (!this.n) return;
    if (this.t >= this.n) this.setTime(0);
    this.playing = true;
    this.playBtn.textContent = "⏸ Pause";
    this.playBtn.setAttribute("aria-label", "Pause");
    this.last = performance.now();
    const tick = (now: number) => {
      if (!this.playing) return;
      const dt = (now - this.last) / 1000;
      this.last = now;
      this.setTime(this.t + dt * Number(this.speed.value) * 0.8);
      if (this.t >= this.n) return this.pause();
      this.raf = requestAnimationFrame(tick);
    };
    this.raf = requestAnimationFrame(tick);
  }

  pause() {
    this.playing = false;
    cancelAnimationFrame(this.raf);
    this.playBtn.textContent = "▶ Play";
    this.playBtn.setAttribute("aria-label", "Play");
  }
}
