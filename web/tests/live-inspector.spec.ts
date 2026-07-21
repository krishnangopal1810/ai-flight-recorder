import {test, expect} from "@playwright/test";
import {writeFile} from "node:fs/promises";

test.skip(!process.env.FLIGHT_LIVE_E2E, "set FLIGHT_LIVE_E2E=1 and PLAYWRIGHT_BASE_URL to exercise a running stack");

test("a real failed agent run leads directly to its controlled candidate", async ({page}, testInfo) => {
  await page.addInitScript(token => sessionStorage.setItem("flight_token", token), "frt_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA");
  await page.goto("/runs?project=refund-operations");

  await expect(page.getByRole("heading", {name: "Recorded runs"})).toBeVisible();
  const failedRun = page.getByRole("button", {name: /Failed.*refund-review-agent.*Replay ready.*\d+ candidates?/i});
  await expect(failedRun).toContainText("Failed at refund-decision");
  await failedRun.click();

  await expect(page.getByRole("heading", {name: "Run failed during refund-decision"})).toBeVisible();
  await expect(page.getByText("Safe to inspect")).toBeVisible();
  await expect(page.getByRole("tree", {name: "Recorded execution timeline"})).toContainText("refund-decision");
  await page.getByRole("button", {name: "Review first important change"}).first().click();

  await expect(page.getByRole("heading", {name: "What changed after the replay?"})).toBeVisible();
  const graph = page.getByRole("figure", {name: "Where the replay split"});
  await expect(graph).toBeVisible();
  await expect(graph).toContainText("refund-decision");
  await expect(graph).toContainText("First change");
  await expect(graph).toContainText("Recorded run");
  await expect(graph).toContainText("Candidate replay");
  await expect(page.getByText("Changed fields", {exact: true})).toBeVisible();
  await expect(page.getByRole("heading", {name: "Candidate no longer reproduces the incident"})).toBeVisible();
  await expect(page.getByText("Possible explanations", {exact: true})).toHaveCount(0);

  const screenshotPath = testInfo.outputPath("comparison.png");
  await page.screenshot({path: screenshotPath, fullPage: true});
  await testInfo.attach("comparison.png", {path: screenshotPath, contentType: "image/png"});
  const path = testInfo.outputPath("dom.html");
  await writeFile(path, await page.content());
  await testInfo.attach("dom.html", {path, contentType: "text/html"});
});
