import { expect, test } from "@playwright/test";
import { stubApi } from "./stub";

test("the viewer lists open sessions and the client holding each", async ({ page }) => {
  const requests = await stubApi(page);
  await page.goto("/");
  const panel = page.locator(".sessions-panel");
  await expect(panel.getByTestId("session-count")).toHaveText("2");
  const code = panel.locator('[data-session="SESS-CODE"]');
  await expect(code).toContainText("Claude Code 2.1.3");
  await expect(code).toContainText("HTTP");
  await expect(code).toContainText("4 calls");
  const desk = panel.locator('[data-session="SESS-DESK"]');
  await expect(desk).toContainText("claude-desktop 0.14.2");
  await expect(desk).toContainText("stdio bridge");

  // Details: address, user agent and the session's own tool calls.
  await code.getByRole("button", { name: "Calls" }).click();
  await expect(code).toContainText("172.17.0.1:51234");
  await expect(code).toContainText("claude-code/2.1.3");
  await expect(code.locator(".session-runs")).toContainText("compile_model");

  // Follow one session from the panel; the top bar selector agrees.
  await code.getByRole("button", { name: "Follow" }).click();
  await expect(code.getByRole("button", { name: "Following" })).toBeVisible();
  await expect(page.getByTestId("follow")).toHaveValue("session:SESS-CODE");
  await page.getByTestId("follow").selectOption("none");
  await expect(panel).toContainText("not following");

  // End a session.
  page.once("dialog", (d) => d.accept());
  await desk.getByRole("button", { name: "End" }).click();
  await expect.poll(() => requests.some((r) => r.method === "DELETE" && r.url === "/api/sessions/SESS-DESK")).toBe(true);
});
