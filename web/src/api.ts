// Typed client for the printpipe HTTP API. Shapes mirror the Go JSON.

export interface Vec { X: number; Y: number; Z: number }

export interface ProjectSummary { name: string; latest_version: number; updated_at: string }

export interface Estimate {
  project: string;
  version: number;
  printer_id: string;
  status: "running" | "done" | "failed";
  error?: string;
  minutes: number;
  grams: number;
  metres: number;
  filament: string;
  layers: number;
  layer_height_mm: number;
  supports: boolean;
  cost?: number;
  currency?: string;
  computed_at: string;
  key: string;
}

export interface VersionInfo {
  version: number;
  params: Record<string, unknown>;
  created_at: string;
  size_mm?: Vec;
  estimates: Estimate[];
}

export interface ParamOption { value: number | string; label: string }

export interface Param {
  name: string;
  type: "number" | "boolean" | "string" | "vector";
  value: unknown;
  min?: number;
  max?: number;
  step?: number;
  options?: ParamOption[];
  label?: string;
  group?: string;
}

export interface Job {
  id: string;
  project: string;
  printer_id: string;
  version: number;
  approved: boolean;
  printed: boolean;
  discarded?: boolean;
  estimate?: Estimate;
}

export interface Approval { job: Job; estimate: Estimate; printer_name: string; viewer_url: string }

export type PrinterState = "idle" | "printing" | "paused" | "finished" | "failed" | "offline";

export interface PrinterStatus {
  state: PrinterState;
  progress: number;
  layer: number;
  total_layers: number;
  message?: string;
  remaining_s?: number; // the printer's own estimate
  job_name?: string;
}

export interface Capabilities {
  model: string;
  build_volume: Vec;
  bed_shape: "rect" | "round";
  job_format: string;
  slicer: string;
  features: string[] | null;
}

export interface PrinterInfo {
  id: string;
  name: string;
  kind: string;
  default: boolean;
  capabilities: Capabilities;
  profiles: Record<string, string> | null;
  system_profiles?: Record<string, string>; // the slicer's own preset used for each kind left empty
  print_job?: Job; // what the printer is printing (or just finished)
  status?: PrinterStatus;
  error?: string;
}

export interface CompileResult { project: string; version: number; size_mm: Vec; fits: boolean; params: Record<string, unknown>; warnings?: string[] }

export interface Field {
  key: string;
  label: string;
  type: "text" | "ip" | "secret" | "number" | "select";
  help?: string;
  required: boolean;
  default?: string;
  options?: string[];
}

export interface PrinterKind { kind: string; label: string; fields: Field[] }

export interface Kinds {
  printers: PrinterKind[];
  slicers: string[];
  profile_kinds: string[];
  preview_angles: string[];
  display_modes: string[];
  log_levels: string[];
  support_types: string[];
}

export interface PrinterSettings {
  id: string;
  name: string;
  kind: string;
  default: boolean;
  config: Record<string, string>;
  profiles: Record<string, string>;
}

export interface Profile { name: string; kind: string; price_per_kg?: number }

/** A preset the slicer ships (Bambu Studio's system presets for the printer's model). */
export interface Preset { kind: string; name: string; default: boolean; price_per_kg?: number }

export interface Settings {
  printers: PrinterSettings[];
  profiles: Profile[];
  slicer: { supports: boolean; support_type: string; layer_height: number; infill: number };
  viewer: { overhang_angle: number; display_mode: string };
  pipeline: { approval_required: boolean; preview_angles: string[]; workspace: string; log_level: string };
  currency: string;
}

export interface Check { name: string; ok: boolean; message: string }

export interface SessionInfo {
  id: string;
  client: string;
  client_version: string;
  client_title?: string;
  transport: "http" | "bridge" | "stdio";
  remote_addr?: string;
  user_agent?: string;
  connected_at: string;
  last_seen: string;
  calls: number;
  last_tool?: string;
  project?: string;
  open: boolean;
  closed_at?: string;
}

export interface AssemblyPart {
  index: number;
  id: string;
  label: string;
  color?: string;
  count: number;
  min: Vec;
  max: Vec;
  volume_mm3: number;
  triangles: number;
  error?: string;
}

export interface AssemblyResult {
  project: string;
  version: number;
  status: "running" | "done" | "failed";
  error?: string;
  mode?: "steps" | "pieces";
  note?: string;
  parts: AssemblyPart[];
  min: Vec;
  max: Vec;
}

export interface Run {
  session?: string;
  client?: string;
  project?: string;
  tool: string;
  args: string;
  exit_code: number | null;
  message: string;
  started_at: string;
  finished_at?: string;
}

export const MASK = "••••••••";

export class ApiError extends Error {
  constructor(public status: number, message: string, public problems: string[] = []) {
    super(message);
  }
}

