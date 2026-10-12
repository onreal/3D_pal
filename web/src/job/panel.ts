import { api, type Approval, type Job } from "../api";
import { h, replace, toast } from "../dom";
import { estimateFacts } from "../estimates/card";

/** The job waiting for approval, with its estimate and the Approve button (PRD V-11, FR-11). */
export class JobPanel {
  readonly el = h("section", { class: "panel job-panel", "aria-label": "Print job", hidden: true });
  private job: Job | null = null;
  private printerName = "";
  private requestedBy = "";
  onShowToolpaths: (job: Job) => void = () => {};

  async load(project: string) {
    try {
      const pending = await api.pendingJobs(project);
      const job = pending[0];
      if (job) this.show({ job, estimate: job.estimate!, printer_name: job.printer_id, viewer_url: "" });
      else this.hide();
    } catch {
      this.hide();
    }
  }

  hide() {
    this.job = null;
    this.el.hidden = true;
  }

  /** Called for job events: a new request, or an approval made elsewhere. */
  show(a: Partial<Approval> & { job: Job; client?: string }) {
    const sameJob = this.job?.id === a.job.id;
    if (!sameJob) this.requestedBy = "";
    if (a.client) this.requestedBy = a.client;
    this.job = { ...(sameJob ? this.job : {}), ...a.job, estimate: a.estimate ?? a.job.estimate ?? this.job?.estimate };
    if (a.printer_name) this.printerName = a.printer_name;
    this.render();
  }

  private render() {
    const job = this.job;
    if (!job) return this.hide();
    this.el.hidden = false;
    const approve = h("button", { class: "primary big", type: "button", "data-testid": "approve" }, "Approve print");
    approve.addEventListener("click", async () => {
      approve.disabled = true;
      approve.textContent = "Approving…";
      try {
        this.job = await api.approve(job.id);
        toast("Approved. Tell the agent to print it.", "ok");
        this.render();
      } catch (e) {
        toast((e as Error).message, "error");
        approve.disabled = false;
        approve.textContent = "Approve print";
      }
    });
    let action: HTMLElement;
    if (job.printed) action = h("p", { class: "ok" }, "Sent to the printer.");
    else if (job.approved) action = h("p", { class: "ok", "data-testid": "approved" }, "Approved. Tell the agent to print it; it will call print_job.");
    else action = h("div", {}, approve, h("p", { class: "muted small" }, "Nothing prints until you approve this exact file."));

    replace(this.el,
      h("h2", {}, job.printed ? "Printed" : job.approved ? "Approved" : "Waiting for your approval"),
      h("p", { class: "job-meta" }, `${job.project} · version ${job.version} · ${this.printerName || job.printer_id} · job `, h("code", {}, job.id),
        this.requestedBy ? h("span", { class: "small muted", "data-testid": "requested-by" }, ` · requested by ${this.requestedBy}`) : null),
      job.estimate ? estimateFacts(job.estimate) : null,
      h("button", { class: "link", type: "button", onclick: () => this.onShowToolpaths(job) }, "Show toolpaths"),
      action);
  }
}
