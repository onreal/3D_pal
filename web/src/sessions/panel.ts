import { api, type SessionInfo } from "../api";
import { h, replace, toast } from "../dom";
import { timeAgo } from "../format";
import { TRANSPORT_LABELS, clientLabel, type Follow } from "./follow";

/** Open agent sessions: which client holds each one, what it is doing, follow or end it. */
export class SessionsPanel {
  readonly el = h("section", { class: "panel sessions-panel", "aria-label": "Agent sessions" });
  private sessions: SessionInfo[] = [];
  private follow: Follow = { mode: "all" };
  private expanded = new Set<string>();
  private details = new Map<string, HTMLElement>();
  onFollow: (f: Follow) => void = () => {};
  onOpenProject: (project: string) => void = () => {};

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

  private render() {
    const open = this.open;
    const closed = this.sessions.filter((s) => !s.open).slice(0, 5);
    replace(this.el,
      h("div", { class: "panel-head" },
        h("h2", {}, "Sessions ", h("span", { class: "badge", "data-testid": "session-count" }, String(open.length))),
        h("span", { class: "muted small" }, this.follow.mode === "all" ? "following all" : this.follow.mode === "none" ? "not following" : "following one")),
      open.length
        ? h("ul", { class: "sessions" }, ...open.map((s) => this.row(s)))
        : h("p", { class: "muted small" }, "No agent is connected. Claude Code connects to http://127.0.0.1:8080/mcp; see the README."),
      closed.length ? h("details", { class: "closed-sessions" }, h("summary", { class: "small muted" }, `Recently closed (${closed.length})`),
        h("ul", { class: "sessions" }, ...closed.map((s) => this.row(s)))) : null);
  }
}
