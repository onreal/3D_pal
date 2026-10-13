import { api, urls, type Job, type PrinterInfo, type PrinterStatus, type ProjectSummary, type Settings } from "./api";
import { debounce, download, h, replace, toast } from "./dom";
import { AssemblyPanel } from "./assembly/panel";
import { EstimatePanel } from "./estimates/panel";
import { Live } from "./events";
import { formatSize } from "./format";
import { HistoryPanel } from "./history/panel";
import { JobPanel } from "./job/panel";
import { LogPanel } from "./log/panel";
import { ParamsPanel } from "./params/panel";
import { clientLabel, decodeFollow, encodeFollow, follows, type Follow } from "./sessions/follow";
import { SessionsPanel } from "./sessions/panel";
import { PrinterStatusBar } from "./printer/status";
import { ToolpathPreview } from "./toolpaths/preview";
import type { View } from "./viewer/controls";
import { DISPLAY_MODES, type DisplayMode } from "./viewer/modes";
import type { Axis } from "./viewer/section";
import { Viewer, type ModelInfo } from "./viewer/viewer";

function loadFollow(): Follow {
  try {
    return decodeFollow(localStorage.getItem("printpipe.follow"));
  } catch {
    return { mode: "all" };
  }
}

/** The viewer page: the part on the bed, parameters, estimates, history and the approval. */
export class App {
  readonly el = h("div", { class: "app" });
  private viewer!: Viewer;
  private live = new Live();
  private printers: PrinterInfo[] = [];
  private projects: ProjectSummary[] = [];
  private settings: Settings | null = null;
  private project = "";
  private version = 0;
  private latest = 0;
  private printerId = "";
  private follow: Follow = loadFollow();
  private followSelect = h("select", { "aria-label": "Follow agent sessions", "data-testid": "follow" });
  private sessions = new SessionsPanel();

  private projectSelect = h("select", { "aria-label": "Project", class: "project-select" });
  private printerSelect = h("select", { "aria-label": "Printer" });
  private printBtn = h("button", { type: "button", class: "primary", "data-testid": "print", title: "Slice this version for the printer, then approve and send it" }, "Print…") as HTMLButtonElement;
  private versionLabel = h("span", { class: "version-label muted small" });
  private connection = h("span", { class: "conn", title: "Live updates" });
  private status = new PrinterStatusBar();
  private stage = h("div", { class: "stage" });
  private canvasHost = h("div", { class: "viewer", "data-testid": "viewer" });
  private info = h("div", { class: "info", "data-testid": "info" });
  private warning = h("div", { class: "warning", role: "alert", hidden: true, "data-testid": "over-volume" });
  private measureOut = h("output", { class: "measure-out small" });
  private sectionSlider = h("input", { type: "range", min: "0", max: "1", step: "0.01", value: "0.5", "aria-label": "Section position", disabled: true });
  private empty = h("div", { class: "empty", hidden: true });
  private tabs = {
    model: h("button", { role: "tab", type: "button", "aria-selected": "true" }, "Model"),
    assembly: h("button", { role: "tab", type: "button", "aria-selected": "false" }, "Assembly"),
    toolpaths: h("button", { role: "tab", type: "button", "aria-selected": "false" }, "Toolpaths"),
  };
  private assembly = new AssemblyPanel();
  private toolpaths = new ToolpathPreview();
  private job = new JobPanel();
  private estimate = new EstimatePanel();
  private params = new ParamsPanel();
  private history = new HistoryPanel();
  private log = new LogPanel();
  private toolpathSource: { url: string } | null = null;

  async start() {
    this.layout();
    this.viewer = new Viewer(this.canvasHost);
    this.viewer.onInfo = (i) => this.showInfo(i);
    this.viewer.onMeasure = (d) => (this.measureOut.textContent = d === null ? "" : `${d.toFixed(2)} mm`);
    this.wire();
    this.live.connect();
    await Promise.all([this.loadSettings(), this.loadPrinters(), this.sessions.load()]);
    this.sessions.setFollow(this.follow);
    this.renderFollow();
    await this.loadProjects();
    const first = this.projects[0];
    if (first) await this.openProject(first.name, 0);
    else this.showEmpty();
  }

  // ---- layout -------------------------------------------------------------

