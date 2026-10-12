import { ApiError, api, type Param } from "../api";
import { debounce, h, replace } from "../dom";

/** Builds one control for a parameter; read() returns its current value in the parameter's type. */
export function control(p: Param, onInput: () => void): { el: HTMLElement; read: () => unknown } {
  const id = `param-${p.name.replace(/[^A-Za-z0-9_-]/g, "_")}`;
  const label = h("label", { for: id, class: "param-label" }, p.label || p.name, p.label ? h("code", {}, p.name) : null);

  if (p.options?.length) {
    const select = h("select", { id, onchange: onInput },
      ...p.options.map((o) => h("option", { value: String(o.value), selected: String(o.value) === String(p.value) }, o.label)));
    return {
      el: h("div", { class: "param" }, label, select),
      read: () => (p.type === "number" ? Number(select.value) : select.value),
    };
  }
  switch (p.type) {
    case "boolean": {
      const box = h("input", { id, type: "checkbox", checked: Boolean(p.value), onchange: onInput });
      return { el: h("div", { class: "param param-bool" }, box, label), read: () => box.checked };
    }
    case "string": {
      const text = h("input", { id, type: "text", value: String(p.value ?? ""), oninput: onInput });
      return { el: h("div", { class: "param" }, label, text), read: () => text.value };
    }
    case "vector": {
      const values = Array.isArray(p.value) ? (p.value as number[]) : [];
      const inputs = values.map((v, i) => h("input", { type: "number", value: String(v), step: "any", "aria-label": `${p.name}[${i}]`, oninput: onInput }));
      return {
        el: h("div", { class: "param" }, label, h("div", { class: "param-vector", id }, ...inputs)),
        read: () => inputs.map((i) => Number(i.value)),
      };
    }
    default: {
      const value = Number(p.value);
      const step = p.step ?? (Number.isInteger(value) ? 1 : 0.1);
      const num = h("input", { id, type: "number", value: String(value), step: String(step), min: p.min ?? null, max: p.max ?? null });
      if (p.min === undefined || p.max === undefined) {
        num.addEventListener("input", onInput);
        return { el: h("div", { class: "param" }, label, num), read: () => Number(num.value) };
      }
      const range = h("input", { type: "range", min: String(p.min), max: String(p.max), step: String(step), value: String(value), "aria-label": p.name });
      range.addEventListener("input", () => {
        num.value = range.value;
        onInput();
      });
      num.addEventListener("input", () => {
        range.value = num.value;
        onInput();
      });
      return { el: h("div", { class: "param" }, label, h("div", { class: "param-range" }, range, num)), read: () => Number(num.value) };
    }
  }
}

/** Sliders and fields generated from the model's top-level parameters; a change recompiles on the server (PRD V-9). */
export class ParamsPanel {
  readonly el = h("section", { class: "panel params-panel", "aria-label": "Parameters" });
  private project = "";
  private readers = new Map<string, () => unknown>();
  private status = h("p", { class: "muted small" });
  private busy = false;
  private pending = false;
  private started = 0;

  /** "Recompiling… 12 s", so a slow model never looks frozen. */
  private showProgress() {
    if (!this.busy) return;
    const secs = Math.round((Date.now() - this.started) / 1000);
    this.status.textContent = `Recompiling… ${secs} s${this.pending ? " (your latest change is next)" : ""}`;
  }

  async load(project: string, version = 0) {
    this.project = project;
    let params: Param[];
    try {
      params = await api.params(project, version);
    } catch (e) {
      replace(this.el, h("h2", {}, "Parameters"), h("p", { class: "muted" }, (e as Error).message));
      return;
    }
    this.render(params);
  }

  render(params: Param[]) {
    this.readers.clear();
    const send = debounce(() => this.submit(), 600);
    const groups = new Map<string, HTMLElement[]>();
    for (const p of params) {
      const c = control(p, send);
      this.readers.set(p.name, c.read);
      const g = p.group ?? "";
      groups.set(g, [...(groups.get(g) ?? []), c.el]);
    }
    if (!params.length) {
      replace(this.el, h("h2", {}, "Parameters"),
        h("p", { class: "muted" }, "This model has no top-level parameters. Ask the agent to name its dimensions, for example wall = 2; // [1:0.5:5]"));
      return;
    }
    replace(this.el, h("h2", {}, "Parameters"),
      ...[...groups].map(([name, els]) => (name ? h("fieldset", {}, h("legend", {}, name), ...els) : h("div", {}, ...els))),
      this.status);
  }

  values(): Record<string, unknown> {
    const out: Record<string, unknown> = {};
    for (const [k, read] of this.readers) out[k] = read();
    return out;
  }

  private async submit() {
    if (this.busy) {
      this.pending = true;
      this.showProgress();
      return;
    }
    this.busy = true;
    this.started = Date.now();
    this.status.className = "muted small";
    this.showProgress();
    const timer = setInterval(() => this.showProgress(), 1000);
    try {
      const res = await api.setParams(this.project, this.values());
      const secs = Math.round((Date.now() - this.started) / 1000);
      this.status.textContent = `Version ${res.version} compiled in ${secs} s.`;
    } catch (e) {
      this.status.className = "error small";
      this.status.textContent = e instanceof ApiError ? e.message : String(e);
    } finally {
      clearInterval(timer);
      this.busy = false;
      if (this.pending) {
        this.pending = false;
        void this.submit();
      }
    }
  }
}