async function request<T>(method: string, path: string, body?: unknown, raw?: BodyInit): Promise<T> {
  const init: RequestInit = { method, headers: {} };
  if (raw !== undefined) {
    init.body = raw;
  } else if (body !== undefined) {
    init.body = JSON.stringify(body);
    (init.headers as Record<string, string>)["Content-Type"] = "application/json";
  }
  const res = await fetch(path, init);
  const text = await res.text();
  let data: unknown = null;
  try {
    data = text ? JSON.parse(text) : null;
  } catch {
    data = null;
  }
  if (!res.ok) {
    const err = (data ?? {}) as { error?: string; problems?: string[] };
    throw new ApiError(res.status, err.error ?? `${res.status} ${res.statusText}`, err.problems ?? []);
  }
  return data as T;
}

const enc = encodeURIComponent;

export const api = {
  projects: () => request<ProjectSummary[]>("GET", "/api/projects"),
  versions: (p: string) => request<VersionInfo[]>("GET", `/api/projects/${enc(p)}/versions`),
  params: (p: string, v = 0) => request<Param[]>("GET", `/api/projects/${enc(p)}/params${v ? `?v=${v}` : ""}`),
  setParams: (p: string, params: Record<string, unknown>) => request<CompileResult>("POST", `/api/projects/${enc(p)}/params`, { params }),
  restore: (p: string, v: number) => request<CompileResult>("POST", `/api/projects/${enc(p)}/versions/${v}/restore`),
  pendingJobs: (p: string) => request<Job[]>("GET", `/api/projects/${enc(p)}/jobs`),
  log: (p: string) => request<Run[]>("GET", `/api/projects/${enc(p)}/log`),
  runs: () => request<Run[]>("GET", "/api/runs"),
  assembly: (p: string, v: number) => request<AssemblyResult>("GET", `/api/projects/${enc(p)}/versions/${v}/assembly`),
  sessions: () => request<SessionInfo[]>("GET", "/api/sessions"),
  session: (id: string) => request<{ session: SessionInfo; runs: Run[] }>("GET", `/api/sessions/${enc(id)}`),
  endSession: (id: string) => request<{ ok: boolean }>("DELETE", `/api/sessions/${enc(id)}`),
  startEstimate: (p: string, v: number, printerId: string) =>
    request<Estimate>("POST", `/api/projects/${enc(p)}/versions/${v}/estimate`, { printer_id: printerId }),
  approve: (id: string) => request<Job>("POST", `/api/jobs/${enc(id)}/approve`),
  discard: (id: string) => request<Job>("POST", `/api/jobs/${enc(id)}/discard`),
  requestJob: (p: string, v: number, printerId: string) => request<Approval>("POST", `/api/projects/${enc(p)}/versions/${v}/job`, { printer_id: printerId }),
  printJob: (id: string) => request<{ job: Job; printer: string }>("POST", `/api/jobs/${enc(id)}/print`),
  job: (id: string) => request<Job>("GET", `/api/jobs/${enc(id)}`),
  printers: (status = true) => request<PrinterInfo[]>("GET", `/api/printers${status ? "" : "?status=0"}`),
  kinds: () => request<Kinds>("GET", "/api/printer-kinds"),
  testPrinter: (id: string) => request<{ ok: boolean; checks: Check[] }>("POST", `/api/printers/${enc(id)}/test`),
  presets: (id: string) => request<Preset[]>("GET", `/api/printers/${enc(id)}/presets`),
  importPreset: (id: string, kind: string, name: string) => request<Profile>("POST", `/api/printers/${enc(id)}/presets`, { kind, name }),
  cancel: (id: string) => request<{ ok: boolean }>("POST", `/api/printers/${enc(id)}/cancel`),
  settings: () => request<Settings>("GET", "/api/settings"),
  saveSettings: (s: Settings) => request<Settings>("PUT", "/api/settings", s),
  exportSettings: () => request<unknown>("GET", "/api/settings/export"),
  importSettings: (bundle: unknown) => request<Settings>("POST", "/api/settings/import", bundle),
  uploadProfile: (kind: string, file: File) => {
    const form = new FormData();
    form.append("file", file);
    return request<Profile>("POST", `/api/profiles/${enc(kind)}`, undefined, form);
  },
};

export const urls = {
  stl: (p: string, v: number) => `/api/projects/${enc(p)}/model.stl?v=${v}`,
  scad: (p: string, v: number) => `/api/projects/${enc(p)}/model.scad?v=${v}`,
  slice: (p: string, v: number, printerId: string) => `/api/projects/${enc(p)}/versions/${v}/job.gcode.3mf?printer=${enc(printerId)}`,
  assemblyPart: (p: string, v: number, i: number) => `/api/projects/${enc(p)}/versions/${v}/assembly/parts/${i}`,
  jobFile: (p: string, jobId: string) => `/api/projects/${enc(p)}/job.gcode.3mf?job=${enc(jobId)}`,
};
