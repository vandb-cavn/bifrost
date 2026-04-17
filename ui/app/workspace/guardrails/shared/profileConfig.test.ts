import { describe, expect, it } from "vitest";
import {
	defaultProfileConfigByProvider,
	getDefaultProfileConfig,
	mergeProfileConfigDefaults,
	parseProfileConfigJson,
	stringifyProfileConfig,
} from "./profileConfig";

describe("profileConfig", () => {
	it("returns a copy of the default config for each provider", () => {
		const config = getDefaultProfileConfig("bedrock");
		expect(config).toEqual(defaultProfileConfigByProvider.bedrock);
		expect(config).not.toBe(defaultProfileConfigByProvider.bedrock);
	});

	it("merges missing optional bedrock fields with defaults", () => {
		expect(
			mergeProfileConfigDefaults("bedrock", {
				endpoint: "https://bedrock.example.com",
				guardrail_id: "gr-123",
			}),
		).toEqual({
			endpoint: "https://bedrock.example.com",
			guardrail_id: "gr-123",
			version: "DRAFT",
		});
	});

	it("preserves explicit provider config overrides", () => {
		expect(
			mergeProfileConfigDefaults("azure", {
				endpoint: "https://custom.azure.example.com",
				api_key: "secret",
				severity_threshold: 2,
			}),
		).toEqual({
			endpoint: "https://custom.azure.example.com",
			api_key: "secret",
			severity_threshold: 2,
		});
	});

	it("parses and stringifies provider config JSON", () => {
		const json = stringifyProfileConfig({
			endpoint: "https://example.com",
			api_key: "secret",
		});
		expect(parseProfileConfigJson(json)).toEqual({
			endpoint: "https://example.com",
			api_key: "secret",
		});
	});

	it("rejects non-object JSON payloads", () => {
		expect(() => parseProfileConfigJson("[1, 2, 3]")).toThrow("Profile config JSON must be an object");
	});
});
