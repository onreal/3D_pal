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
