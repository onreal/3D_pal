import { expect, test } from "@playwright/test";
import { stubApi } from "./stub";

test("settings mask the access code, lock approval on, and save with PUT", async ({ page }) => {
  const requests = await stubApi(page);
  await page.goto("/settings");
  await expect(page.getByRole("heading", { name: "Settings" })).toBeVisible();
  const code = page.locator('input[type="password"]');
  await expect(code).toHaveValue("••••••••");
  const approval = page.getByText("Approval required before printing").locator("input");
  await expect(approval).toBeChecked();
  await expect(approval).toBeDisabled();
  await page.getByLabel("Printer name").fill("Garage A1");
  await page.getByTestId("save").click();
  await expect(page.getByText("Settings saved.")).toBeVisible();
  const put = requests.find((r) => r.method === "PUT" && r.url === "/api/settings");
  expect(put).toBeTruthy();
  const body = JSON.parse(put!.body!);
  expect(body.printers[0].name).toBe("Garage A1");
  expect(body.printers[0].config.access_code).toBe("••••••••"); // the mask keeps the stored code
  expect(body.pipeline.approval_required).toBe(true);
});

test("adding a printer builds its form from the driver's fields", async ({ page }) => {
  await stubApi(page);
  await page.goto("/settings");
  await page.getByLabel("Printer kind").selectOption("fake");
  await page.getByRole("button", { name: "Add printer" }).click();
  await expect(page.getByText("Simulated print time (seconds)")).toBeVisible();
});
