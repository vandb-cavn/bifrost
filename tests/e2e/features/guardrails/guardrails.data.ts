export type GuardrailProviderName = "bedrock" | "azure" | "grayswan" | "patronus_ai" | "model_armor";

export interface GuardrailProfileData {
	name: string;
	provider_name: GuardrailProviderName;
	enabled: boolean;
	config: Record<string, unknown>;
}

export interface GuardrailRuleData {
	name: string;
	description?: string;
	enabled?: boolean;
	apply_to?: "input" | "output" | "both";
	action?: "block" | "warn";
	sampling_rate?: number;
	timeout_ms?: number;
	priority?: number;
	scope?: "global" | "virtual_key" | "team";
	scope_id?: string;
	block_message?: string;
	fail_open?: boolean;
	cel_expression?: string;
	profileNames?: string[];
}

function defaultProfileConfig(provider_name: GuardrailProviderName): Record<string, unknown> {
	switch (provider_name) {
		case "bedrock":
			return { endpoint: "https://bedrock.example.com", guardrail_id: "guardrail-1", version: "DRAFT" };
		case "azure":
			return {
				endpoint: "https://example.cognitiveservices.azure.com",
				api_key: "test-api-key",
				severity_threshold: 4,
			};
		case "grayswan":
			return {
				endpoint: "https://grayswan.example.com",
				api_key: "test-api-key",
				score_threshold: 0.5,
			};
		case "patronus_ai":
			return {
				endpoint: "https://api.patronus.ai",
				api_key: "test-api-key",
				evaluator: "lynx",
			};
		case "model_armor":
			return {
				project_id: "test-project",
				location: "us-central1",
				template_id: "template-1",
				credentials_json: "eyJ0eXBlIjoic2VydmljZV9hY2NvdW50In0=",
			};
	}
}

export function createGuardrailProfileData(overrides: Partial<GuardrailProfileData> = {}): GuardrailProfileData {
	const timestamp = Date.now();
	const provider_name = overrides.provider_name ?? "azure";
	return {
		name: `guardrail-profile-${timestamp}`,
		provider_name,
		enabled: true,
		config: defaultProfileConfig(provider_name),
		...overrides,
		config: overrides.config ?? defaultProfileConfig(provider_name),
	};
}

export function createGuardrailRuleData(overrides: Partial<GuardrailRuleData> = {}): GuardrailRuleData {
	const timestamp = Date.now();
	return {
		name: `guardrail-rule-${timestamp}`,
		description: "Guardrail rule created by E2E",
		enabled: true,
		apply_to: "both",
		action: "block",
		sampling_rate: 100,
		timeout_ms: 60000,
		priority: 0,
		scope: "global",
		block_message: "Request blocked by guardrail policy",
		fail_open: true,
		cel_expression: 'request.messages.exists(m, m.content.contains("secret"))',
		profileNames: [],
		...overrides,
	};
}
