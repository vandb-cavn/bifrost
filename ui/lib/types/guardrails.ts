export type GuardrailProviderName = "bedrock" | "azure" | "grayswan" | "patronus_ai" | "model_armor";

export interface GuardrailProfile {
	id: string;
	name: string;
	provider_name: GuardrailProviderName;
	enabled: boolean;
	config: Record<string, unknown>;
	created_at: string;
	updated_at: string;
}

export interface GuardrailRule {
	id: string;
	name: string;
	description: string;
	enabled: boolean;
	cel_expression: string;
	apply_to: "input" | "output" | "both";
	action: "block" | "warn";
	sampling_rate: number;
	timeout_ms: number;
	priority: number;
	scope: "global" | "virtual_key" | "team";
	scope_id: string | null;
	block_message: string;
	fail_open: boolean;
	profiles?: GuardrailProfile[];
	created_at: string;
	updated_at: string;
}

export interface CreateGuardrailProfileRequest {
	name: string;
	provider_name: GuardrailProviderName;
	enabled: boolean;
	config: Record<string, unknown>;
}

export interface UpdateGuardrailProfileRequest {
	name?: string;
	provider_name?: GuardrailProviderName;
	enabled?: boolean;
	config?: Record<string, unknown>;
}

export interface CreateGuardrailRuleRequest {
	name: string;
	description?: string;
	enabled: boolean;
	cel_expression: string;
	apply_to: "input" | "output" | "both";
	action: "block" | "warn";
	sampling_rate: number;
	timeout_ms: number;
	priority: number;
	scope: "global" | "virtual_key" | "team";
	scope_id: string | null;
	block_message: string;
	fail_open: boolean;
}

export interface UpdateGuardrailRuleRequest {
	name?: string;
	description?: string;
	enabled?: boolean;
	cel_expression?: string;
	apply_to?: "input" | "output" | "both";
	action?: "block" | "warn";
	sampling_rate?: number;
	timeout_ms?: number;
	priority?: number;
	scope?: "global" | "virtual_key" | "team";
	scope_id?: string | null;
	block_message?: string;
	fail_open?: boolean;
	profiles?: GuardrailProfile[];
}

export interface ValidateRuleRequest {
	cel_expression: string;
	sample: any;
}

export interface ValidateRuleResponse {
	valid: boolean;
	result?: boolean;
	error?: string;
}
