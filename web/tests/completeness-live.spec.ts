import {test, expect} from "@playwright/test";
import {writeFile} from "node:fs/promises";

test.skip(!process.env.FLIGHT_COMPLETENESS_E2E || !process.env.FLIGHT_COMPLETENESS_CAPSULE_ID, "set the live completeness fixture environment");

test("missing tool evidence explains the blocked capability and recovery", async ({page}, testInfo) => {
  const runID = process.env.FLIGHT_COMPLETENESS_CAPSULE_ID!;
  await page.addInitScript(token => sessionStorage.setItem("flight_token", token), "frt_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA");
  await page.goto(`/runs/${encodeURIComponent(runID)}/understand`);

  await expect(page.getByRole("heading", {name: "This run can’t be replayed yet"})).toBeVisible();
  await expect(page.getByText(/was not fully recorded/i).first()).toBeVisible();
  await expect(page.getByText(/record the agent again/i).first()).toBeVisible();
  await expect(page.getByText("You can still inspect its timeline and all unaffected recorded evidence.")).toBeVisible();
  await page.getByText("View complete capability matrix").click();
  await expect(page.getByText("Playback", {exact: true})).toBeVisible();
  await expect(page.getByText("Diff", {exact: true})).toBeVisible();

  const path = testInfo.outputPath("dom.html");
  await writeFile(path, await page.content());
  await testInfo.attach("dom.html", {path, contentType: "text/html"});
});
