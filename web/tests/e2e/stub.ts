import type { Page, Route } from "@playwright/test";
import { readFileSync } from "node:fs";

// A 20 mm cube sliced by Bambu Studio 02.08.02.61 with A1 presets (100 layers).
const slicedCube = readFileSync(new URL("./fixtures/cube.gcode.3mf", import.meta.url));

/** ASCII STL of an axis-aligned box. */
export function cubeSTL(x: number, y = x, z = x): string {
  const p = [[0, 0, 0], [x, 0, 0], [x, y, 0], [0, y, 0], [0, 0, z], [x, 0, z], [x, y, z], [0, y, z]];
  const f = [[0, 2, 1], [0, 3, 2], [4, 5, 6], [4, 6, 7], [0, 1, 5], [0, 5, 4], [1, 2, 6], [1, 6, 5], [2, 3, 7], [2, 7, 6], [3, 0, 4], [3, 4, 7]];
  return "solid cube\n" + f.map((t) => ` facet normal 0 0 0\n  outer loop\n${t.map((i) => `   vertex ${p[i]!.join(" ")}`).join("\n")}\n  endloop\n endfacet`).join("\n") + "\nendsolid cube\n";
}

export const A1 = {
  id: "a1", name: "Workshop A1", kind: "bambu-lan", default: true, profiles: {},
  capabilities: { model: "Bambu Lab A1", build_volume: { X: 256, Y: 256, Z: 256 }, bed_shape: "rect", job_format: ".gcode.3mf", slicer: "bambustudio", features: ["status", "cancel"] },
  status: { state: "idle", progress: 0, layer: 0, total_layers: 0 },
};

export const estimate = {
  project: "cube", version: 1, printer_id: "a1", status: "done", minutes: 21, grams: 7.4, metres: 2.48, filament: "PLA",
  layers: 100, layer_height_mm: 0.2, supports: false, cost: 0.19, currency: "EUR", computed_at: "2026-10-12T10:00:00Z", key: "k1",
};

export const settings = {
  printers: [{ id: "a1", name: "Workshop A1", kind: "bambu-lan", default: true, config: { ip: "192.168.1.50", serial: "03919C", access_code: "••••••••" }, profiles: {} }],
  profiles: [],
  slicer: { supports: true, support_type: "tree(auto)", layer_height: 0, infill: -1 },
  viewer: { overhang_angle: 45, display_mode: "solid" },
  pipeline: { approval_required: true, preview_angles: ["iso", "front"], workspace: "/work", log_level: "info" },
  currency: "EUR",
};

export const kinds = {
  printers: [
    { kind: "bambu-lan", label: "Bambu Lab A1 (LAN, Developer Mode)", fields: [
      { key: "ip", label: "IP address", type: "ip", required: true },
      { key: "serial", label: "Serial number", type: "text", required: true },
      { key: "access_code", label: "LAN access code", type: "secret", required: true },
    ] },
    { kind: "fake", label: "Fake printer (simulated, for testing)", fields: [{ key: "print_seconds", label: "Simulated print time (seconds)", type: "number", required: false, default: "60" }] },
  ],
  slicers: ["bambustudio", "fake"], profile_kinds: ["machine", "process", "filament"], preview_angles: ["iso", "front", "right", "top"],
  display_modes: ["solid", "wireframe", "xray", "edges"], log_levels: ["debug", "info", "warn", "error"], support_types: ["tree(auto)", "normal(auto)"],
};

export const sessionList = [
  { id: "SESS-CODE", client: "claude-code", client_title: "Claude Code", client_version: "2.1.3", transport: "http",
    remote_addr: "172.17.0.1:51234", user_agent: "claude-code/2.1.3", connected_at: "2026-10-12T10:00:00Z", last_seen: "2026-10-12T10:05:00Z",
    calls: 4, last_tool: "compile_model", project: "cube", open: true },
  { id: "SESS-DESK", client: "claude-desktop", client_version: "0.14.2", transport: "bridge",
    remote_addr: "127.0.0.1:40000", user_agent: "printpipe-bridge/dev", connected_at: "2026-10-12T09:00:00Z", last_seen: "2026-10-12T09:30:00Z",
    calls: 1, last_tool: "list_printers", open: true },
];

export interface StubOptions {
  size?: number;
  jobs?: unknown[];
  requests?: { method: string; url: string; body: string | null }[];
}

/** Routes every /api call to canned answers; records the requests it sees. */
export async function stubApi(page: Page, opts: StubOptions = {}) {
  const size = opts.size ?? 20;
  const requests = opts.requests ?? [];
  const json = (route: Route, body: unknown, status = 200) => route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) });
  await page.route("**/api/**", async (route) => {
    const req = route.request();
    const url = new URL(req.url());
    requests.push({ method: req.method(), url: url.pathname + url.search, body: req.postData() });
    const path = url.pathname;
    if (path === "/api/events") return route.fulfill({ status: 200, contentType: "text/event-stream", body: "retry: 600000\n\n" });
    if (path === "/api/settings" && req.method() === "GET") return json(route, settings);
    if (path === "/api/settings" && req.method() === "PUT") return json(route, JSON.parse(req.postData() ?? "{}"));
    if (path === "/api/printer-kinds") return json(route, kinds);
    if (path === "/api/sessions") return json(route, sessionList);
    if (path.startsWith("/api/sessions/") && req.method() === "GET") {
      const id = decodeURIComponent(path.split("/")[3]!);
      const s = sessionList.find((x) => x.id === id);
      return s ? json(route, { session: s, runs: [{ session: id, client: s.client, project: "cube", tool: "compile_model", args: "{}", exit_code: 0, message: "ok", started_at: "2026-10-12T10:05:00Z" }] }) : json(route, { error: "no such session" }, 404);
    }
    if (path.startsWith("/api/sessions/") && req.method() === "DELETE") return json(route, { ok: true });
    if (path === "/api/printers") return json(route, [A1]);
    if (path === "/api/projects") return json(route, [{ name: "cube", latest_version: 1, updated_at: "2026-10-12T10:00:00Z" }]);
    if (path === "/api/projects/cube/versions") return json(route, [{ version: 1, params: { size }, created_at: "2026-10-12T10:00:00Z", size_mm: { X: size, Y: size, Z: size }, estimates: [] }]);
    if (path === "/api/projects/cube/model.stl") return route.fulfill({ status: 200, contentType: "model/stl", body: cubeSTL(size) });
    if (path === "/api/projects/cube/params") return json(route, [{ name: "size", type: "number", value: size, min: 5, max: 300, step: 1, label: "Edge length" }]);
    if (path === "/api/projects/cube/jobs") return json(route, opts.jobs ?? []);
    if (path === "/api/projects/cube/log") return json(route, []);
    if (/^\/api\/jobs\/[0-9a-f]{8}\/approve$/.test(path)) {
      const id = path.split("/")[3];
      return json(route, { id, project: "cube", version: 1, printer_id: "a1", approved: true, printed: false, estimate });
    }
    if (path === "/api/projects/cube/versions/1/estimate") return json(route, estimate);
    if (path === "/api/projects/cube/versions/1/job.gcode.3mf" || path === "/api/projects/cube/job.gcode.3mf")
      return route.fulfill({ status: 200, contentType: "application/vnd.ms-package.3dmanufacturing-3dmodel+xml", body: slicedCube });
    return json(route, { error: `no stub for ${req.method()} ${path}` }, 404);
  });
  return requests;
}
