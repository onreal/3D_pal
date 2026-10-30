import { api, type SessionInfo } from "../api";
import { h, replace, toast } from "../dom";
import { timeAgo } from "../format";
import { TRANSPORT_LABELS, clientKey, clientLabel, type Follow } from "./follow";

/** Keep each client in one row even when it has many MCP connections. */
export function groupByClient(sessions: SessionInfo[]): SessionInfo[][] {
  const groups = new Map<string, SessionInfo[]>();
  for (const session of sessions) {
    const key = clientKey(session.client);
    const group = groups.get(key) ?? [];
    group.push(session);
    groups.set(key, group);
  }
  return [...groups.values()];
}

/** Open agent sessions: which client holds each one, what it is doing, follow or end it. */
export class SessionsPanel {
  readonly el = h("section", { class: "panel sessions-panel", "aria-label": "Agent sessions" });
  private sessions: SessionInfo[] = [];
  private follow: Follow = { mode: "all" };
  private expanded = new Set<string>();
  private expandedClients = new Set<string>();
  private clearedIDs = new Set<string>();
  private details = new Map<string, HTMLElement>();
  private clearing = false;
  onFollow: (f: Follow) => void = () => {};
  onOpenProject: (project: string) => void = () => {};
  onCleared: () => void = () => {};

  async load() {
    try {
      this.sessions = await api.sessions();
    } catch (e) {
      replace(this.el, h("h2", {}, "Sessions"), h("p", { class: "error" }, (e as Error).message));
      return;
    }
    this.render();
  }

  get open(): SessionInfo[] {
    return this.sessions.filter((s) => s.open);
  }

  setFollow(f: Follow) {
    this.follow = f;
    this.render();
  }

  /** A session event from the server: opened, updated or closed. */
  receive(s: SessionInfo) {
    if (this.clearedIDs.has(s.id)) return;
    const i = this.sessions.findIndex((x) => x.id === s.id);
    if (i >= 0) this.sessions[i] = s;
    else this.sessions.unshift(s);
    this.sessions.sort((a, b) => Number(b.open) - Number(a.open) || Date.parse(b.last_seen) - Date.parse(a.last_seen));
    if (this.expanded.has(s.id)) void this.loadDetail(s.id);
    this.render();
  }

  private async loadDetail(id: string) {
    let body: HTMLElement;
    try {
      const { session, runs } = await api.session(id);
      const facts: [string, string | HTMLElement][] = [
        ["Session ID", h("code", {}, session.id)],
        ["Client", `${session.client}${session.client_version ? ` ${session.client_version}` : ""}`],
        ["Transport", TRANSPORT_LABELS[session.transport] ?? session.transport],
      ];
      if (session.remote_addr) facts.push(["Address", session.remote_addr]);
      if (session.user_agent) facts.push(["User agent", session.user_agent]);
      facts.push(["Connected", new Date(session.connected_at).toLocaleString()]);
      const dl = h("dl", { class: "kv small" });
      for (const [k, v] of facts) dl.append(h("dt", {}, k), h("dd", {}, v));
      body = h("div", { class: "session-detail" },
        dl,
        runs.length
          ? h("ol", { class: "session-runs small" }, ...runs.slice(0, 15).map((r) =>
              h("li", { class: r.exit_code ? "failed" : "" },
                h("span", { class: "muted nowrap" }, new Date(r.started_at).toLocaleTimeString()), " ",
                h("code", {}, r.tool), r.project ? ` · ${r.project}` : "", r.exit_code ? ` · ${r.message}` : "")))
          : h("p", { class: "muted small" }, "No tool calls yet."));
    } catch (e) {
      body = h("p", { class: "error small" }, (e as Error).message);
    }
    this.details.set(id, body);
    this.render();
  }

