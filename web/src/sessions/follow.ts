import type { FromSession } from "../events";

/** Which agent work the viewer follows: every session, one session, or none (manual). */
export type Follow = { mode: "all" } | { mode: "none" } | { mode: "session"; id: string };

/** Whether an event from an agent session should move the viewer to its project. */
export function follows(f: Follow, e: FromSession): boolean {
  if (!e.session) return false; // the viewer's own actions never switch projects
  if (f.mode === "all") return true;
  if (f.mode === "session") return e.session === f.id;
  return false;
}

export function encodeFollow(f: Follow): string {
  return f.mode === "session" ? `session:${f.id}` : f.mode;
}

export function decodeFollow(v: string | null | undefined): Follow {
  if (v === "none") return { mode: "none" };
  if (v?.startsWith("session:") && v.length > 8) return { mode: "session", id: v.slice(8) };
  return { mode: "all" };
}

/** A short name for a client: its title, or its name with the version. */
export function clientLabel(s: { client: string; client_title?: string; client_version?: string }): string {
  const name = s.client_title || s.client || "unknown client";
  return s.client_version ? `${name} ${s.client_version}` : name;
}

export const TRANSPORT_LABELS: Record<string, string> = {
  http: "HTTP",
  bridge: "stdio bridge",
  stdio: "stdio",
};