  private layout() {
    const viewBtn = (v: View, label: string) => h("button", { type: "button", onclick: () => this.viewer.setView(v), title: `${label} view` }, label);
    const mode = h("select", { "aria-label": "Display mode", "data-testid": "mode" }, ...DISPLAY_MODES.map((m) => h("option", { value: m.mode }, m.label)));
    mode.addEventListener("change", () => this.viewer.setMode(mode.value as DisplayMode));
    const overhang = h("input", { type: "checkbox", checked: true, "aria-label": "Show overhangs" });
    overhang.addEventListener("change", () => this.viewer.setOverhangs(overhang.checked, this.settings?.viewer.overhang_angle ?? 45));
    const axis = h("select", { "aria-label": "Section axis" }, h("option", { value: "" }, "No section"), h("option", { value: "x" }, "Cut X"), h("option", { value: "y" }, "Cut Y"), h("option", { value: "z" }, "Cut Z"));
    axis.addEventListener("change", () => {
      this.sectionSlider.disabled = !axis.value;
      this.viewer.setSection((axis.value || null) as Axis | null, Number(this.sectionSlider.value));
    });
    this.sectionSlider.addEventListener("input", () => this.viewer.setSection((axis.value || null) as Axis | null, Number(this.sectionSlider.value)));
    const measure = h("input", { type: "checkbox", "aria-label": "Measure" });
    measure.addEventListener("change", () => this.viewer.setMeasuring(measure.checked));
    const shot = h("button", { type: "button", title: "Save the view as PNG" }, "Screenshot");
    shot.addEventListener("click", async () => download(await this.viewer.screenshot(), `${this.project || "view"}_v${this.version}.png`));
    const stl = h("button", { type: "button", title: "Download the STL" }, "STL");
    stl.addEventListener("click", () => {
      if (!this.project) return;
      const a = h("a", { href: urls.stl(this.project, this.version), download: `${this.project}_v${this.version}.stl` });
      document.body.append(a);
      a.click();
      a.remove();
    });

    const toolbar = h("div", { class: "toolbar", role: "toolbar", "aria-label": "View tools" },
      h("div", { class: "group" }, viewBtn("iso", "Iso"), viewBtn("top", "Top"), viewBtn("front", "Front"), viewBtn("right", "Right"),
        h("button", { type: "button", onclick: () => this.viewer.fit(), title: "Fit the model" }, "Fit")),
      h("div", { class: "group" }, mode),
      h("label", { class: "group inline" }, overhang, " Overhangs ", h("span", { class: "swatch overhang", "aria-hidden": "true" })),
      h("div", { class: "group" }, axis, this.sectionSlider),
      h("label", { class: "group inline" }, measure, " Measure ", this.measureOut),
      h("div", { class: "group" }, shot, stl));

    this.tabs.model.addEventListener("click", () => this.showTab("model"));
    this.tabs.assembly.addEventListener("click", () => this.showTab("assembly"));
    this.tabs.toolpaths.addEventListener("click", () => this.showTab("toolpaths"));
    replace(this.stage,
      h("div", { class: "tabs", role: "tablist" }, this.tabs.model, this.tabs.assembly, this.tabs.toolpaths),
      h("div", { class: "pane model-pane" }, toolbar, this.canvasHost, this.info, this.warning, this.empty),
      h("div", { class: "pane assembly-pane", hidden: true }, this.assembly.el),
      h("div", { class: "pane toolpaths-pane", hidden: true }, this.toolpaths.el));

    this.followSelect.addEventListener("change", () => this.setFollow(decodeFollow(this.followSelect.value)));

    replace(this.el,
      h("header", { class: "topbar" },
        h("a", { href: "/", class: "brand" }, h("span", { class: "logo", "aria-hidden": "true" }), "Print Pipeline"),
        this.projectSelect, this.versionLabel,
        h("label", { class: "inline small follow", title: "Switch to the project an agent session works on" }, "Following ", this.followSelect),
        h("span", { class: "spacer" }),
        h("label", { class: "inline small" }, "Printer ", this.printerSelect),
        this.printBtn,
        this.status.el, this.connection,
        h("a", { href: "/settings", class: "button" }, "Settings")),
      h("div", { class: "body" },
        this.stage,
        h("aside", { class: "sidebar" }, this.sessions.el, this.job.el, this.estimate.el, this.params.el, this.history.el,
          h("details", { class: "panel" }, h("summary", {}, "Run log"), this.log.el))));
  }

  private tab: "model" | "assembly" | "toolpaths" = "model";

  private showTab(tab: "model" | "assembly" | "toolpaths") {
    this.tab = tab;
    for (const t of ["model", "assembly", "toolpaths"] as const) {
      this.tabs[t].setAttribute("aria-selected", String(t === tab));
      (this.stage.querySelector(`.${t}-pane`) as HTMLElement).hidden = t !== tab;
    }
    if (tab === "toolpaths") void this.loadToolpaths();
    if (tab === "assembly" && this.project) void this.assembly.open(this.project, this.version);
  }

  private async loadToolpaths() {
    if (!this.project) return;
    const src = this.toolpathSource ?? (this.toolpaths.following ? { url: this.toolpaths.followUrl! } : { url: urls.slice(this.project, this.version, this.printerId) });
    await this.toolpaths.load(src.url, this.currentPrinter()?.capabilities.build_volume ?? { X: 256, Y: 256, Z: 256 });
  }

