import { afterEach, describe, expect, it, vi } from "vitest";
import { createServer, Server } from "node:http";
import { AddressInfo } from "node:net";
import { mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import OpenAI from "openai";
import { AIMessage, AIMessageChunk, HumanMessage } from "@langchain/core/messages";
import { Document } from "@langchain/core/documents";
import { testing } from "./index.js";
import { flightRecorderMiddleware, wrapChatModel, wrapRetriever, wrapTool } from "./langchain.js";
import { wrapOpenAI } from "./openai.js";

const response = {
	id: "resp_flight",
	object: "response",
	created_at: 1,
	status: "completed",
	error: null,
	incomplete_details: null,
	instructions: null,
	max_output_tokens: null,
	model: "gpt-flight",
	output: [],
	parallel_tool_calls: true,
	previous_response_id: null,
	reasoning: {effort: null, summary: null},
	store: true,
	temperature: 1,
	text: {format: {type: "text"}},
	tool_choice: "auto",
	tools: [],
	top_p: 1,
	truncation: "disabled",
	usage: {input_tokens: 2, input_tokens_details: {cached_tokens: 0, cache_write_tokens: 0}, output_tokens: 1, output_tokens_details: {reasoning_tokens: 0}, total_tokens: 3},
};

afterEach(() => {
	delete process.env.FLIGHT_REPLAY_PLAN;
	delete process.env.FLIGHT_PROJECT_ID;
	delete process.env.FLIGHT_CAPTURE;
	testing.reset();
});

async function localOpenAIServer(): Promise<{server: Server; baseURL: string; calls: string[]}> {
	const calls: string[] = [];
	const server = createServer((request, result) => {
		calls.push(request.url ?? "");
		const chunks: Buffer[] = [];
		request.on("data", chunk => chunks.push(chunk));
		request.on("end", () => {
			const body = JSON.parse(Buffer.concat(chunks).toString() || "{}") as {stream?: boolean};
			if (body.stream) {
				const first = {id: "chat_flight", object: "chat.completion.chunk", created: 1, model: "gpt-flight", choices: [{index: 0, delta: {role: "assistant", content: "hel"}, finish_reason: null, logprobs: null}]};
				const second = {id: "chat_flight", object: "chat.completion.chunk", created: 1, model: "gpt-flight", choices: [{index: 0, delta: {content: "lo"}, finish_reason: "stop", logprobs: null}]};
				result.writeHead(200, {"content-type": "text/event-stream"});
				result.end(`data: ${JSON.stringify(first)}\n\ndata: ${JSON.stringify(second)}\n\ndata: [DONE]\n\n`);
				return;
			}
			const encoded = JSON.stringify(response);
			result.writeHead(200, {"content-type": "application/json", "content-length": Buffer.byteLength(encoded)});
			result.end(encoded);
		});
	});
	await new Promise<void>(resolve => server.listen(0, "127.0.0.1", resolve));
	const address = server.address() as AddressInfo;
	return {server, baseURL: `http://127.0.0.1:${address.port}/v1`, calls};
}

function replayPlan(request: unknown, output: unknown, logicalKey: string): string {
	const directory = mkdtempSync(join(tmpdir(), "flight-integrations-"));
	const filename = join(directory, "plan.json");
	writeFileSync(filename, JSON.stringify({substitutions: [logicalKey], boundaries: [{logical_key: logicalKey, boundary_class: "llm", occurrence: 0, request_digest: testing.digest(request), response: output}]}));
	return filename;
}

describe("OpenAI integration", () => {
	it("uses the official client against a local endpoint", async () => {
		const fixture = await localOpenAIServer();
		try {
			const client = wrapOpenAI(new OpenAI({apiKey: "test", baseURL: fixture.baseURL}));
			const result = await client.responses.create({model: "gpt-flight", input: "hello"});
			const stream = await client.chat.completions.create({model: "gpt-flight", messages: [{role: "user", content: "hello"}], stream: true});
			const content = [];
			for await (const chunk of stream) content.push(chunk.choices[0]?.delta.content);
			expect(result.id).toBe("resp_flight");
			expect(content).toEqual(["hel", "lo"]);
			expect(fixture.calls).toEqual(["/v1/responses", "/v1/chat/completions"]);
		} finally { await new Promise<void>(resolve => fixture.server.close(() => resolve())); }
	});

	it("replays structural response and ordered stream events without network", async () => {
		const parameters = {model: "gpt-flight", input: "hello"};
		const request = {schema_version: "1", integration: "openai", operation: "responses.create", parameters};
		process.env.FLIGHT_REPLAY_PLAN = replayPlan(request, {schema_version: "1", integration: "openai", kind: "response", surface: "responses", value: response}, "openai.responses.create");
		const client = wrapOpenAI(new OpenAI({apiKey: "test", baseURL: "http://127.0.0.1:1/v1"}));
		const result = await client.responses.create(parameters);
		expect(result.id).toBe("resp_flight");

		testing.reset();
		const streamParameters = {model: "gpt-flight", messages: [{role: "user" as const, content: "hello"}], stream: true as const};
		const streamRequest = {schema_version: "1", integration: "openai", operation: "chat.completions.create", parameters: streamParameters};
		const values = ["hel", "lo"].map((content, index) => ({id: "chat_flight", object: "chat.completion.chunk", created: 1, model: "gpt-flight", choices: [{index: 0, delta: {content}, finish_reason: index ? "stop" : null, logprobs: null}]}));
		const envelope = {schema_version: "1", kind: "stream", events: values.map(value => ({type: "ChatCompletionChunk", payload: {integration: "openai", surface: "chat.completions", value}})), termination: {kind: "completed"}};
		process.env.FLIGHT_REPLAY_PLAN = replayPlan(streamRequest, envelope, "openai.chat.completions.create");
		const stream = await client.chat.completions.create(streamParameters);
		const content: Array<string | null | undefined> = [];
		for await (const chunk of stream) content.push(chunk.choices[0]?.delta.content);
		expect(content).toEqual(["hel", "lo"]);
	});
});

describe("LangChain integration", () => {
	it("deduplicates a nested wrapped OpenAI call", async () => {
		const fixture = await localOpenAIServer();
		try {
			const client = wrapOpenAI(new OpenAI({apiKey: "test", baseURL: fixture.baseURL}));
			const model = wrapChatModel({
				async invoke(): Promise<AIMessage> {
					const result = await client.responses.create({model: "gpt-flight", input: "nested"});
					return new AIMessage({content: result.id});
				},
			});
			const answer = await model.invoke([new HumanMessage({content: "question"})]);
			expect(answer.content).toBe("resp_flight");
			expect(testing.occurrenceKeys()).toHaveLength(1);
			expect(testing.occurrenceKeys()[0]).toContain("langchain.model");
		} finally { await new Promise<void>(resolve => fixture.server.close(() => resolve())); }
	});

	it("preserves official message, chunk, and document types", async () => {
		const model = wrapChatModel({
			async invoke(input: HumanMessage[]): Promise<AIMessage> { return new AIMessage({content: `answer:${input[0]?.content}`}); },
			async *stream(): AsyncIterable<AIMessageChunk> { yield new AIMessageChunk({content: "an"}); yield new AIMessageChunk({content: "swer"}); },
		});
		const retriever = wrapRetriever({async invoke(query: string): Promise<Document[]> { return [new Document({pageContent: `policy:${query}`, metadata: {source: "fixture"}})]; }});
		const tool = wrapTool({async invoke(value: unknown): Promise<unknown> { return {ok: true, value}; }}, {sideEffect: "read_only"});

		const answer = await model.invoke([new HumanMessage({content: "question"})]);
		const stream = await model.stream([new HumanMessage({content: "question"})]);
		const chunks = [];
		for await (const chunk of stream) chunks.push(chunk);
		const documents = await retriever.invoke("refunds");
		const toolResult = await tool.invoke({id: 1});

		expect(answer).toBeInstanceOf(AIMessage);
		expect(chunks.every(chunk => chunk instanceof AIMessageChunk)).toBe(true);
		expect(documents[0]).toBeInstanceOf(Document);
		expect(toolResult).toEqual({ok: true, value: {id: 1}});
	});

	it("exposes modern agent middleware and blocks unknown tools during replay", async () => {
		const middleware = flightRecorderMiddleware();
		expect(middleware).toHaveProperty("wrapModelCall");
		expect(middleware).toHaveProperty("wrapToolCall");
		const filename = join(mkdtempSync(join(tmpdir(), "flight-tool-")), "plan.json");
		writeFileSync(filename, JSON.stringify({boundaries: []}));
		process.env.FLIGHT_REPLAY_PLAN = filename;
		const live = vi.fn();
		const tool = wrapTool({invoke: live});
		await expect(tool.invoke({id: 1})).rejects.toThrow("live fallback is disabled");
		expect(live).not.toHaveBeenCalled();
	});
});
