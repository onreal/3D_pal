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
