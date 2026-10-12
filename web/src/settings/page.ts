import { ApiError, MASK, api, type Field, type Kinds, type PrinterSettings, type Profile, type Settings } from "../api";
import { download, h, replace, toast } from "../dom";

/** A printer ID from its name: lowercase letters, digits and dashes. */
export function slug(name: string, taken: string[]): string {
  const base = name.toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "").slice(0, 24) || "printer";
  let id = base;
  for (let i = 2; taken.includes(id); i++) id = `${base}-${i}`;
  return id;
}

const PROFILE_LABELS: Record<string, string> = { machine: "Machine", process: "Process", filament: "Filament" };

/** Every setting on one page (PRD FR-15 to FR-20, FR-26). */
export class SettingsPage {
  readonly el = h("main", { class: "settings" });
  private s!: Settings;
  private kinds!: Kinds;
  private saved = "";
  private errors = h("div", { class: "form-errors", role: "alert" });

  async load() {
    replace(this.el, h("p", { class: "muted" }, "Loading settings…"));
    try {
      [this.s, this.kinds] = await Promise.all([api.settings(), api.kinds()]);
    } catch (e) {
      replace(this.el, h("p", { class: "error" }, (e as Error).message));
      return;
    }
    this.saved = JSON.stringify(this.s);
    this.render();
  }

  private dirty() {
    return JSON.stringify(this.s) !== this.saved;
  }

  private render() {
    replace(this.el,
      h("header", { class: "settings-head" },
        h("div", {}, h("h1", {}, "Settings"), h("p", { class: "muted" }, "Saved in the database on the data volume. Changes apply to the next tool call; no restart needed.")),
        h("a", { href: "/", class: "button" }, "Back to viewer")),
      this.errors,
      this.printersSection(),
      this.slicerSection(),
      this.viewerSection(),
      this.pipelineSection(),
      this.transferSection(),
      h("div", { class: "save-bar" },
        h("button", { class: "primary", type: "button", "data-testid": "save", onclick: () => void this.save() }, "Save settings"),
        h("span", { class: "muted small" }, this.dirty() ? "Unsaved changes" : "")));
  }

  private rerender() {
    const y = window.scrollY;
    this.render();
    window.scrollTo(0, y);
  }

  // ---- printers -----------------------------------------------------------

  private printersSection() {
    const kindSelect = h("select", { "aria-label": "Printer kind" }, ...this.kinds.printers.map((k) => h("option", { value: k.kind }, k.label)));
    const add = h("button", { type: "button" }, "Add printer");
    add.addEventListener("click", () => {
      const kind = this.kinds.printers.find((k) => k.kind === kindSelect.value)!;
      const name = kind.kind === "fake" ? "Simulated printer" : kind.label.replace(/ \(.*\)$/, "");
      const config: Record<string, string> = {};
      for (const f of kind.fields) if (f.default) config[f.key] = f.default;
      this.s.printers.push({ id: slug(name, this.s.printers.map((p) => p.id)), name, kind: kind.kind, default: this.s.printers.length === 0, config, profiles: {} });
      this.rerender();
    });
    return h("section", { class: "card" },
      h("h2", {}, "Printers"),
      h("p", { class: "muted small" }, "The default printer is used when the agent doesn't name one. The LAN access code is stored on this computer only and never shown again or sent to the agent."),
      ...this.s.printers.map((p, i) => this.printerCard(p, i)),
      h("div", { class: "row" }, kindSelect, add));
  }

