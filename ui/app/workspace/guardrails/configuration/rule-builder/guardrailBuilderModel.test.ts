import { describe, expect, it } from "vitest";
import {
	getGuardrailBuilderFields,
	importGuardrailQuery,
	isGuardrailBuilderGroupCompatible,
	serializeGuardrailQuery,
} from "./guardrailBuilderModel";

describe("serializeGuardrailQuery", () => {
	it("serializes a request-message contains rule", () => {
		expect(
			serializeGuardrailQuery({
				combinator: "and",
				rules: [
					{
						field: "request_message",
						operator: "contains",
						value: "secret",
					},
				],
			}),
		).toBe('request.messages.exists(m, m.content.contains("secret"))');
	});

	it("serializes nested groups and escapes string literals", () => {
		expect(
			serializeGuardrailQuery({
				combinator: "and",
				rules: [
					{
						field: "request_message",
						operator: "contains",
						value: 'secret "phrase"',
					},
					{
						combinator: "or",
						rules: [
							{
								field: "response_content",
								operator: "contains",
								value: "policy",
							},
							{
								field: "response_finish_reason",
								operator: "equals",
								value: "stop",
							},
						],
					},
				],
			}),
		).toBe(
			'request.messages.exists(m, m.content.contains("secret \\"phrase\\"")) && (output.content.contains("policy") || output.finish_reason == "stop")',
		);
	});

	it("serializes tabs in string literals", () => {
		expect(
			serializeGuardrailQuery({
				combinator: "and",
				rules: [
					{
						field: "request_message",
						operator: "contains",
						value: "secret\tvalue",
					},
				],
			}),
		).toBe('request.messages.exists(m, m.content.contains("secret\\tvalue"))');
	});

	it("serializes a starts-with rule", () => {
		expect(
			serializeGuardrailQuery({
				combinator: "and",
				rules: [
					{
						field: "request_model",
						operator: "starts_with",
						value: "gpt-4",
					},
				],
			}),
		).toBe('request.model.startsWith("gpt-4")');
	});

	it("serializes a request-message empty rule with a closing exists parenthesis", () => {
		expect(
			serializeGuardrailQuery({
				combinator: "and",
				rules: [
					{
						field: "request_message",
						operator: "is_empty",
					},
				],
			}),
		).toBe('request.messages.exists(m, m.content == "")');
	});
});

describe("importGuardrailQuery", () => {
	it("imports a request-message empty rule", () => {
		expect(importGuardrailQuery('request.messages.exists(m, m.content == "")')).toEqual({
			combinator: "and",
			rules: [
				{
					field: "request_message",
					operator: "is_empty",
				},
			],
		});
	});

	it("wraps a supported single rule in a builder group", () => {
		expect(importGuardrailQuery('output.content.contains("policy")')).toEqual({
			combinator: "and",
			rules: [
				{
					field: "response_content",
					operator: "contains",
					value: "policy",
				},
			],
		});
	});

	it("imports a supported builder-generated expression", () => {
		expect(
			importGuardrailQuery(
				'request.messages.exists(m, m.content.contains("secret")) && (output.content.contains("policy") || output.finish_reason == "stop")',
			),
		).toEqual({
			combinator: "and",
			rules: [
				{
					field: "request_message",
					operator: "contains",
					value: "secret",
				},
				{
					combinator: "or",
					rules: [
						{
							field: "response_content",
							operator: "contains",
							value: "policy",
						},
						{
							field: "response_finish_reason",
							operator: "equals",
							value: "stop",
						},
					],
				},
			],
		});
	});

	it("returns null for unsupported expressions", () => {
		expect(importGuardrailQuery('request.user.contains("secret")')).toBeNull();
	});

	it("imports request-message expressions with flexible whitespace", () => {
		expect(importGuardrailQuery('request.messages.exists( m , m.content.contains("secret") )')).toEqual({
			combinator: "and",
			rules: [
				{
					field: "request_message",
					operator: "contains",
					value: "secret",
				},
			],
		});
	});
});

describe("role-filtered message fields", () => {
	it("serializes a user message contains rule", () => {
		expect(
			serializeGuardrailQuery({
				combinator: "and",
				rules: [{ field: "request_user_message", operator: "contains", value: "secret" }],
			}),
		).toBe('request.messages.exists(m, m.role == "user" && m.content.contains("secret"))');
	});

	it("serializes a system message equals rule", () => {
		expect(
			serializeGuardrailQuery({
				combinator: "and",
				rules: [{ field: "request_system_message", operator: "equals", value: "You are helpful" }],
			}),
		).toBe('request.messages.exists(m, m.role == "system" && m.content == "You are helpful")');
	});

	it("serializes an assistant message is_empty rule", () => {
		expect(
			serializeGuardrailQuery({
				combinator: "and",
				rules: [{ field: "request_assistant_message", operator: "is_empty" }],
			}),
		).toBe('request.messages.exists(m, m.role == "assistant" && m.content == "")');
	});

	it("imports a user message contains expression", () => {
		expect(importGuardrailQuery('request.messages.exists(m, m.role == "user" && m.content.contains("secret"))')).toEqual({
			combinator: "and",
			rules: [{ field: "request_user_message", operator: "contains", value: "secret" }],
		});
	});

	it("imports a system message starts_with expression", () => {
		expect(importGuardrailQuery('request.messages.exists(m, m.role == "system" && m.content.startsWith("You"))')).toEqual({
			combinator: "and",
			rules: [{ field: "request_system_message", operator: "starts_with", value: "You" }],
		});
	});

	it("imports an assistant message equals expression", () => {
		expect(importGuardrailQuery('request.messages.exists(m, m.role == "assistant" && m.content == "ok")')).toEqual({
			combinator: "and",
			rules: [{ field: "request_assistant_message", operator: "equals", value: "ok" }],
		});
	});
});

describe("guardrail builder field compatibility", () => {
	it("limits input builders to request fields", () => {
		expect(getGuardrailBuilderFields("input").map((field) => field.name)).toEqual([
			"request_message",
			"request_user_message",
			"request_system_message",
			"request_assistant_message",
			"request_model",
		]);
	});

	it("rejects response fields for input-only builders", () => {
		expect(
			isGuardrailBuilderGroupCompatible(
				{
					combinator: "and",
					rules: [
						{
							field: "response_content",
							operator: "contains",
							value: "policy",
						},
					],
				},
				"input",
			),
		).toBe(false);
	});
});
