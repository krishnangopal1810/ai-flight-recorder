import {test, expect} from "@playwright/test";
import {writeFile} from "node:fs/promises";

test.skip(!process.env.FLIGHT_PLAYBACK_E2E || !process.env.FLIGHT_PLAYBACK_CAPSULE_ID, "set the live damaged-playback fixture environment");

test("damaged evidence remains passive, localized, and partially inspectable", async ({page}, testInfo) => {
  const runID = process.env.FLIGHT_PLAYBACK_CAPSULE_ID!;
  await page.addInitScript(token => sessionStorage.setItem("flight_token", token), "frt_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA");
  await page.goto(`/runs/${encodeURIComponent(runID)}/understand`);

  await expect(page.getByText("Safe to inspect", {exact: true})).toBeVisible();
  await page.getByText("Technical proof").click();
  await expect(page.getByText(/0 application processes.*0 outbound requests/i)).toBeVisible();
  const readiness = page.getByRole("region", {name: "This run can’t be replayed yet"});
  await expect(readiness.getByRole("listitem")).toHaveCount(2);
  await expect(readiness.getByText("One required replay detail is missing", {exact: true})).toHaveCount(2);
  await expect(page.getByRole("tree", {name: "Recorded execution timeline"})).toContainText("answer-policy");

  await page.getByRole("button", {name: /policy.lookup tool/i}).click();
  await expect(page.getByLabel("policy.lookup details")).toContainText("missing");
  await expect(page.getByLabel("policy.lookup details")).toContainText("Unaffected evidence remains usable.");
  await page.getByText("Artifacts", {exact: true}).click();
  await expect(page.getByText("sensitive-policy")).toBeVisible();
  await expect(page.getByText("corrupt", {exact: true})).toBeVisible();
  await page.getByText("Model configuration", {exact: true}).click();
  await expect(page.getByText("Model configuration", {exact: true})).toBeVisible();

  const path = testInfo.outputPath("dom.html");
  await writeFile(path, await page.content());
  await testInfo.attach("dom.html", {path, contentType: "text/html"});
});
