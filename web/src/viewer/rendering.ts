import type { EventMap } from "../events";
import { h, replace } from "../dom";

type RenderEvent = EventMap["render"];

/** Who started a render: the agent session's client, or this viewer (no session). */
export function renderedBy(e: { session?: string; client?: string }): string {
  if (!e.session) return "from the viewer";
  return `by ${e.client || "the agent"}`;
}

interface Running { id: number; project: string; by: string; started: number }

/** A render whose end never arrived (the live connection dropped) stops showing after this. */
export const STALE_MS = 10 * 60 * 1000;

/**
 * The banner over the model while it renders, whoever asked: the Render 3D button, the agent's
 * compile_model or a restore. The server brackets every compile with render events
 * (started → done | failed), so the model on screen is never mistaken for the one being made.
 * Renders of every project are tracked: the agent's first render of a new project, or one that
 * started before this page opened its project, shows too, named by project when it isn't the
 * open one. A failure (open project only) stays until dismissed or the next render.
 */
export class RenderStatus {
  readonly el = h("div", { class: "render-status", role: "status", "aria-live": "polite", hidden: true, "data-testid": "render-status" });
  private project = "";
  private running = new Map<number, Running>();
  private failure: { by: string; error: string } | null = null;
  private done: { version: number; by: string; secs: number } | null = null;
  private timer: ReturnType<typeof setInterval> | null = null;
  private doneTimer: ReturnType<typeof setTimeout> | null = null;
  /** Told after every redraw whether the open project is rendering (the page dims the model). */
  onBusy: (busy: boolean) => void = () => {};

  /** Switching project forgets the previous one's result; renders in progress stay. */
  setProject(project: string) {
    if (project === this.project) return;
    this.project = project;
    this.failure = null;
    this.done = null;
    this.draw();
  }

  /** True while the open project has a render in progress. */
  get busy(): boolean {
    return this.active().some((r) => r.project === this.project);
  }

  /** Renders in progress, dropping any whose end was missed. */
  private active(): Running[] {
    for (const [id, r] of this.running) if (Date.now() - r.started > STALE_MS) this.running.delete(id);
    return [...this.running.values()].sort((a, b) => a.started - b.started);
  }

  receive(e: RenderEvent) {
    const by = renderedBy(e);
    const mine = e.project === this.project;
    if (e.state === "started") {
      this.running.set(e.id, { id: e.id, project: e.project, by, started: Date.now() });
      if (mine) this.failure = this.done = null;
    } else {
      const r = this.running.get(e.id);
      this.running.delete(e.id);
      const secs = r ? Math.round((Date.now() - r.started) / 1000) : 0;
      if (mine && e.state === "failed") this.failure = { by, error: e.error || "The render failed." };
      else if (mine && !this.busy) this.done = { version: e.version ?? 0, by, secs };
    }
    this.draw();
  }

  private draw() {
    this.paint();
    this.onBusy(this.busy);
  }

  private paint() {
    if (this.doneTimer) clearTimeout(this.doneTimer);
    this.doneTimer = null;
    const running = this.active();
    const busy = running.length > 0;
    if (busy && !this.timer) this.timer = setInterval(() => this.draw(), 1000);
    if (!busy && this.timer) {
      clearInterval(this.timer);
      this.timer = null;
    }

    if (busy) {
      // The open project's render first; otherwise another project's, named.
      const first = running.find((r) => r.project === this.project) ?? running[0]!;
      const mine = first.project === this.project;
      const secs = Math.round((Date.now() - first.started) / 1000);
      const queued = running.length - 1;
      this.el.className = "render-status rendering";
      this.el.hidden = false;
      replace(this.el,
        h("span", { class: "spinner", "aria-hidden": "true" }),
        h("strong", {}, mine ? "Rendering 3D…" : `Rendering ${first.project}…`),
        h("span", { class: "small" }, ` ${first.by} · ${secs} s`),
        queued > 0 ? h("span", { class: "small" }, ` · ${queued} more queued`) : null,
        h("span", { class: "small muted render-note" }, mine
          ? "The model below is the previous version."
          : "It opens here when it is done, if you follow that session's work."));
      return;
    }
    if (this.failure) {
      this.el.className = "render-status failed";
      this.el.hidden = false;
      replace(this.el,
        h("strong", {}, `Render failed (${this.failure.by})`),
        h("button", { type: "button", class: "link", "aria-label": "Dismiss", onclick: () => { this.failure = null; this.draw(); } }, "×"),
        h("pre", { class: "render-error" }, this.failure.error));
      return;
    }
    if (this.done) {
      this.el.className = "render-status done";
      this.el.hidden = false;
      replace(this.el, h("strong", {}, `Rendered v${this.done.version}`), h("span", { class: "small" }, ` ${this.done.by} · ${this.done.secs} s`));
      this.doneTimer = setTimeout(() => {
        this.done = null;
        this.draw();
      }, 4000);
      return;
    }
    this.el.hidden = true;
    replace(this.el);
  }
}
