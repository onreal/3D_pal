import { expect, test } from "@playwright/test";
import { stubApi } from "./stub";

test("toolpaths of a real Bambu Studio .gcode.3mf, with a layer slider", async ({ page }) => {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await stubApi(page);
  await page.goto("/");
  await page.getByTestId("size").waitFor();
  await page.getByRole("tab", { name: "Toolpaths" }).click();
  await expect(page.locator(".toolpaths > p")).toHaveText("100 layers", { timeout: 20_000 });
  expect(await page.evaluate(() => !!customElements.get("gcode-preview"))).toBe(true);
  const slider = page.getByLabel("Show layers up to");
  await expect(slider).toBeEnabled();
  await slider.fill("40");
  await expect(page.locator(".toolpath-controls output")).toHaveText("40 / 100");
  await page.getByLabel("Show travel moves").check();
  await page.getByLabel("Show supports").uncheck();
  expect(errors).toEqual([]);
});
