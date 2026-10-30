import type { FromSession } from "../events";

/** Which agent work the viewer follows: all clients, one client or session, or none. */
export type Follow = { mode: "all" } | { mode: "none" } | { mode: "session"; id: string } | { mode: "client"; client: string };

export function clientKey(client: string): string {
  return client.trim().toLowerCase() || "unknown client";
}

/** Whether an event from an agent session should move the viewer to its project. */
export function follows(f: Follow, e: FromSession): boolean {
  if (!e.session) return false; // the viewer's own actions never switch projects
  if (f.mode === "all") return true;
  if (f.mode === "session") return e.session === f.id;
  if (f.mode === "client") return !!e.client && clientKey(e.client) === clientKey(f.client);
  return false;
}

export function encodeFollow(f: Follow): string {
  if (f.mode === "session") return `session:${f.id}`;
  if (f.mode === "client") return `client:${clientKey(f.client)}`;
  return f.mode;
}

export function decodeFollow(v: string | null | undefined): Follow {
  if (v === "none") return { mode: "none" };
  if (v?.startsWith("session:") && v.length > 8) return { mode: "session", id: v.slice(8) };
  if (v?.startsWith("client:") && v.length > 7) return { mode: "client", client: v.slice(7) };
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
