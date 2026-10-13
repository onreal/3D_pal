import { expect, test } from "@playwright/test";
import { stubApi } from "./stub";

test("toolpaths of a real Bambu Studio .gcode.3mf, with a layer slider", async ({ page }) => {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await stubApi(page);
  await page.goto("/");
  await page.getByTestId("size").waitFor();
  await page.getByRole("tab", { name: "Toolpaths" }).click();
  await expect(page.locator(".toolpaths > p[role=status]")).toHaveText("100 layers", { timeout: 20_000 });
  expect(await page.evaluate(() => !!customElements.get("gcode-preview"))).toBe(true);
  const slider = page.getByLabel("Show layers up to");
  await expect(slider).toBeEnabled();
  await slider.fill("40");
  await expect(page.locator(".toolpath-controls output")).toHaveText("40 / 100");
  await page.getByLabel("Show travel moves").check();
  await page.getByLabel("Show supports").uncheck();
  expect(errors).toEqual([]);
});

test("Watch follows the running print: layers so far, live status, and following stops on a slider move", async ({ page }) => {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  const requests = await stubApi(page, { printingLayer: 37 });
  await page.goto("/");
  await page.getByTestId("size").waitFor();
  await page.getByTestId("watch-print").click();
  await expect(page.getByRole("tab", { name: "Toolpaths" })).toHaveAttribute("aria-selected", "true");
  await expect(page.getByTestId("follow-print")).toBeChecked();
  await expect(page.getByTestId("follow-live")).toHaveText("Printing · layer 37 / 100 · 37% · about 1 h 05 min left", { timeout: 20_000 });
  await expect(page.locator(".toolpath-controls output")).toHaveText("37 / 100");
  // The file shown is the job the printer runs, not a fresh slice of the version.
  expect(requests.some((r) => r.url === "/api/projects/cube/job.gcode.3mf?job=1a2b3c4d")).toBe(true);
  expect(requests.some((r) => r.url.startsWith("/api/projects/cube/versions/1/job.gcode.3mf"))).toBe(false);

  await page.getByLabel("Show layers up to").fill("80");
  await expect(page.getByTestId("follow-print")).not.toBeChecked();
  await expect(page.getByTestId("follow-live")).toBeHidden();
  await expect(page.locator(".toolpath-controls output")).toHaveText("80 / 100");
  expect(errors).toEqual([]);
});
