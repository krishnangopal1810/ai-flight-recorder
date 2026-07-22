import { defineConfig } from "@playwright/test";
import {createHash} from "node:crypto";

const worktreeID = createHash("sha256").update(process.cwd()).digest("hex");
const port = 24000 + Number.parseInt(worktreeID.slice(0, 3), 16) % 1000;
const externalBaseURL = process.env.PLAYWRIGHT_BASE_URL;
const outputDir = process.env.FLIGHT_PLAYWRIGHT_OUTPUT_DIR ?? "../.artifacts/playwright";

export default defineConfig({
  testDir: "./tests",
	use: {baseURL: externalBaseURL ?? `http://127.0.0.1:${port}`, trace: "on", screenshot: "on", video: "on"},
	webServer: externalBaseURL ? undefined : {command: `npm run dev -- --host 127.0.0.1 --port ${port}`, port, reuseExistingServer: false},
  outputDir,
});
