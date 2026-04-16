export type GuardrailBuilderSample = {
	messages: Array<{
		role: "user";
		content: string;
	}>;
	model: string;
	output?: {
		content: string;
		finish_reason: string;
	};
};

export function getGuardrailBuilderSample(applyTo: "input" | "output" | "both"): GuardrailBuilderSample {
	if (applyTo === "input") {
		return {
			messages: [{ role: "user", content: "Test message" }],
			model: "gpt-4o",
		};
	}

	return {
		messages: [{ role: "user", content: "Original request" }],
		model: "gpt-4o",
		output: {
			content: "Assistant response",
			finish_reason: "stop",
		},
	};
}
