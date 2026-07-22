import {QueryClient, QueryClientProvider} from "@tanstack/react-query";
import {fireEvent, render, screen} from "@testing-library/react";
import {expect, it, vi} from "vitest";
import type {Playback} from "./api";
import {TimelineWorkspace} from "./TimelineWorkspace";

it("shows span and run logs while loading bodies only after disclosure", async () => {
  Object.defineProperty(window, "matchMedia", {configurable: true, value: vi.fn(() => ({matches: true, addEventListener: vi.fn(), removeEventListener: vi.fn()}))});
  const fetchMock = vi.fn(async () => new Response(JSON.stringify("policy started"), {status: 200, headers: {"Content-Type": "application/json"}}));
  vi.stubGlobal("fetch", fetchMock);
  const playback = {
    capsule: {
      capsule_id: "afr_logs",
      operations: [{span_id: "span-1", kind: "agent", name: "answer", start_time: "2026-01-01T00:00:00Z", end_time: "2026-01-01T00:00:01Z", status: "ok", sequence: 0}],
      logs: [
        {id: `log_${"a".repeat(64)}`, span_id: "span-1", timestamp: "2026-01-01T00:00:00.100Z", severity_number: 9, severity_text: "INFO", event_name: "agent.started", body: {digest: `sha256:${"b".repeat(64)}`, size: 16, state: "captured", media_type: "application/json"}},
        {id: `log_${"c".repeat(64)}`, timestamp: "2026-01-01T00:00:00.200Z", severity_number: 13, severity_text: "WARN", event_name: "run.notice"},
      ],
      boundaries: [],
    },
    payloads: [{digest: `sha256:${"b".repeat(64)}`, availability: "available", size: 16, references: ["logs[0].body"], media_type: "application/json"}],
  } as unknown as Playback;
  const client = new QueryClient({defaultOptions: {queries: {retry: false}}});
  render(<QueryClientProvider client={client}><TimelineWorkspace playback={playback} /></QueryClientProvider>);

  expect(screen.getByRole("heading", {name: "Run logs"})).toBeInTheDocument();
  expect(screen.getByRole("heading", {name: "Logs"})).toBeInTheDocument();
  expect(screen.getByText("agent.started")).toBeInTheDocument();
  expect(screen.getByText(/1 log/)).toBeInTheDocument();
  expect(fetchMock).not.toHaveBeenCalled();

  fireEvent.click(screen.getByRole("button", {name: "View recorded value"}));
  expect(await screen.findByText(/policy started/)).toBeInTheDocument();
  expect(fetchMock).toHaveBeenCalledTimes(1);
});
