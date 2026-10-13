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
  private preparing = false; // the Print button's slice is running
  private busy = new Map<string, string>(); // printers that are printing, paused, or can't tell → state
  private names = new Map<string, string>(); // printer ID → name
  onShowToolpaths: (job: Job) => void = () => {};

  /** Shows the version's job waiting for approval or sending, if any: a job for another
   *  version (another part, other parameters) stays out of the way. */
  async load(project: string, version: number) {
    try {
      const pending = await api.pendingJobs(project);
      if (this.preparing) return;
      const job = pending.find((j) => j.version === version);
      if (job) this.show({ job, estimate: job.estimate!, printer_name: job.printer_id, viewer_url: "" });
      // Keep a job just sent from this version on screen; anything else goes.
      else if (!(this.job?.printed && this.job.project === project && this.job.version === version)) this.hide();
    } catch {
      this.hide();
    }
  }

  /** A printer's state; a busy printer, or one whose status is unavailable, gets no send button. */
  setPrinterState(printerId: string, state: string, name?: string) {
    if (name) this.names.set(printerId, name);
    const was = this.busy.get(printerId);
    if (state === "printing" || state === "paused" || state === "unknown") this.busy.set(printerId, state);
    else this.busy.delete(printerId);
    if (was !== this.busy.get(printerId) && this.job?.printer_id === printerId) this.render();
  }

  hide() {
    this.job = null;
    this.el.hidden = true;
  }

  /** Called for job events: a new request, or an approval made elsewhere. */
  show(a: Partial<Approval> & { job: Job; client?: string }) {
    if (a.job.discarded) {
      if (this.job?.id === a.job.id) this.hide();
      return;
    }
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
    this.preparing = true;
    this.el.hidden = false;
    replace(this.el, h("h2", {}, "Preparing the print"),
      h("p", { class: "muted", role: "status" }, `Slicing ${project} version ${version}${printerName ? ` for ${printerName}` : ""}…`));
    try {
      const a = await api.requestJob(project, version, printerId);
      this.preparing = false;
      this.show({ ...a, client: "you" });
    } catch (e) {
      this.preparing = false;
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
    const discard = button("Discard", "discard", "link", async (b) => {
      b.disabled = true;
      try {
        await api.discard(job.id);
        this.hide();
        toast("Job discarded.", "ok");
      } catch (e) {
        b.disabled = false;
        toast((e as Error).message, "error");
      }
    });
    let action: HTMLElement;
    if (job.printed) {
      action = h("div", {}, h("p", { class: "ok" }, "Sent to the printer."),
        button("Print again", "print-again", "", () => void this.request(job.project, job.version, job.printer_id, this.printerName)));
    } else if (this.busy.has(job.printer_id)) {
      // Sending would fail the pre-flight check: say so instead of offering the button.
      const name = this.names.get(job.printer_id) ?? (this.printerName || job.printer_id);
      action = h("div", { "data-testid": "printer-busy" },
        h("p", { class: "muted" }, this.busy.get(job.printer_id) === "unknown"
          ? `${name} isn't reporting its status, so it may be printing. Send this job once its status is back.`
          : `${name} is printing. Send this job when the print is done.`),
        h("p", { class: "muted small" }, job.approved ? null : approveOnly, job.approved ? null : " · ", discard));
    } else if (job.approved) {
      action = h("div", { "data-testid": "approved" },
        this.sendError ? h("p", { class: "error", role: "alert", "data-testid": "send-error" }, this.sendError) : null,
        button(this.sendError ? "Try again" : "Send to printer", "send", "primary big", (b) => void this.send(job, b, false)),
        h("p", { class: "muted small" }, "Approved. Send it now, or let the agent call print_job. ", discard));
    } else {
      action = h("div", {},
        button("Approve and print", "approve-print", "primary big", (b) => void this.send(job, b, true)),
        h("p", { class: "muted small" }, "Nothing prints until you approve this exact file. ", approveOnly, " · ", discard));
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