  private printerCard(p: PrinterSettings, i: number) {
    const kind = this.kinds.printers.find((k) => k.kind === p.kind);
    const name = h("input", { type: "text", value: p.name, "aria-label": "Printer name", oninput: (e: Event) => { p.name = (e.target as HTMLInputElement).value; } });
    const def = h("input", { type: "radio", name: "default-printer", checked: p.default, "aria-label": `Make ${p.name} the default` });
    def.addEventListener("change", () => {
      this.s.printers.forEach((x) => (x.default = x === p));
    });
    const remove = h("button", { type: "button", class: "link danger" }, "Remove");
    remove.addEventListener("click", () => {
      if (!confirm(`Remove ${p.name}?`)) return;
      this.s.printers.splice(i, 1);
      if (p.default && this.s.printers[0]) this.s.printers[0].default = true;
      this.rerender();
    });
    const results = h("ul", { class: "checks" });
    const test = h("button", { type: "button" }, "Test connection");
    test.addEventListener("click", async () => {
      if (this.dirty()) {
        toast("Save your changes first, then test the connection.", "info");
        return;
      }
      test.disabled = true;
      replace(results, h("li", { class: "muted" }, "Testing…"));
      try {
        const r = await api.testPrinter(p.id);
        replace(results, ...r.checks.map((c) => h("li", { class: c.ok ? "ok" : "error" }, h("strong", {}, c.ok ? "✓ " : "✗ ", c.name), ` — ${c.message}`)));
      } catch (e) {
        replace(results, h("li", { class: "error" }, (e as Error).message));
      } finally {
        test.disabled = false;
      }
    });

    return h("fieldset", { class: "printer" },
      h("legend", {}, kind?.label ?? p.kind),
      h("div", { class: "grid2" },
        h("label", {}, "Name", name),
        h("label", {}, "ID", h("input", { type: "text", value: p.id, "aria-label": "Printer ID", pattern: "[a-z0-9][a-z0-9_-]{0,31}", oninput: (e: Event) => { p.id = (e.target as HTMLInputElement).value; } })),
        ...(kind?.fields ?? []).map((f) => this.fieldInput(f, p))),
      h("div", { class: "grid3" }, ...["machine", "process", "filament"].map((k) => this.profilePicker(p, k))),
      h("div", { class: "row" }, h("label", { class: "inline" }, def, " Default printer"), test, remove),
      results);
  }

  /** One driver-described field; the form needs no code per printer kind (PRD FR-26). */
  private fieldInput(f: Field, p: PrinterSettings) {
    const value = p.config[f.key] ?? f.default ?? "";
    const set = (v: string) => { p.config[f.key] = v; };
    let input: HTMLInputElement | HTMLSelectElement;
    if (f.type === "select") {
      input = h("select", { onchange: (e: Event) => set((e.target as HTMLSelectElement).value) }, ...(f.options ?? []).map((o) => h("option", { value: o, selected: o === value }, o)));
    } else {
      input = h("input", {
        type: f.type === "secret" ? "password" : f.type === "number" ? "number" : "text",
        value,
        required: f.required,
        autocomplete: f.type === "secret" ? "new-password" : "off",
        inputmode: f.type === "ip" ? "decimal" : null,
        placeholder: f.type === "ip" ? "192.168.1.50" : null,
        oninput: (e: Event) => set((e.target as HTMLInputElement).value),
      });
      if (f.type === "secret" && value === MASK) {
        const secret = input as HTMLInputElement;
        secret.addEventListener("focus", () => { if (secret.value === MASK) secret.select(); });
      }
    }
    return h("label", {}, h("span", {}, f.label, f.required ? h("span", { class: "req", "aria-hidden": "true" }, " *") : null), input, f.help ? h("small", { class: "muted" }, f.help) : null);
  }

  private profilePicker(p: PrinterSettings, kind: string) {
    const options = this.s.profiles.filter((x) => x.kind === kind);
    const sel = h("select", { onchange: (e: Event) => { p.profiles[kind] = (e.target as HTMLSelectElement).value; } },
      h("option", { value: "" }, "— none —"),
      ...options.map((o) => h("option", { value: o.name, selected: p.profiles?.[kind] === o.name }, o.name)));
    return h("label", {}, `${PROFILE_LABELS[kind]} profile`, sel);
  }

  // ---- slicer -------------------------------------------------------------

