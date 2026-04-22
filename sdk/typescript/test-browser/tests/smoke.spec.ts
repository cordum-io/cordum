import { test, expect } from "@playwright/test";

test("loads jobs and stream events in the browser harness", async ({ page }) => {
  await page.goto("/");
  await expect(page.locator("#jobs")).toContainText("job-browser-1");
  await expect(page.locator("#stream")).toContainText("job.status_changed");
  await expect(page.locator("#stream")).toContainText("running");
});
