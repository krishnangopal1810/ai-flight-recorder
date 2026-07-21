import {test, expect} from "@playwright/test";
import {writeFile} from "node:fs/promises";

test.skip(!process.env.FLIGHT_PLAYBACK_E2E || !process.env.FLIGHT_PLAYBACK_CAPSULE_ID, "set the live damaged-playback fixture environment");

test("damaged hosted payloads remain partially inspectable without active execution", async ({page}, testInfo) => {
  const capsuleID = process.env.FLIGHT_PLAYBACK_CAPSULE_ID!;
  await page.addInitScript(token => sessionStorage.setItem("flight_token", token), "frt_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA");
  await page.goto(`/capsules/${encodeURIComponent(capsuleID)}`);
  const isolation = page.getByRole("status", {name: "Playback isolation proof"});
  await expect(isolation).toContainText("0 application processes");
  await expect(isolation).toContainText("0 outbound requests");
  const warnings = page.getByRole("alert", {name: "Playback evidence warnings"});
  await expect(warnings).toContainText("payload_missing");
  await expect(warnings).toContainText("payload_corrupt");
  await expect(page.getByRole("group", {name: "playback completeness"})).toContainText("incomplete");
  await expect(page.getByRole("group", {name: "replay completeness"})).toContainText("blocked");
  await expect(page.getByRole("tree", {name: "Logical execution tree"})).toContainText("answer-policy");
  await expect(page.getByRole("region", {name: "Boundary tool.policy"})).toContainText("substitute");
  await expect(page.getByRole("region", {name: "Artifact evidence"})).toContainText("sensitive-policy");
  await expect(page.getByRole("region", {name: "Model configuration evidence"})).toBeVisible();
  const path = testInfo.outputPath("dom.html");
  await writeFile(path, await page.content());
  await testInfo.attach("dom.html", {path, contentType: "text/html"});
});