  private slicerSection() {
    const s = this.s.slicer;
    const uploads = this.kinds.profile_kinds.map((kind) => {
      const file = h("input", { type: "file", accept: ".json,application/json", "aria-label": `Upload ${kind} profile` });
      file.addEventListener("change", async () => {
        const f = file.files?.[0];
        if (!f) return;
        try {
          if (this.dirty()) await this.save(true);
          await api.uploadProfile(kind, f);
          toast(`Uploaded ${f.name}.`, "ok");
          await this.load();
        } catch (e) {
          toast((e as Error).message, "error");
        }
      });
      const inUse = this.s.profiles.filter((x) => x.kind === kind);
      return h("div", { class: "upload" },
        h("h3", {}, `${PROFILE_LABELS[kind]} profiles`),
        inUse.length ? h("ul", { class: "profiles" }, ...inUse.map((x) => this.profileRow(x))) : h("p", { class: "muted small" }, "None uploaded."),
        h("label", { class: "button small-button" }, `Upload ${kind} JSON…`, file));
    });
    return h("section", { class: "card" },
      h("h2", {}, "Slicer"),
      h("p", { class: "muted small" }, "Export the A1 machine, process and filament presets from Bambu Studio as JSON and upload them here. Without them the slicer falls back to defaults that are not A1 settings."),
      h("div", { class: "grid3" }, ...uploads),
      h("h3", {}, "Defaults for every job"),
      h("div", { class: "grid2" },
        h("label", { class: "inline" }, h("input", { type: "checkbox", checked: s.supports, onchange: (e: Event) => { s.supports = (e.target as HTMLInputElement).checked; } }), " Generate supports"),
        h("label", {}, "Support type", h("select", { onchange: (e: Event) => { s.support_type = (e.target as HTMLSelectElement).value; } },
          ...this.kinds.support_types.map((t) => h("option", { value: t, selected: t === s.support_type }, t)))),
        h("label", {}, "Layer height (mm, 0 = the process profile's)", h("input", { type: "number", min: "0", max: "0.6", step: "0.02", value: String(s.layer_height), oninput: (e: Event) => { s.layer_height = Number((e.target as HTMLInputElement).value); } })),
        h("label", {}, "Infill (%, −1 = the process profile's)", h("input", { type: "number", min: "-1", max: "100", step: "1", value: String(s.infill), oninput: (e: Event) => { s.infill = Number((e.target as HTMLInputElement).value); } })),
        h("label", {}, "Currency for costs", h("input", { type: "text", maxlength: "3", value: this.s.currency, oninput: (e: Event) => { this.s.currency = (e.target as HTMLInputElement).value.toUpperCase(); } }))));
  }

  private profileRow(x: Profile) {
    const remove = h("button", { type: "button", class: "link danger small" }, "Remove");
    remove.addEventListener("click", () => {
      this.s.profiles = this.s.profiles.filter((p) => p !== x);
      for (const p of this.s.printers) for (const k of Object.keys(p.profiles ?? {})) if (p.profiles[k] === x.name) p.profiles[k] = "";
      this.rerender();
    });
    return h("li", {},
      h("span", { class: "small" }, x.name),
      x.kind === "filament"
        ? h("label", { class: "inline small" }, "Price per kg ",
            h("input", { type: "number", min: "0", step: "0.5", value: String(x.price_per_kg ?? 0), class: "narrow", oninput: (e: Event) => { x.price_per_kg = Number((e.target as HTMLInputElement).value); } }))
        : null,
      remove);
  }

  // ---- viewer and pipeline ------------------------------------------------

