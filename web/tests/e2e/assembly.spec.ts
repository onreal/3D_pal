import { expect, test } from "@playwright/test";
import { stubApi } from "./stub";

test("assembly mode adds the parts one by one along a timeline", async ({ page }) => {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await stubApi(page);
  await page.goto("/");
  await page.getByTestId("size").waitFor();
  await page.getByRole("tab", { name: "Assembly" }).click();

  const steps = page.getByTestId("assembly-steps");
  await expect(steps.locator(".assembly-step")).toHaveCount(3);
  await expect(steps).toContainText("Base plate");
  await expect(steps).toContainText("Pins ×2");
  const label = page.getByTestId("assembly-step");
  await expect(label).toHaveText("3 / 3 · Lid"); // opens on the finished assembly

  await page.getByRole("button", { name: "Start" }).click();
  await expect(label).toHaveText("0 / 3");
  await page.getByRole("button", { name: "Next step" }).click();
  await expect(label).toHaveText("1 / 3 · Base plate");
  await expect(steps.locator(".assembly-step.current")).toContainText("Base plate");

  // Clicking a step jumps to it.
  await steps.getByRole("button", { name: "Pins ×2", exact: true }).click();
  await expect(label).toHaveText("2 / 3 · Pins");

  // Play runs to the end.
  await page.getByRole("button", { name: "Start" }).click();
  await page.getByTestId("assembly-play").click();
  await expect(label).toHaveText("3 / 3 · Lid", { timeout: 10_000 });
  await expect(page.getByTestId("assembly-play")).toHaveText("▶ Play");

  // Reorder: the lid first; the order is kept for the project.
  await steps.getByRole("button", { name: "Move Lid earlier" }).click();
  await steps.getByRole("button", { name: "Move Lid earlier" }).click();
  await expect(steps.locator(".assembly-step").first()).toContainText("Lid");
  await page.reload();
  await page.getByTestId("size").waitFor();
  await page.getByRole("tab", { name: "Assembly" }).click();
  await expect(page.getByTestId("assembly-steps").locator(".assembly-step").first()).toContainText("Lid");
  expect(errors).toEqual([]);
});
