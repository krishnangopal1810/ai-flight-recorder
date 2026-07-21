import {test, expect} from "@playwright/test";
import {writeFile} from "node:fs/promises";

test.skip(!process.env.FLIGHT_COMPLETENESS_E2E || !process.env.FLIGHT_COMPLETENESS_CAPSULE_ID, "set the live completeness fixture environment");

test("missing tool response is explicit in every affected mode", async ({page}, testInfo) => {
	const capsuleID = process.env.FLIGHT_COMPLETENESS_CAPSULE_ID!;
	await page.addInitScript(token => sessionStorage.setItem("flight_token", token), "frt_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA");
	await page.goto(`/capsules/${encodeURIComponent(capsuleID)}`);
	await expect(page.getByRole("group", {name: "playback completeness"})).toContainText("ready");
	const replay = page.getByRole("group", {name: "replay completeness"});
	await expect(replay).toContainText("blocked");
	await expect(replay).toContainText("boundary_response_missing");
	await expect(replay).toContainText(/boundary_.*0302/);
	await expect(page.getByRole("group", {name: "diff completeness"})).toContainText("ready");
	await expect(page.getByRole("group", {name: "bisect completeness"})).toContainText("blocked");
	const path = testInfo.outputPath("dom.html");
	await writeFile(path, await page.content());
	await testInfo.attach("dom.html", {path, contentType: "text/html"});
});
