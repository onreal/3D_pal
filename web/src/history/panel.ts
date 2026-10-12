import { api, type Estimate, type VersionInfo } from "../api";
import { h, replace, toast } from "../dom";
import { compareEstimates, formatCost, formatMinutes, formatSize, timeAgo, type Better } from "../format";

/** Parameter names whose value differs from the previous version. */
export function changedParams(v: VersionInfo, prev: VersionInfo | undefined): string[] {
  if (!prev) return [];
  const keys = new Set([...Object.keys(v.params ?? {}), ...Object.keys(prev.params ?? {})]);
  return [...keys].filter((k) => JSON.stringify(v.params?.[k]) !== JSON.stringify(prev.params?.[k])).sort();
}

export function estimateFor(v: VersionInfo, printerId: string): Estimate | undefined {
  return v.estimates.find((e) => e.status === "done" && (!printerId || e.printer_id === printerId));
}

/** Every version with its estimate; click to view, pick two to compare, or roll back (PRD V-12, FR-37). */
export class HistoryPanel {
  readonly el = h("section", { class: "panel history-panel", "aria-label": "History" });
  private project = "";
  private versions: VersionInfo[] = [];
  private printerId = "";
  private shown = 0;
  private picked: number[] = [];
  onView: (version: number) => void = () => {};

  async load(project: string, printerId: string, shown: number) {
    this.project = project;
    this.printerId = printerId;
    this.shown = shown;
    try {
      this.versions = await api.versions(project);
    } catch (e) {
      replace(this.el, h("h2", {}, "History"), h("p", { class: "error" }, (e as Error).message));
      return;
    }
    this.picked = this.picked.filter((n) => this.versions.some((v) => v.version === n));
    this.render();
  }

  setShown(version: number) {
    this.shown = version;
    this.render();
  }

  private render() {
    const rows = [...this.versions].reverse().map((v) => {
      const i = this.versions.indexOf(v);
      const changed = changedParams(v, this.versions[i - 1]);
      const e = estimateFor(v, this.printerId);
      const pick = h("input", { type: "checkbox", "aria-label": `Compare version ${v.version}`, checked: this.picked.includes(v.version) });
      pick.addEventListener("change", () => {
        this.picked = pick.checked ? [...this.picked, v.version].slice(-2) : this.picked.filter((n) => n !== v.version);
        this.render();
      });
      return h("li", { class: `version${v.version === this.shown ? " current" : ""}`, "data-version": v.version },
        pick,
        h("button", { class: "version-main", type: "button", onclick: () => this.onView(v.version), title: "View this version" },
          h("strong", {}, `v${v.version}`), " ",
          h("span", { class: "muted small" }, timeAgo(v.created_at)),
          v.size_mm ? h("span", { class: "small" }, formatSize(v.size_mm)) : null,
          changed.length ? h("span", { class: "small changed" }, changed.map((k) => `${k} = ${JSON.stringify(v.params[k])}`).join(", ")) : null,
          e ? h("span", { class: "small est" }, `${formatMinutes(e.minutes)} · ${e.grams.toFixed(1)} g${e.cost ? ` · ${formatCost(e)}` : ""}`) : null),
        v.version !== this.versions.at(-1)?.version
          ? h("button", { class: "link small", type: "button", onclick: () => void this.rollback(v.version) }, "Roll back")
          : null);
    });
    replace(this.el,
      h("h2", {}, "History"),
      this.versions.length ? h("ol", { class: "versions", reversed: true }, ...rows) : h("p", { class: "muted" }, "No versions yet."),
      this.comparison());
  }

  private comparison(): HTMLElement | null {
    if (this.picked.length !== 2) return this.versions.length > 1 ? h("p", { class: "muted small" }, "Tick two versions to compare them side by side.") : null;
    const [a, b] = this.picked.map((n) => this.versions.find((v) => v.version === n)!) as [VersionInfo, VersionInfo];
    const ea = estimateFor(a, this.printerId);
    const eb = estimateFor(b, this.printerId);
    const params = [...new Set([...Object.keys(a.params ?? {}), ...Object.keys(b.params ?? {})])].sort();
    const cell = (text: string, mark: Better | "diff" | "") => h("td", { class: mark === "lower" ? "better" : mark === "higher" ? "worse" : mark === "diff" ? "diff" : "" }, text);
    const cmpA = ea && eb ? compareEstimates(ea, eb) : null;
    const cmpB = ea && eb ? compareEstimates(eb, ea) : null;
    const row = (label: string, va: string, vb: string, ma: Better | "diff" | "" = "", mb: Better | "diff" | "" = "") =>
      h("tr", {}, h("th", { scope: "row" }, label), cell(va, ma), cell(vb, mb));
    return h("div", { class: "compare" },
      h("table", {},
        h("thead", {}, h("tr", {}, h("th", {}), h("th", {}, `v${a.version}`), h("th", {}, `v${b.version}`))),
        h("tbody", {},
          ...params.map((k) => {
            const va = JSON.stringify(a.params?.[k]);
            const vb = JSON.stringify(b.params?.[k]);
            const d = va === vb ? "" : "diff";
            return row(k, va ?? "–", vb ?? "–", d, d);
          }),
          ...(ea && eb && cmpA && cmpB
            ? [
                row("Time", formatMinutes(ea.minutes), formatMinutes(eb.minutes), cmpA.minutes, cmpB.minutes),
                row("Filament", `${ea.grams.toFixed(1)} g`, `${eb.grams.toFixed(1)} g`, cmpA.grams, cmpB.grams),
                row("Layers", String(ea.layers), String(eb.layers), cmpA.layers, cmpB.layers),
                ea.cost || eb.cost ? row("Cost", formatCost(ea), formatCost(eb), cmpA.cost, cmpB.cost) : null,
              ].filter((x): x is HTMLTableRowElement => x !== null)
            : [h("tr", {}, h("td", { colspan: 3, class: "muted small" }, "Estimate both versions to compare time, filament and cost."))]),
        )));
  }

  private async rollback(version: number) {
    try {
      const res = await api.restore(this.project, version);
      toast(`Version ${version} restored as version ${res.version}.`, "ok");
    } catch (e) {
      toast((e as Error).message, "error");
    }
  }
}