  // ---- events -------------------------------------------------------------

  private wire() {
    this.projectSelect.addEventListener("change", () => void this.openProject(this.projectSelect.value, 0));
    this.printerSelect.addEventListener("change", () => {
      this.printerId = this.printerSelect.value;
      this.applyPrinter();
      this.syncPrint(this.currentPrinter()?.print_job ?? null, this.currentPrinter()?.status ?? null);
      void this.refreshProject();
    });
    this.printBtn.addEventListener("click", () => {
      if (!this.project || !this.printerId) return;
      void this.job.request(this.project, this.version, this.printerId, this.currentPrinter()?.name ?? "");
    });
    this.job.onShowToolpaths = (job: Job) => {
      this.toolpathSource = { url: urls.jobFile(job.project, job.id) };
      this.showTab("toolpaths");
      this.toolpathSource = null;
    };
    this.history.onView = (v) => void this.showVersion(v);
    this.estimate.onDone = () => this.reloadHistory();

    this.live.onConnection = (ok) => {
      this.connection.className = `conn ${ok ? "on" : "off"}`;
      this.connection.title = ok ? "Live updates connected" : "Reconnecting to the server…";
    };
    this.live.on("model", async (e) => {
      if (e.project !== this.project && !follows(this.follow, e)) {
        await this.loadProjects();
        return;
      }
      if (e.project !== this.project) {
        await this.loadProjects();
        await this.openProject(e.project, e.version);
        return;
      }
      this.latest = Math.max(this.latest, e.version);
      await this.showVersion(e.version);
      void this.params.load(this.project, e.version);
      this.reloadHistory();
      void this.log.load(this.project);
    });
    this.live.on("job", (e) => {
      if (e.job.project !== this.project) {
        if (!follows(this.follow, e)) return;
        void this.openProject(e.job.project, e.job.version);
      }
      this.job.show(e);
      void this.log.load(e.job.project);
      this.reloadHistory();
    });
    this.live.on("estimate", (e) => {
      this.estimate.receive(e);
      if (e.project === this.project) this.reloadHistory();
    });
    this.live.on("printer", (e) => {
      this.status.receive(e.printer_id, e.status);
      this.job.setPrinterState(e.printer_id, e.status.state);
      if (e.job) this.job.show({ job: e.job });
      if (e.printer_id === this.printerId) this.syncPrint(e.print_job ?? null, e.status);
    });
    this.toolpaths.onFollowChange = () => {
      if (this.tab === "toolpaths") void this.loadToolpaths();
    };
    this.status.onWatch = () => {
      if (!this.toolpaths.followUrl) {
        toast("This print was not sent from here, so there are no toolpaths to follow.", "info");
        return;
      }
      this.toolpaths.follow();
      this.showTab("toolpaths");
    };
    this.live.on("session", (e) => {
      this.sessions.receive(e.session);
      if (!e.session.open && this.follow.mode === "session" && this.follow.id === e.session.id) {
        toast(`${clientLabel(e.session)} disconnected; following all sessions again.`, "info");
        this.setFollow({ mode: "all" });
      }
      this.renderFollow();
    });
    this.sessions.onFollow = (f) => this.setFollow(f);
    this.sessions.onOpenProject = (p) => void this.openProject(p, 0).then(() => this.loadProjects());
    this.live.on("assembly", (r) => this.assembly.receive(r));
    this.live.on("settings", () => {
      void this.loadSettings();
      void this.loadPrinters();
    });
  }

  private reloadHistory = debounce(() => {
    if (this.project) void this.history.load(this.project, this.printerId, this.version);
  }, 150);

  private setFollow(f: Follow) {
    this.follow = f;
    try {
      localStorage.setItem("printpipe.follow", encodeFollow(f));
    } catch {
      /* private window: the choice lasts for this page only */
    }
    this.sessions.setFollow(f);
    this.renderFollow();
    if (f.mode === "session") {
      const s = this.sessions.open.find((x) => x.id === f.id);
      if (s?.project && s.project !== this.project) void this.openProject(s.project, 0).then(() => this.loadProjects());
    }
  }

  private renderFollow() {
    const open = this.sessions.open;
    if (this.follow.mode === "session" && !open.some((s) => this.follow.mode === "session" && s.id === this.follow.id)) {
      this.follow = { mode: "all" };
    }
    replace(this.followSelect,
      h("option", { value: "all" }, `All sessions (${open.length})`),
      ...open.map((s) => h("option", { value: `session:${s.id}` }, `${clientLabel(s)}${s.project ? ` · ${s.project}` : ""}`)),
      h("option", { value: "none" }, "Nobody (stay here)"));
    this.followSelect.value = encodeFollow(this.follow);
  }

  // ---- data ---------------------------------------------------------------

