import type {Playback} from "./api";

export type ReplayIssue = NonNullable<Playback["completeness"]["replay"]["missing"]>[number];

export interface ReplayIssueExplanation {
  title: string;
  summary: string;
  why: string;
  fix: string;
  operationID?: string;
  operationName?: string;
  technicalCode: string;
  technicalMessage: string;
}

export function explainReplayIssue(playback: Playback, issue: ReplayIssue): ReplayIssueExplanation {
  const boundary = issue.boundary_id ? playback.capsule.boundaries?.find(item => item.id === issue.boundary_id) : undefined;
  const operation = boundary ? playback.capsule.operations.find(item => item.span_id === boundary.operation_span_id) : undefined;
  const operationName = operation?.name || boundary?.logical_key;
  const call = operationName ? `“${operationName}”` : "a recorded call";
  const dependency = boundary?.class === "llm" ? "model" : boundary?.class === "retriever" ? "retriever" : "tool";
  const base = {
    operationID: operation?.span_id,
    operationName,
    technicalCode: issue.code,
    technicalMessage: issue.message,
  };

  if (issue.code === "boundary_response_missing" || issue.code === "boundary_response_truncated") {
    const limited = issue.code.endsWith("truncated");
    return {
      ...base,
      title: `The output from ${call} was not fully recorded`,
      summary: limited ? `${call} produced an output, but the recording kept only part of it.` : `${call} ran, but this recording does not contain its output.`,
      why: `Replay must return that recorded output instead of contacting the real ${dependency}. Without it, Flight Recorder stops rather than making an unexpected live call.`,
      fix: limited ? `Increase the capture limit for ${call}, then record the agent again.` : `Record the agent again and make sure ${call} captures its result. If this call is expected to fail, capture the failure as a structured output.`,
    };
  }

  if (issue.code === "boundary_request_missing" || issue.code === "boundary_request_omitted" || issue.code === "boundary_request_truncated") {
    return {
      ...base,
      title: `The input to ${call} was not fully recorded`,
      summary: `Flight Recorder cannot identify the matching ${dependency} call from this recording.`,
      why: `Replay uses the recorded input to match this call exactly. Guessing could substitute the wrong response.`,
      fix: `Capture the complete input to ${call}, then record the agent again. Review capture exclusions or size limits if they removed it.`,
    };
  }

  if (issue.code === "entrypoint_missing") return {
    ...base,
    title: "The command that starts this agent was not recorded",
    summary: "Flight Recorder can show the run, but it does not know how to start the agent inside an isolated replay.",
    why: "Replay needs the exact executable and arguments so it starts the same pipeline safely.",
    fix: "Register the agent entrypoint before the run starts, then record the run again.",
  };

	if (issue.code === "root_input_missing" || issue.code === "root_input_omitted" || issue.code === "root_input_truncated") return {
    ...base,
    title: "The agent’s starting input was not fully recorded",
    summary: "The original request cannot be supplied to the replayed agent.",
    why: "Replay needs the same starting input to reproduce the pipeline.",
    fix: "Allow the root input through the capture policy, then record the run again.",
  };

	if (issue.code === "boundary_declaration_missing") return {
		...base,
		title: "An external call has no replay boundary",
		summary: "Flight Recorder saw a model, retriever, tool, API, or MCP operation that has no declared replay policy.",
		why: "Without a boundary, replay cannot safely decide whether to substitute, block, or allow the dependency.",
		fix: "Wrap the call with the Python or TypeScript boundary helper, choose a stable logical key and replay policy, then record again.",
	};

  if (issue.code === "tool_definition_omitted" || issue.code === "tool_definition_truncated") return {
    ...base,
    title: "The model’s available tools were not fully recorded",
    summary: "Flight Recorder cannot reconstruct which tools the model was allowed to call.",
    why: "Changing the tool list can change model behavior before any tool is called.",
    fix: "Capture the complete model tool definitions, then record the run again.",
  };

  if (issue.code === "boundary_match_ambiguous") return {
    ...base,
    title: `${call} cannot be matched to one recorded call`,
    summary: "Two or more calls have the same replay identity.",
    why: "Flight Recorder stops rather than guessing which recorded response to use.",
    fix: `Give each repeated call a stable, distinct logical name or parent, then record the run again.`,
  };

  if (issue.code === "operations_missing") return {
    ...base,
    title: "No agent operations were recorded",
    summary: "The recording contains no model, tool, retriever, or agent steps to replay.",
    why: "Replay needs an execution timeline and recorded dependencies.",
    fix: "Check that the agent exports its trace before exiting, then record it again.",
  };

  return {
    ...base,
    title: "One required replay detail is missing",
    summary: "This recording is still safe to inspect, but it is not complete enough for an isolated replay.",
    why: "Flight Recorder stops when required evidence is unavailable instead of guessing or calling a dependency live.",
    fix: "Review the affected recording setup, then capture the run again.",
  };
}
