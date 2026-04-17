import { GuardrailProviderName } from "@/lib/types/guardrails";

export type GuardrailProfileConfig = Record<string, unknown>;

export const guardrailProviderLabels: Record<GuardrailProviderName, string> = {
	bedrock: "AWS Bedrock",
	azure: "Azure Content Safety",
	grayswan: "GraySwan",
	patronus_ai: "Patronus AI",
	model_armor: "Google Cloud Model Armor",
};

export const defaultProfileConfigByProvider = {
	bedrock: { endpoint: "", guardrail_id: "", version: "DRAFT" },
	azure: { endpoint: "", api_key: "", severity_threshold: 4 },
	grayswan: { endpoint: "", api_key: "", score_threshold: 0.5 },
	patronus_ai: { endpoint: "", api_key: "", evaluator: "lynx" },
	model_armor: { project_id: "", location: "", template_id: "", credentials_json: "" },
} satisfies Record<GuardrailProviderName, GuardrailProfileConfig>;

function isPlainObject(value: unknown): value is GuardrailProfileConfig {
	return typeof value === "object" && value !== null && !Array.isArray(value);
}

export function getDefaultProfileConfig(providerName: GuardrailProviderName): GuardrailProfileConfig {
	return { ...defaultProfileConfigByProvider[providerName] };
}

export function mergeProfileConfigDefaults(
	providerName: GuardrailProviderName,
	config: GuardrailProfileConfig | null | undefined,
): GuardrailProfileConfig {
	return {
		...getDefaultProfileConfig(providerName),
		...(config ?? {}),
	};
}

export function parseProfileConfigJson(value: string): GuardrailProfileConfig {
	const parsed = JSON.parse(value);
	if (!isPlainObject(parsed)) {
		throw new Error("Profile config JSON must be an object");
	}
	return parsed;
}

export function stringifyProfileConfig(config: GuardrailProfileConfig): string {
	return JSON.stringify(config, null, 2);
}
