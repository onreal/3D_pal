import { api, type PrinterInfo, type PrinterStatus } from "../api";
import { h, replace, toast } from "../dom";

const LABELS: Record<PrinterStatus["state"], string> = {
  idle: "Idle", printing: "Printing", paused: "Paused", finished: "Finished", failed: "Failed", offline: "Offline",
  unknown: "Status unavailable",
};

/** Live state and progress of the selected printer during a print (PRD FR-14, V-14). */
export class PrinterStatusBar {
  readonly el = h("div", { class: "printer-status", role: "status", "aria-live": "polite" });
  private printer: PrinterInfo | null = null;
  private status: PrinterStatus | null = null;
  private error = "";
  /** Opens the live toolpath view of the running print. */
  onWatch: () => void = () => {};

  set(printer: PrinterInfo | null) {
    this.printer = printer;
    this.status = printer?.status ?? null;
    this.error = printer?.error ?? "";
    this.render();
  }

  receive(printerId: string, status: PrinterStatus) {
    if (!this.printer || printerId !== this.printer.id) return;
    this.status = status;
    this.error = status.state === "offline" || status.state === "unknown" ? status.message ?? "" : "";
    this.render();
  }

  private render() {
    if (!this.printer) {
      replace(this.el, h("a", { href: "/settings", class: "small" }, "Add a printer"));
      return;
    }
    const st = this.status;
    const state = st?.state ?? (this.error ? "offline" : "idle");
    const active = state === "printing" || state === "paused";
    const pct = Math.round((st?.progress ?? 0) * 100);
    const cancel = h("button", { class: "link danger small", type: "button" }, "Stop print");
    cancel.addEventListener("click", async () => {
      if (!confirm(`Stop the print on ${this.printer!.name}? This cannot be undone.`)) return;
      try {
        await api.cancel(this.printer!.id);
        toast("Print stopped.", "ok");
      } catch (e) {
        toast((e as Error).message, "error");
      }
    });
    replace(this.el,
      h("span", { class: `dot ${state}`, "aria-hidden": "true" }),
      h("span", { class: "small", title: (state === "unknown" ? st?.message : this.error) || undefined, "data-testid": "printer-state" }, `${LABELS[state]}`),
      active ? h("progress", { max: 100, value: pct, "aria-label": "Print progress" }) : null,
      active ? h("span", { class: "small" }, `${pct}%${st?.total_layers ? ` · layer ${st.layer}/${st.total_layers}` : ""}`) : null,
      active ? h("button", { class: "link small", type: "button", "data-testid": "watch-print", onclick: () => this.onWatch() }, "Watch") : null,
      active ? cancel : null);
  }
}
