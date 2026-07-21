import {QueryClient, QueryClientProvider} from "@tanstack/react-query";
import {fireEvent, render, screen} from "@testing-library/react";
import {afterEach, expect, it, vi} from "vitest";
import {App} from "./App";

afterEach(() => {
  vi.restoreAllMocks();
  sessionStorage.clear();
  localStorage.clear();
  history.replaceState({}, "", "/runs");
});

it("turns a missing token into connection controls instead of a backend error", async () => {
  stubFetch({"/api/v1/session": {schema_version: "1.0", service_version: "1.0", auth_mode: "token", connected: false, local_mode_available: false, scopes: []}});
  renderApp();
  expect(await screen.findByRole("heading", {name: "Connect to your Flight Recorder"})).toBeInTheDocument();
  expect(screen.getByLabelText("Service URL")).toBeInTheDocument();
  expect(screen.getByLabelText("Access token")).toBeInTheDocument();
  expect(screen.queryByRole("button", {name: /demo/i})).not.toBeInTheDocument();
  expect(screen.queryByText("exactly one bearer token is required")).not.toBeInTheDocument();
});

it("shows an in-product recording quickstart for a connected empty store", async () => {
  stubFetch({
    "/api/v1/session": {schema_version: "1.0", service_version: "1.0", auth_mode: "token", connected: true, local_mode_available: false, scopes: ["read", "replay"]},
    "/api/v1/capsules": {items: []},
  });
  renderApp();
  expect(await screen.findByRole("heading", {name: "No recorded runs yet"})).toBeInTheDocument();
  expect(screen.getByRole("button", {name: "Add your first run"})).toBeInTheDocument();
  expect(screen.queryByText(/0 recorded capsules/i)).not.toBeInTheDocument();
});

it("guides a user by setup and explains the tracing prerequisite before showing commands", async () => {
  history.replaceState({}, "", "/record");
  stubFetch({
    "/api/v1/session": {schema_version: "1.0", service_version: "1.0", auth_mode: "token", connected: true, local_mode_available: false, scopes: ["read", "replay", "ingest"]},
    "/api/v1/capsules": {items: []},
  });
  renderApp();
  expect(await screen.findByRole("heading", {name: "See what your agent did"})).toBeInTheDocument();
  expect(screen.getByRole("button", {name: /On this computer/})).toHaveAttribute("aria-pressed", "true");
  expect(screen.getByRole("button", {name: /Already running/})).toBeInTheDocument();
  expect(screen.getByRole("button", {name: /A recording file/})).toBeInTheDocument();
  expect(screen.getByRole("heading", {name: "Does this agent already create traces?"})).toBeInTheDocument();
  expect(screen.queryByLabelText("Your agent command")).not.toBeInTheDocument();

  fireEvent.click(screen.getByRole("button", {name: "Yes, tracing is set up"}));
  expect(screen.getByLabelText("Your agent command")).toHaveValue("python agent.py");
  expect(screen.getByText(/flight --url .* record -- python agent\.py/)).toBeInTheDocument();
  expect(screen.getByText("Need to change a model or tool value and replay this run?")).toBeInTheDocument();
  expect(await screen.findByRole("button", {name: "Start listening"})).toBeEnabled();

  fireEvent.click(screen.getByRole("button", {name: /Already running/}));
  expect(screen.getByRole("heading", {name: "Connect an agent that already sends traces"})).toBeInTheDocument();
  expect(screen.getByLabelText("Agent or project name")).toHaveValue("my-agent");

  fireEvent.click(screen.getByRole("button", {name: /A recording file/}));
  expect(screen.getByRole("heading", {name: "Import a recorded run"})).toBeInTheDocument();
  expect(screen.getByText(/complete and has not been changed or damaged/)).toBeInTheDocument();
  expect(screen.getByRole("heading", {name: "Download one from any recorded run"})).toBeInTheDocument();
  expect(screen.queryByText(/digest tree/i)).not.toBeInTheDocument();
});

it("turns an incompatible service into a typed connection recovery", async () => {
  stubFetch({"/api/v1/session": {schema_version: "2.0", service_version: "2.0", auth_mode: "token", connected: true, local_mode_available: false, scopes: ["read"]}});
  renderApp();
  expect(await screen.findByText("This inspector and Flight Recorder service use incompatible API versions.")).toBeInTheDocument();
  expect(screen.getByText("Connect to a Flight Recorder service with API major version 1.")).toBeInTheDocument();
});

it("keeps the job ID recovery control secondary and collapsed", async () => {
  history.replaceState({}, "", "/jobs");
  stubFetch({
    "/api/v1/session": {schema_version: "1.0", service_version: "1.0", auth_mode: "token", connected: true, local_mode_available: false, scopes: ["read"]},
    "/api/v1/jobs": {items: []},
  });
  renderApp();
  const summary = await screen.findByText("Open a job by ID");
  expect(screen.queryByLabelText("Job ID")).not.toBeVisible();
  fireEvent.click(summary);
  expect(screen.getByLabelText("Job ID")).toBeVisible();
});

function renderApp() {
  const client = new QueryClient({defaultOptions: {queries: {retry: false}}});
  return render(<QueryClientProvider client={client}><App /></QueryClientProvider>);
}

function stubFetch(routes: Record<string, unknown>) {
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
    const url = new URL(String(input), "http://localhost");
    const key = Object.keys(routes).find(path => url.pathname === path);
    if (!key) return new Response(JSON.stringify({error: {message: `Unhandled ${url.pathname}`}}), {status: 500, headers: {"Content-Type": "application/json"}});
    return new Response(JSON.stringify(routes[key]), {status: 200, headers: {"Content-Type": "application/json"}});
  }));
}
