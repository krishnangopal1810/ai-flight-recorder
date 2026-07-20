import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { App } from "./App";

afterEach(() => vi.restoreAllMocks());

it("renders an agent-legible empty state", async () => {
  vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify({items: []}), {status: 200, headers: {"Content-Type": "application/json"}})));
  render(<QueryClientProvider client={new QueryClient()}><App /></QueryClientProvider>);
  expect(await screen.findByText("No capsules recorded yet.")).toBeInTheDocument();
  expect(screen.getByText("Playback executes nothing and makes no outbound requests.")).toBeInTheDocument();
});
