import { expect, test } from "@playwright/test";
import { estimate, stubApi } from "./stub";

test("the viewer shows the test cube's size", async ({ page }) => {
  await stubApi(page, { size: 20 });
  await page.goto("/");
  await expect(page.getByTestId("size")).toHaveText("20.0 × 20.0 × 20.0 mm");
  await expect(page.getByTestId("over-volume")).toBeHidden();
  await expect(page.getByRole("heading", { name: "Parameters" })).toBeVisible();
  await expect(page.getByLabel("size", { exact: true })).toHaveValue("20");
});

test("a 300 mm part shows the over-volume warning", async ({ page }) => {
  await stubApi(page, { size: 300 });
  await page.goto("/");
  await expect(page.getByTestId("size")).toHaveText("300.0 × 300.0 × 300.0 mm");
  await expect(page.getByTestId("over-volume")).toBeVisible();
  await expect(page.getByTestId("over-volume")).toContainText("Too big for the Bambu Lab A1");
});

test("Approve sends exactly one POST with the job ID", async ({ page }) => {
  const job = { id: "1a2b3c4d", project: "cube", version: 1, printer_id: "a1", approved: false, printed: false, estimate };
  const requests = await stubApi(page, { jobs: [job] });
  await page.goto("/");
  const approve = page.getByTestId("approve");
  await expect(approve).toBeVisible();
  await expect(page.locator(".job-panel")).toContainText("21 min");
  await expect(page.locator(".job-panel")).toContainText("7.4 g");
  await approve.click();
  await expect(page.getByTestId("approved")).toBeVisible();
  const posts = requests.filter((r) => r.method === "POST" && r.url.includes("/approve"));
  expect(posts).toHaveLength(1);
  expect(posts[0]!.url).toBe("/api/jobs/1a2b3c4d/approve");
});

test("estimate button shows time, filament and cost", async ({ page }) => {
  await stubApi(page);
  await page.goto("/");
  await page.getByTestId("estimate").click();
  await expect(page.locator(".estimate-panel")).toContainText("21 min");
  await expect(page.locator(".estimate-panel")).toContainText("7.4 g PLA");
});

test("Print slices the version, then Approve and print sends it, and Print again makes a new job", async ({ page }) => {
  const requests = await stubApi(page);
  await page.goto("/");
  await page.getByTestId("print").click();
  const panel = page.locator(".job-panel");
  await expect(panel).toContainText("Waiting for your approval");
  await expect(panel).toContainText("requested by you");
  await expect(panel).toContainText("21 min");
  await page.getByTestId("approve-print").click();
  await expect(page.getByText("Printing on Workshop A1.")).toBeVisible();
  await expect(panel).toContainText("Sent to the printer.");
  const posts = requests.filter((r) => r.method === "POST").map((r) => r.url);
  expect(posts).toEqual(["/api/projects/cube/versions/1/job", "/api/jobs/5e5e5e01/approve", "/api/jobs/5e5e5e01/print"]);
  expect(JSON.parse(requests.find((r) => r.url.endsWith("/job"))!.body!)).toEqual({ printer_id: "a1" });

  await page.getByTestId("print-again").click();
  await expect(panel).toContainText("Waiting for your approval");
  await expect(panel).toContainText("5e5e5e02");
});

test("an approved job can be sent from the viewer", async ({ page }) => {
  const job = { id: "1a2b3c4d", project: "cube", version: 1, printer_id: "a1", approved: true, printed: false, estimate };
  const requests = await stubApi(page, { jobs: [job] });
  await page.goto("/");
  await page.getByTestId("send").click();
  await expect(page.locator(".job-panel")).toContainText("Sent to the printer.");
  expect(requests.filter((r) => r.method === "POST").map((r) => r.url)).toEqual(["/api/jobs/1a2b3c4d/print"]);
});

test("a refused send stays in the panel with Try again", async ({ page }) => {
  const job = { id: "1a2b3c4d", project: "cube", version: 1, printer_id: "a1", approved: true, printed: false, estimate };
  await stubApi(page, { jobs: [job] });
  await page.route("**/api/jobs/1a2b3c4d/print", (route) => route.fulfill({ status: 502, contentType: "application/json",
    body: JSON.stringify({ error: "printer refused the command: … unless Developer Mode is on …" }) }));
  await page.goto("/");
  await page.getByTestId("send").click();
  await expect(page.getByTestId("send-error")).toContainText("Developer Mode");
  await expect(page.getByTestId("send")).toHaveText("Try again");
});

