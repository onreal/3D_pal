import { describe, expect, test } from "vitest";
import { clientLabel, decodeFollow, encodeFollow, follows } from "./follow";

describe("following sessions", () => {
  test("all follows every agent event but never the viewer's own", () => {
    expect(follows({ mode: "all" }, { session: "a" })).toBe(true);
    expect(follows({ mode: "all" }, {})).toBe(false);
  });
  test("one session follows only that session", () => {
    const f = { mode: "session" as const, id: "a" };
    expect(follows(f, { session: "a" })).toBe(true);
    expect(follows(f, { session: "b" })).toBe(false);
  });
  test("one client follows all its connections", () => {
    const f = { mode: "client" as const, client: "Claude-Code" };
    expect(follows(f, { session: "a", client: "claude-code" })).toBe(true);
    expect(follows(f, { session: "b", client: "CLAUDE-CODE" })).toBe(true);
    expect(follows(f, { session: "c", client: "other" })).toBe(false);
  });
  test("none never switches", () => {
    expect(follows({ mode: "none" }, { session: "a" })).toBe(false);
  });
  test("round trip through storage", () => {
    for (const f of [{ mode: "all" as const }, { mode: "none" as const }, { mode: "session" as const, id: "XYZ" }, { mode: "client" as const, client: "claude-code" }]) {
      expect(decodeFollow(encodeFollow(f))).toEqual(f);
    }
    expect(decodeFollow("garbage")).toEqual({ mode: "all" });
    expect(decodeFollow("session:")).toEqual({ mode: "all" });
  });
  test("client labels prefer the title", () => {
    expect(clientLabel({ client: "claude-code", client_title: "Claude Code", client_version: "2.1" })).toBe("Claude Code 2.1");
    expect(clientLabel({ client: "claude-desktop", client_version: "" })).toBe("claude-desktop");
  });
});
