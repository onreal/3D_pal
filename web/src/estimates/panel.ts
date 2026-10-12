import { api, type Estimate } from "../api";
import { h, replace, toast } from "../dom";
import { estimateFacts } from "./card";

/** Estimate any preview on request, without approving anything (PRD FR-32 to FR-37). */
export class EstimatePanel {
  readonly el = h("section", { class: "panel estimate-panel", "aria-label": "Estimate" });
  private project = "";
  private version = 0;
  private printerId = "";
  private current: Estimate | null = null;
  onDone: (e: Estimate) => void = () => {};

  set(project: string, version: number, printerId: string, known?: Estimate) {
    const changed = project !== this.project || version !== this.version || printerId !== this.printerId;
    this.project = project;
    this.version = version;
    this.printerId = printerId;
    if (changed) this.current = known ?? null;
    this.render();
  }

  /** Estimate events from the server (this panel's own runs, or the agent's). */
  receive(e: Estimate) {
    if (e.project !== this.project || e.version !== this.version || (this.printerId && e.printer_id !== this.printerId)) return;
    this.current = e;
    this.render();
    if (e.status === "done") this.onDone(e);
  }

  async estimate() {
    if (!this.project) return;
    this.current = { status: "running" } as Estimate;
    this.render();
    try {
      const e = await api.startEstimate(this.project, this.version, this.printerId);
      if (e.status !== "running" || this.current?.status === "running") this.receive(e);
    } catch (err) {
      this.current = null;
      this.render();
      toast((err as Error).message, "error");
    }
  }

  private render() {
    const btn = h("button", { type: "button", "data-testid": "estimate", disabled: this.current?.status === "running" || !this.project },
      this.current?.status === "done" ? "Estimate again" : "Estimate");
    btn.addEventListener("click", () => void this.estimate());
    replace(this.el,
      h("div", { class: "panel-head" }, h("h2", {}, this.version ? `Estimate · v${this.version}` : "Estimate"), btn),
      this.current ? estimateFacts(this.current) : h("p", { class: "muted small" }, "Slices this version for the selected printer: time, filament and cost. Nothing is sent to the printer."));
  }
}