test("no send button while the printer is printing", async ({ page }) => {
  const approved = { id: "1a2b3c4d", project: "cube", version: 1, printer_id: "a1", approved: true, printed: false, estimate };
  await stubApi(page, { jobs: [approved], printingLayer: 37 });
  await page.goto("/");
  await expect(page.getByTestId("printer-busy")).toContainText("Workshop A1 is printing. Send this job when the print is done.");
  await expect(page.getByTestId("send")).toHaveCount(0);
  await expect(page.locator(".job-panel")).not.toContainText("Send it now");
});

test("a job for another version stays out of the panel", async ({ page }) => {
  const job = { id: "2b2b2b2b", project: "cube", version: 2, printer_id: "a1", approved: true, printed: false, estimate };
  await stubApi(page, { jobs: [job] });
  await page.goto("/");
  await expect(page.getByTestId("size")).toBeVisible();
  await expect(page.locator(".job-panel")).toBeHidden();
});

test("Discard drops an approved job and Print makes a new one", async ({ page }) => {
  const job = { id: "3c3c3c3c", project: "cube", version: 1, printer_id: "a1", approved: true, printed: false, estimate };
  const requests = await stubApi(page, { jobs: [job] });
  await page.goto("/");
  await page.getByTestId("discard").click();
  await expect(page.locator(".job-panel")).toBeHidden();
  expect(requests.filter((r) => r.method === "POST" && r.url.endsWith("/discard")).map((r) => r.url)).toEqual(["/api/jobs/3c3c3c3c/discard"]);
  await page.getByTestId("print").click();
  await expect(page.getByTestId("approve-print")).toBeVisible();
});

test("a printer whose status channel is down shows Status unavailable, not Offline, and no send button", async ({ page }) => {
  const approved = { id: "1a2b3c4d", project: "cube", version: 1, printer_id: "a1", approved: true, printed: false, estimate };
  await stubApi(page, { jobs: [approved], statusDown: true });
  await page.goto("/");
  await expect(page.getByTestId("printer-state")).toHaveText("Status unavailable");
  await expect(page.getByTestId("printer-state")).toHaveAttribute("title", /status service \(MQTT/);
  await expect(page.getByTestId("printer-busy")).toContainText("Workshop A1 isn't reporting its status, so it may be printing.");
  await expect(page.getByTestId("send")).toHaveCount(0);
});

test("changing a parameter renders nothing until Render 3D, which renders once", async ({ page }) => {
  const requests = await stubApi(page);
  await page.goto("/");
  const render = page.getByTestId("render-3d");
  await expect(render).toBeDisabled();
  await page.getByLabel("size", { exact: true }).fill("30");
  await page.getByLabel("size", { exact: true }).fill("35");
  await expect(page.getByTestId("params-status")).toContainText("Not rendered yet: size");
  await page.waitForTimeout(300);
  const posts = () => requests.filter((r) => r.method === "POST" && r.url === "/api/projects/cube/params");
  expect(posts()).toHaveLength(0);
  await render.click();
  await expect(render).toContainText("Rendering…");
  await expect(page.getByTestId("params-status")).toContainText("Version 2 rendered");
  expect(posts()).toHaveLength(1);
  expect(JSON.parse(posts()[0]!.body!)).toEqual({ params: { size: 35 } });
});

test("a render by the agent shows on the viewer, with who started it", async ({ page }) => {
  await stubApi(page, { events: [{ type: "render", data: { id: 7, project: "cube", state: "started", session: "S1", client: "claude-code" } }] });
  await page.goto("/");
  const banner = page.getByTestId("render-status");
  await expect(banner).toBeVisible();
  await expect(banner).toContainText("Rendering 3D…");
  await expect(banner).toContainText("by claude-code");
  await expect(banner).toContainText("The model below is the previous version.");
  await expect(page.getByTestId("viewer")).toHaveClass(/rendering/);
});
