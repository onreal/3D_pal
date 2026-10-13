import { ApiError, api, type Param } from "../api";
import { h, replace } from "../dom";

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

/**
 * Sliders and fields generated from the model's top-level parameters (PRD V-9). Editing a value
 * renders nothing: the edits wait, marked as unrendered, until Render 3D (or Enter in a field)
 * compiles them as one new version. A model can take a minute to compile, so rendering on every
 * keystroke or slider step only queued renders nobody wanted.
 */
export class ParamsPanel {
  readonly el = h("section", { class: "panel params-panel", "aria-label": "Parameters" });
  private project = "";
  private readers = new Map<string, () => unknown>();
  /** The values of the version shown: what "changed" is measured against. */
  private base = new Map<string, string>();
  private status = h("p", { class: "muted small", "data-testid": "params-status" });
  private renderBtn = h("button", { type: "button", class: "primary", "data-testid": "render-3d", title: "Compile these values as a new version", onclick: () => void this.submit() }, "Render 3D") as HTMLButtonElement;
  private resetBtn = h("button", { type: "button", title: "Put back the values of the version shown", onclick: () => this.reset() }, "Reset") as HTMLButtonElement;
  private params: Param[] = [];
  private busy = false;
  private started = 0;
  private message: { text: string; error: boolean } | null = null;

  constructor() {
    this.el.addEventListener("keydown", (e) => {
      if (e.key === "Enter" && (e.target as HTMLElement).tagName === "INPUT") {
        e.preventDefault();
        void this.submit();
      }
    });
  }

  /**
   * Shows a version's parameters. Unrendered edits on the same project survive, so a render by
   * the agent while you are typing does not throw your values away.
   */
  async load(project: string, version = 0) {
    const edits = project === this.project ? this.edits() : {};
    this.project = project;
    let params: Param[];
    try {
      params = await api.params(project, version);
    } catch (e) {
      replace(this.el, h("h2", {}, "Parameters"), h("p", { class: "muted" }, (e as Error).message));
      return;
    }
    this.render(params, edits);
  }

  render(params: Param[], edits: Record<string, unknown> = {}) {
    this.params = params;
    this.readers.clear();
    this.base.clear();
    const changed = () => {
      this.message = null; // a new edit makes the last result old news
      this.refresh();
    };
    const groups = new Map<string, HTMLElement[]>();
    for (const p of params) {
      this.base.set(p.name, JSON.stringify(p.value));
      const c = control(p.name in edits ? { ...p, value: edits[p.name] } : p, changed);
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
      h("div", { class: "params-actions" }, this.resetBtn, this.renderBtn),
      this.status);
    this.refresh();
  }

  values(): Record<string, unknown> {
    const out: Record<string, unknown> = {};
    for (const [k, read] of this.readers) out[k] = read();
    return out;
  }

  /** Values that differ from the version shown, not rendered yet. */
  edits(): Record<string, unknown> {
    const out: Record<string, unknown> = {};
    for (const [k, v] of Object.entries(this.values())) if (JSON.stringify(v) !== this.base.get(k)) out[k] = v;
    return out;
  }

  private reset() {
    this.message = null;
    this.render(this.params);
  }

  /** Redraws the button and the status line from the edits and the render in progress. */
  private refresh() {
    const changed = Object.keys(this.edits());
    this.el.classList.toggle("dirty", changed.length > 0);
    this.renderBtn.disabled = this.busy || changed.length === 0;
    this.resetBtn.disabled = this.busy || changed.length === 0;
    if (this.busy) {
      const secs = Math.round((Date.now() - this.started) / 1000);
      replace(this.renderBtn, h("span", { class: "spinner", "aria-hidden": "true" }), `Rendering… ${secs} s`);
    } else {
      replace(this.renderBtn, "Render 3D");
    }
    this.status.className = this.message?.error ? "error small" : changed.length ? "unrendered small" : "muted small";
    this.status.textContent = this.message?.text
      ?? (changed.length ? `Not rendered yet: ${changed.join(", ")}. Press Render 3D to see it.` : "");
  }

  private async submit() {
    if (this.busy || !Object.keys(this.edits()).length) return;
    this.busy = true;
    this.started = Date.now();
    this.message = null;
    this.refresh();
    const timer = setInterval(() => this.refresh(), 1000);
    try {
      const values = this.values();
      const res = await api.setParams(this.project, values);
      const secs = Math.round((Date.now() - this.started) / 1000);
      // The rendered values are the new baseline; the live model event reloads the panel too.
      for (const [k, v] of Object.entries(values)) this.base.set(k, JSON.stringify(v));
      this.message = { text: `Version ${res.version} rendered in ${secs} s.`, error: false };
    } catch (e) {
      this.message = { text: e instanceof ApiError ? e.message : String(e), error: true };
    } finally {
      clearInterval(timer);
      this.busy = false;
      this.refresh();
    }
  }
}
