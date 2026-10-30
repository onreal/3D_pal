import { afterEach, expect, test, vi } from "vitest";
import type { SessionInfo } from "../api";
import { SessionsPanel } from "./panel";

const session = (id: string, client: string): SessionInfo => ({
  id, client, client_version: "1", transport: "http", connected_at: "2026-01-01T00:00:00Z",
  last_seen: "2026-01-01T00:00:00Z", calls: 0, open: true,
});

afterEach(() => vi.unstubAllGlobals());

test("counts a client once and clears all its connections", async () => {
  let listed = [session("a", "claude-code"), session("b", "CLAUDE-CODE"), session("c", "other")];
  const fetchMock = vi.fn(async (_url: string, init?: RequestInit) => {
    if (init?.method === "DELETE") {
      listed = [];
      return new Response(JSON.stringify({ disconnected: 3 }), { status: 200 });
    }
    return new Response(JSON.stringify(listed), { status: 200 });
  });
  vi.stubGlobal("fetch", fetchMock);
  vi.stubGlobal("confirm", () => true);
  const panel = new SessionsPanel();
  await panel.load();

  expect(panel.el.querySelector('[data-testid="session-count"]')?.textContent).toBe("2");
  expect(panel.el.querySelectorAll(":scope > ul.sessions > li")).toHaveLength(2);
  const group = panel.el.querySelector<HTMLElement>(".client-group")!;
  expect(group.textContent).toContain("2 connections");
  group.querySelector<HTMLButtonElement>("button")!.click();
  expect(panel.el.querySelectorAll(".client-connections > li")).toHaveLength(2);

  panel.el.querySelector<HTMLButtonElement>(".panel-head button")!.click();
  await vi.waitFor(() => expect(panel.el.querySelector('[data-testid="session-count"]')?.textContent).toBe("0"));
  panel.receive({ ...session("a", "claude-code"), open: false }); // a delayed close event cannot restore a cleared row
  expect(panel.el.querySelector(".closed-sessions")).toBeNull();
  expect(fetchMock).toHaveBeenCalledWith("/api/sessions", expect.objectContaining({ method: "DELETE" }));
});