  private async loadSettings() {
    try {
      this.settings = await api.settings();
      const angle = this.settings.viewer.overhang_angle;
      this.viewer.setOverhangs(true, angle);
      const mode = this.settings.viewer.display_mode as DisplayMode;
      this.viewer.setMode(mode);
      const sel = this.el.querySelector<HTMLSelectElement>('[data-testid="mode"]');
      if (sel) sel.value = mode;
    } catch (e) {
      toast((e as Error).message, "error");
    }
  }

  /** Hands the selected printer's print to the toolpath view, loading its file if it follows. */
  private syncPrint(job: Job | null, status: PrinterStatus | null) {
    const url = job ? urls.jobFile(job.project, job.id) : null;
    if (this.toolpaths.setPrint(url, status) && this.tab === "toolpaths") void this.loadToolpaths();
  }

  private async loadPrinters() {
    try {
      this.printers = await api.printers();
    } catch (e) {
      toast((e as Error).message, "error");
      this.printers = [];
    }
    if (!this.printers.some((p) => p.id === this.printerId)) this.printerId = (this.printers.find((p) => p.default) ?? this.printers[0])?.id ?? "";
    replace(this.printerSelect, ...this.printers.map((p) => h("option", { value: p.id, selected: p.id === this.printerId }, p.name)));
    this.printerSelect.disabled = this.printers.length < 2;
    this.printBtn.disabled = this.printers.length === 0;
    for (const p of this.printers) this.job.setPrinterState(p.id, p.status?.state ?? "idle", p.name);
    const cur = this.currentPrinter();
    this.syncPrint(cur?.print_job ?? null, cur?.status ?? null);
    this.applyPrinter();
  }

  private currentPrinter() {
    return this.printers.find((p) => p.id === this.printerId) ?? null;
  }

  private applyPrinter() {
    const p = this.currentPrinter();
    this.viewer.setPrinter(p?.capabilities ?? null);
    this.assembly.setPrinter(p?.capabilities ?? null);
    this.status.set(p);
    this.estimate.set(this.project, this.version, this.printerId);
  }

  private async loadProjects() {
    try {
      this.projects = await api.projects();
    } catch {
      this.projects = [];
    }
    replace(this.projectSelect, ...this.projects.map((p) => h("option", { value: p.name, selected: p.name === this.project }, p.name)));
    this.projectSelect.disabled = this.projects.length === 0;
  }

  private async openProject(name: string, version: number) {
    this.project = name;
    this.empty.hidden = true;
    this.projectSelect.value = name;
    const versions = await api.versions(name).catch(() => []);
    this.latest = versions.at(-1)?.version ?? 0;
    await this.showVersion(version || this.latest);
    await Promise.all([this.params.load(name, this.version), this.log.load(name)]);
  }

  private async refreshProject() {
    if (!this.project) return;
    this.estimate.set(this.project, this.version, this.printerId);
    this.reloadHistory();
  }

  private async showVersion(version: number) {
    if (!this.project || !version) return;
    this.version = version;
    this.versionLabel.textContent = version === this.latest ? `v${version} (latest)` : `v${version} of ${this.latest}`;
    try {
      await this.viewer.showModel(urls.stl(this.project, version));
    } catch (e) {
      toast((e as Error).message, "error");
    }
    this.estimate.set(this.project, version, this.printerId);
    this.history.setShown(version);
    void this.job.load(this.project, version);
    this.reloadHistory();
    if (version !== this.latest) void this.params.load(this.project, version);
    if (this.tab === "toolpaths") void this.loadToolpaths();
    if (this.tab === "assembly") void this.assembly.open(this.project, version);
  }

  private showInfo(i: ModelInfo) {
    const p = this.currentPrinter();
    replace(this.info,
      h("span", { "data-testid": "size" }, formatSize(i.size)),
      i.overhangShare > 0.005 ? h("span", { class: "small muted" }, ` · ${Math.round(i.overhangShare * 100)}% overhang`) : null);
    this.warning.hidden = i.fits;
    if (!i.fits) {
      const v = p?.capabilities.build_volume ?? { X: 256, Y: 256, Z: 256 };
      this.warning.textContent = `Too big for the ${p?.capabilities.model ?? "printer"} (${v.X} × ${v.Y} × ${v.Z} mm): ${i.overflow.join(", ") || "size"} exceeds the build volume.`;
    }
  }

  private showEmpty() {
    this.empty.hidden = false;
    const hasPrinter = this.printers.length > 0;
    replace(this.empty,
      h("h2", {}, "Waiting for the agent"),
      h("p", {}, "Ask your agent to make a part, for example: “Make a 20 mm calibration cube with a 2 mm chamfer, project name test-cube.” It appears here as soon as it compiles."),
      hasPrinter ? null : h("p", {}, h("a", { href: "/settings" }, "Add your printer in Settings"), " first, so the bed and estimates match it."));
  }
}
