import { test, expect } from "@playwright/test";
import {writeFile} from "node:fs/promises";

test.skip(!process.env.FLIGHT_LIVE_E2E, "set FLIGHT_LIVE_E2E=1 and PLAYWRIGHT_BASE_URL to exercise a running stack");

test("live worktree stack exposes seeded playback and divergence evidence", async ({page}, testInfo) => {
	await page.addInitScript(token => sessionStorage.setItem("flight_token", token), "frt_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA");
	await page.goto("/");
	await expect(page.getByRole("heading", {name: "AI Flight Recorder"})).toBeVisible();
	await expect(page.getByText("2", {exact: true})).toBeVisible();
	const capsules = page.getByRole("button", {name: /policy-agent/});
	await expect(capsules).toHaveCount(2);
	await capsules.first().click();
	await expect(page.getByRole("tree", {name: "Logical execution tree"})).toContainText("answer-policy");
	await expect(page.getByRole("group", {name: "replay completeness"})).toContainText("ready");
	await page.getByLabel("Candidate capsule").selectOption({index: 1});
	await expect(page.getByRole("region", {name: "First divergence"})).toContainText("Observation");
	const path = testInfo.outputPath("dom.html");
	await writeFile(path, await page.content());
	await testInfo.attach("dom.html", {path, contentType: "text/html"});
});
