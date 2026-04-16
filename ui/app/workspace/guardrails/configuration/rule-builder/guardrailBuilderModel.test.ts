import { describe, expect, it } from "vitest";
import {
	importGuardrailQuery,
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
});

describe("importGuardrailQuery", () => {
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
});
