import { api, type Approval, type Job } from "../api";
import { h, replace, toast } from "../dom";
import { estimateFacts } from "../estimates/card";

/** The job waiting for approval, with its estimate and the Approve button (PRD V-11, FR-11). */
export class JobPanel {
  readonly el = h("section", { class: "panel job-panel", "aria-label": "Print job", hidden: true });
  private job: Job | null = null;
  private printerName = "";
  private requestedBy = "";
  private sendError = ""; // why the last send failed, kept next to the button
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
    if (!sameJob) this.requestedBy = this.sendError = "";
    if (a.client) this.requestedBy = a.client;
    this.job = { ...(sameJob ? this.job : {}), ...a.job, estimate: a.estimate ?? a.job.estimate ?? this.job?.estimate };
    if (a.printer_name) this.printerName = a.printer_name;
    this.render();
  }

  /** The viewer's Print button: slice this version for the printer and wait for approval here. */
  async request(project: string, version: number, printerId: string, printerName = "") {
    this.job = null;
    this.el.hidden = false;
    replace(this.el, h("h2", {}, "Preparing the print"),
      h("p", { class: "muted", role: "status" }, `Slicing ${project} version ${version}${printerName ? ` for ${printerName}` : ""}…`));
    try {
      const a = await api.requestJob(project, version, printerId);
      this.show({ ...a, client: "you" });
    } catch (e) {
      replace(this.el, h("h2", {}, "Could not prepare the print"), h("p", { class: "error" }, (e as Error).message));
    }
  }

  private async send(job: Job, btn: HTMLButtonElement, approveFirst: boolean) {
    btn.disabled = true;
    this.sendError = "";
    try {
      if (approveFirst) {
        btn.textContent = "Approving…";
        this.job = { ...job, ...(await api.approve(job.id)) };
      }
      btn.textContent = "Sending to the printer…";
      const r = await api.printJob(job.id);
      this.job = { ...this.job!, ...r.job, printed: true };
      toast(`Printing on ${r.printer}.`, "ok");
    } catch (e) {
      this.sendError = (e as Error).message;
    }
    this.render();
  }

  private render() {
    const job = this.job;
    if (!job) return this.hide();
    this.el.hidden = false;
    const button = (label: string, testid: string, cls: string, fn: (b: HTMLButtonElement) => void) => {
      const b = h("button", { class: cls, type: "button", "data-testid": testid }, label) as HTMLButtonElement;
      b.addEventListener("click", () => fn(b));
      return b;
    };
    const approveOnly = button("Approve only", "approve", "link", async (b) => {
      b.disabled = true;
      try {
        this.job = await api.approve(job.id);
        toast("Approved. Send it from here, or tell the agent to print it.", "ok");
      } catch (e) {
        toast((e as Error).message, "error");
      }
      this.render();
    });
    let action: HTMLElement;
    if (job.printed) {
      action = h("div", {}, h("p", { class: "ok" }, "Sent to the printer."),
        button("Print again", "print-again", "", () => void this.request(job.project, job.version, job.printer_id, this.printerName)));
    } else if (job.approved) {
      action = h("div", { "data-testid": "approved" },
        this.sendError ? h("p", { class: "error", role: "alert", "data-testid": "send-error" }, this.sendError) : null,
        button(this.sendError ? "Try again" : "Send to printer", "send", "primary big", (b) => void this.send(job, b, false)),
        h("p", { class: "muted small" }, "Approved. Send it now, or let the agent call print_job."));
    } else {
      action = h("div", {},
        button("Approve and print", "approve-print", "primary big", (b) => void this.send(job, b, true)),
        h("p", { class: "muted small" }, "Nothing prints until you approve this exact file. ", approveOnly));
    }

    replace(this.el,
      h("h2", {}, job.printed ? "Printed" : job.approved ? "Approved" : "Waiting for your approval"),
      h("p", { class: "job-meta" }, `${job.project} · version ${job.version} · ${this.printerName || job.printer_id} · job `, h("code", {}, job.id),
        this.requestedBy ? h("span", { class: "small muted", "data-testid": "requested-by" }, ` · requested by ${this.requestedBy}`) : null),
      job.estimate ? estimateFacts(job.estimate) : null,
      h("button", { class: "link", type: "button", onclick: () => this.onShowToolpaths(job) }, "Show toolpaths"),
      action);
  }
}
