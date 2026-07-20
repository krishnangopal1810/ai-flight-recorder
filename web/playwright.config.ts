import { defineConfig } from "@playwright/test";
import {createHash} from "node:crypto";

const worktreeID = createHash("sha256").update(process.cwd()).digest("hex");
const port = 24000 + Number.parseInt(worktreeID.slice(0, 3), 16) % 1000;

export default defineConfig({
  testDir: "./tests",
	use: {baseURL: `http://127.0.0.1:${port}`, trace: "retain-on-failure", screenshot: "only-on-failure", video: "retain-on-failure"},
	webServer: {command: `npm run dev -- --host 127.0.0.1 --port ${port}`, port, reuseExistingServer: true},
  outputDir: "../.artifacts/playwright",
});
