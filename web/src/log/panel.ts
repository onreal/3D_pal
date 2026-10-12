import { api, type Run } from "../api";
import { h, replace } from "../dom";

/** The project's run log: every tool call with its time and result (PRD observability). */
export class LogPanel {
  readonly el = h("section", { class: "panel log-panel", "aria-label": "Run log" });

  async load(project: string) {
    let runs: Run[];
    try {
      runs = await api.log(project);
    } catch (e) {
      replace(this.el, h("h2", {}, "Run log"), h("p", { class: "error" }, (e as Error).message));
      return;
    }
    replace(this.el,
      h("h2", {}, "Run log"),
      runs.length
        ? h("table", { class: "log" },
            h("thead", {}, h("tr", {}, h("th", {}, "Time"), h("th", {}, "Tool"), h("th", {}, "By"), h("th", {}, "Exit"), h("th", {}, "Result"))),
            h("tbody", {}, ...runs.map((r) =>
              h("tr", { class: r.exit_code ? "failed" : "" },
                h("td", { class: "small nowrap" }, new Date(r.started_at).toLocaleTimeString()),
                h("td", {}, h("code", {}, r.tool)),
                h("td", { class: "small" }, r.client ?? ""),
                h("td", {}, r.exit_code === null ? "…" : String(r.exit_code)),
                h("td", { class: "small msg" }, r.message)))))
        : h("p", { class: "muted" }, "No tool calls yet."));
  }
}
