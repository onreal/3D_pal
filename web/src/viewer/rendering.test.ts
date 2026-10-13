import { describe, expect, test, vi } from "vitest";
import { RenderStatus, STALE_MS, renderedBy } from "./rendering";

describe("render status on the viewer", () => {
  test("says who is rendering, counts the time, and clears when done", async () => {
    vi.useFakeTimers();
    const r = new RenderStatus();
    r.setProject("cube");
    r.receive({ id: 1, project: "cube", state: "started", session: "s1", client: "Claude Code" });
    expect(r.el.hidden).toBe(false);
    expect(r.busy).toBe(true);
    await vi.advanceTimersByTimeAsync(2000);
    expect(r.el.textContent).toContain("Rendering 3D…");
    expect(r.el.textContent).toContain("by Claude Code · 2 s");

    r.receive({ id: 1, project: "cube", state: "done", version: 3, session: "s1", client: "Claude Code" });
    expect(r.busy).toBe(false);
    expect(r.el.textContent).toContain("Rendered v3");
    await vi.advanceTimersByTimeAsync(4000);
    expect(r.el.hidden).toBe(true);
    vi.useRealTimers();
  });

  test("a failure stays until dismissed", () => {
    const r = new RenderStatus();
    r.setProject("cube");
    r.receive({ id: 6, project: "cube", state: "started" });
    r.receive({ id: 6, project: "cube", state: "failed", error: "OpenSCAD could not compile the model:\nsyntax error" });
    expect(r.el.textContent).toContain("Render failed (from the viewer)");
    expect(r.el.textContent).toContain("syntax error");
    r.el.querySelector("button")!.click();
    expect(r.el.hidden).toBe(true);
  });

  test("another project's render shows by name, and a render seen before its project opened counts", () => {
    const r = new RenderStatus();
    r.receive({ id: 1, project: "new-clip", state: "started", session: "s1", client: "claude-code" });
    expect(r.el.textContent).toContain("Rendering new-clip…");
    expect(r.busy).toBe(false); // no project open yet
    r.setProject("new-clip");
    expect(r.busy).toBe(true);
    expect(r.el.textContent).toContain("Rendering 3D…");
    r.receive({ id: 1, project: "new-clip", state: "done", version: 1 });
    expect(r.el.textContent).toContain("Rendered v1");
    r.receive({ id: 2, project: "other", state: "failed", error: "x" });
    expect(r.el.textContent).not.toContain("failed"); // other projects' failures are theirs
  });

  test("a render whose end was missed stops showing", () => {
    vi.useFakeTimers();
    const r = new RenderStatus();
    r.setProject("cube");
    r.receive({ id: 1, project: "cube", state: "started" });
    vi.advanceTimersByTime(STALE_MS + 2000);
    expect(r.busy).toBe(false);
    expect(r.el.hidden).toBe(true);
    vi.useRealTimers();
  });

  test("names the viewer when no agent session caused it", () => {
    expect(renderedBy({})).toBe("from the viewer");
    expect(renderedBy({ session: "s", client: "" })).toBe("by the agent");
  });
});