  private viewerSection() {
    const v = this.s.viewer;
    return h("section", { class: "card" },
      h("h2", {}, "Viewer"),
      h("div", { class: "grid2" },
        h("label", {}, "Overhang angle (degrees from vertical)", h("input", { type: "number", min: "1", max: "89", value: String(v.overhang_angle), oninput: (e: Event) => { v.overhang_angle = Number((e.target as HTMLInputElement).value); } })),
        h("label", {}, "Default display mode", h("select", { onchange: (e: Event) => { v.display_mode = (e.target as HTMLSelectElement).value; } },
          ...this.kinds.display_modes.map((m) => h("option", { value: m, selected: m === v.display_mode }, m))))));
  }

  private pipelineSection() {
    const p = this.s.pipeline;
    return h("section", { class: "card" },
      h("h2", {}, "Pipeline"),
      h("label", { class: "inline locked" },
        h("input", { type: "checkbox", checked: true, disabled: true, "aria-describedby": "approval-help" }),
        " Approval required before printing ", h("span", { class: "badge" }, "Locked on")),
      h("p", { id: "approval-help", class: "muted small" }, "A print is physical and runs unattended, so every job waits for your Approve click. This cannot be turned off."),
      h("fieldset", { class: "plain" }, h("legend", {}, "Preview angles sent to the agent"),
        ...this.kinds.preview_angles.map((a) => h("label", { class: "inline" },
          h("input", { type: "checkbox", checked: p.preview_angles.includes(a), onchange: (e: Event) => {
            const on = (e.target as HTMLInputElement).checked;
            p.preview_angles = on ? [...new Set([...p.preview_angles, a])] : p.preview_angles.filter((x) => x !== a);
          } }), ` ${a} `))),
      h("div", { class: "grid2" },
        h("label", {}, "Workspace path (inside the container)", h("input", { type: "text", value: p.workspace, oninput: (e: Event) => { p.workspace = (e.target as HTMLInputElement).value; } })),
        h("label", {}, "Log level", h("select", { onchange: (e: Event) => { p.log_level = (e.target as HTMLSelectElement).value; } },
          ...this.kinds.log_levels.map((l) => h("option", { value: l, selected: l === p.log_level }, l))))));
  }

  // ---- import and export --------------------------------------------------

  private transferSection() {
    const exp = h("button", { type: "button" }, "Export settings");
    exp.addEventListener("click", async () => {
      if (!confirm("The file includes your printer access codes. Keep it private. Export?")) return;
      try {
        const bundle = await api.exportSettings();
        download(new Blob([JSON.stringify(bundle, null, 2)], { type: "application/json" }), `printpipe-settings-${new Date().toISOString().slice(0, 10)}.json`);
      } catch (e) {
        toast((e as Error).message, "error");
      }
    });
    const file = h("input", { type: "file", accept: ".json,application/json", "aria-label": "Import settings file" });
    file.addEventListener("change", async () => {
      const f = file.files?.[0];
      if (!f || !confirm("Replace all settings and profiles with this file?")) return;
      try {
        await api.importSettings(JSON.parse(await f.text()));
        toast("Settings imported.", "ok");
        await this.load();
      } catch (e) {
        this.showError(e);
      }
    });
    return h("section", { class: "card" },
      h("h2", {}, "Move to another computer"),
      h("p", { class: "muted small" }, "One file holds every setting and profile."),
      h("div", { class: "row" }, exp, h("label", { class: "button" }, "Import settings…", file)));
  }

  private showError(e: unknown) {
    if (e instanceof ApiError && e.problems.length) {
      replace(this.errors, h("p", {}, h("strong", {}, "Not saved. Fix these:")), h("ul", {}, ...e.problems.map((p) => h("li", {}, p))));
    } else {
      replace(this.errors, h("p", {}, (e as Error).message));
    }
    this.errors.scrollIntoView({ behavior: "smooth", block: "center" });
  }

  async save(quiet = false) {
    replace(this.errors);
    try {
      this.s = await api.saveSettings(this.s);
      this.saved = JSON.stringify(this.s);
      if (!quiet) toast("Settings saved.", "ok");
      this.rerender();
    } catch (e) {
      this.showError(e);
      throw e;
    }
  }
}