  private row(s: SessionInfo): HTMLElement {
    const following = this.follow.mode === "session" && this.follow.id === s.id;
    const followBtn = h("button", { type: "button", class: following ? "small primary" : "small", "aria-pressed": String(following), disabled: !s.open },
      following ? "Following" : "Follow");
    followBtn.addEventListener("click", () => this.onFollow(following ? { mode: "all" } : { mode: "session", id: s.id }));
    const end = h("button", { type: "button", class: "link danger small" }, "End");
    end.addEventListener("click", async () => {
      if (!confirm(`End the session of ${clientLabel(s)}? The client will have to reconnect.`)) return;
      try {
        await api.endSession(s.id);
        toast("Session ended.", "ok");
      } catch (e) {
        toast((e as Error).message, "error");
      }
    });
    const more = h("button", { type: "button", class: "link small", "aria-expanded": String(this.expanded.has(s.id)) },
      this.expanded.has(s.id) ? "Hide calls" : "Calls");
    more.addEventListener("click", () => {
      if (this.expanded.has(s.id)) this.expanded.delete(s.id);
      else {
        this.expanded.add(s.id);
        void this.loadDetail(s.id);
      }
      this.render();
    });
    const project = s.project
      ? h("button", { type: "button", class: "link small", onclick: () => this.onOpenProject(s.project!) }, s.project)
      : h("span", { class: "muted small" }, "no project yet");
    return h("li", { class: `session${s.open ? "" : " closed"}${following ? " following" : ""}`, "data-session": s.id },
      h("div", { class: "session-head" },
        h("span", { class: `dot ${s.open ? "finished" : "offline"}`, "aria-hidden": "true" }),
        h("strong", {}, clientLabel(s)),
        h("span", { class: "badge" }, TRANSPORT_LABELS[s.transport] ?? s.transport)),
      h("div", { class: "session-meta small muted" },
        s.open ? `connected ${timeAgo(s.connected_at)} · ` : `closed ${timeAgo(s.closed_at ?? s.last_seen)} · `,
        `${s.calls} call${s.calls === 1 ? "" : "s"}`,
        s.last_tool ? ` · last ${s.last_tool} ${timeAgo(s.last_seen)}` : ""),
      h("div", { class: "session-actions" }, project, h("span", { class: "spacer" }), more, s.open ? followBtn : null, s.open ? end : null),
      this.expanded.has(s.id) ? this.details.get(s.id) ?? h("p", { class: "muted small" }, "Loading…") : null);
  }

  private clientRow(group: SessionInfo[]): HTMLElement {
    if (group.length === 1) return this.row(group[0]!);
    const first = group[0]!;
    const key = clientKey(first.client);
    const expanded = this.expandedClients.has(key);
    const calls = group.reduce((total, s) => total + s.calls, 0);
    const show = h("button", { type: "button", class: "link small", "aria-expanded": String(expanded) },
      expanded ? "Hide connections" : "Connections");
    const following = this.follow.mode === "client" && clientKey(this.follow.client) === key;
    const follow = h("button", { type: "button", class: following ? "small primary" : "small", "aria-pressed": String(following) },
      following ? "Following" : "Follow");
    follow.addEventListener("click", () => this.onFollow(following ? { mode: "all" } : { mode: "client", client: first.client }));
    show.addEventListener("click", () => {
      if (expanded) this.expandedClients.delete(key);
      else this.expandedClients.add(key);
      this.render();
    });
    return h("li", { class: `session client-group${first.open ? "" : " closed"}${following ? " following" : ""}` },
      h("div", { class: "session-head" },
        h("span", { class: `dot ${first.open ? "finished" : "offline"}`, "aria-hidden": "true" }),
        h("strong", {}, clientLabel(first)),
        h("span", { class: "badge" }, `${group.length} connections`)),
      h("div", { class: "session-meta small muted" },
        `${calls} call${calls === 1 ? "" : "s"} across ${group.length} connections`),
      h("div", { class: "session-actions" }, h("span", { class: "spacer" }), show, first.open ? follow : null),
      expanded ? h("ul", { class: "sessions client-connections" }, ...group.map((s) => this.row(s))) : null);
  }

  private async clearAll() {
    const count = this.open.length;
    if (!confirm(`Clear all clients and disconnect ${count} active connection${count === 1 ? "" : "s"}?`)) return;
    const clearedIDs = this.sessions.map((s) => s.id);
    this.clearing = true;
    this.render();
    try {
      const result = await api.clearSessions();
      for (const id of clearedIDs) this.clearedIDs.add(id);
      this.expanded.clear();
      this.expandedClients.clear();
      this.details.clear();
      await this.load();
      this.onCleared();
      toast(`${result.disconnected} connection${result.disconnected === 1 ? "" : "s"} disconnected.`, "ok");
    } catch (e) {
      toast((e as Error).message, "error");
    } finally {
      this.clearing = false;
      this.render();
    }
  }

  private render() {
    const open = this.open;
    const openClients = groupByClient(open);
    const closedClients = groupByClient(this.sessions.filter((s) => !s.open)).slice(0, 5);
    const clear = h("button", { type: "button", class: "link danger small", disabled: this.sessions.length === 0 || this.clearing }, "Clear all clients");
    clear.addEventListener("click", () => void this.clearAll());
    replace(this.el,
      h("div", { class: "panel-head" },
        h("h2", {}, "Clients ", h("span", { class: "badge", "data-testid": "session-count" }, String(openClients.length))),
        clear,
        h("span", { class: "muted small" }, this.follow.mode === "all" ? "following all" : this.follow.mode === "none" ? "not following" : "following one")),
      open.length
        ? h("ul", { class: "sessions" }, ...openClients.map((group) => this.clientRow(group)))
        : h("p", { class: "muted small" }, "No agent is connected. Claude Code connects to http://127.0.0.1:8080/mcp; see the README."),
      closedClients.length ? h("details", { class: "closed-sessions" }, h("summary", { class: "small muted" }, `Recently closed (${closedClients.length})`),
        h("ul", { class: "sessions" }, ...closedClients.map((group) => this.clientRow(group)))) : null);
  }
}
