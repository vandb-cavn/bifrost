export type GuardrailBuilderField =
	| "request_message"
	| "request_model"
	| "response_content"
	| "response_finish_reason";

export type GuardrailBuilderOperator = "contains" | "equals" | "starts_with" | "ends_with";

export type GuardrailBuilderRule =
	| {
			field: GuardrailBuilderField;
			operator: GuardrailBuilderOperator;
			value: string;
	  }
	| {
			field: GuardrailBuilderField;
			operator: "is_empty";
	  };

export type GuardrailBuilderGroup = {
	combinator: "and" | "or";
	rules: Array<GuardrailBuilderGroup | GuardrailBuilderRule>;
};

export const guardrailBuilderFields = [
	{ name: "request_message", label: "Request message" },
	{ name: "request_model", label: "Model" },
	{ name: "response_content", label: "Response content" },
	{ name: "response_finish_reason", label: "Finish reason" },
] as const;

export const defaultGuardrailBuilderGroup: GuardrailBuilderGroup = {
	combinator: "and",
	rules: [],
};

const fieldPathByName: Record<GuardrailBuilderField, string> = {
	request_message: "request.messages.exists(m, m.content",
	request_model: "request.model",
	response_content: "output.content",
	response_finish_reason: "output.finish_reason",
};

const celMethodByOperator: Record<Exclude<GuardrailBuilderOperator, "equals" | "is_empty">, "contains" | "startsWith" | "endsWith"> = {
	contains: "contains",
	starts_with: "startsWith",
	ends_with: "endsWith",
};

const fieldImportPatterns: Record<GuardrailBuilderField, RegExp> = {
	request_message:
		/^request\.messages\.exists\(m,\s*m\.content(?:\.(contains|startsWith|endsWith)\(("(?:[^"\\]|\\.)*")\)|\s*==\s*("(?:[^"\\]|\\.)*"))\)$/u,
	request_model: /^request\.model(?:\.(contains|startsWith|endsWith)\(("(?:[^"\\]|\\.)*")\)|\s*==\s*("(?:[^"\\]|\\.)*"))$/u,
	response_content: /^output\.content(?:\.(contains|startsWith|endsWith)\(("(?:[^"\\]|\\.)*")\)|\s*==\s*("(?:[^"\\]|\\.)*"))$/u,
	response_finish_reason:
		/^output\.finish_reason(?:\.(contains|startsWith|endsWith)\(("(?:[^"\\]|\\.)*")\)|\s*==\s*("(?:[^"\\]|\\.)*"))$/u,
};

function escapeCELString(value: string): string {
	return value.replace(/\\/g, "\\\\").replace(/"/g, '\\"').replace(/\n/g, "\\n").replace(/\r/g, "\\r");
}

function quoteCELString(value: string): string {
	return `"${escapeCELString(value)}"`;
}

function serializeRule(rule: GuardrailBuilderRule): string {
	const fieldPath = fieldPathByName[rule.field];

	if (rule.field === "request_message") {
		if (rule.operator === "is_empty") {
			return `${fieldPath} == ""` + ")";
		}

		if (rule.operator === "equals") {
			return `${fieldPath} == ${quoteCELString(rule.value)}` + ")";
		}

		return `${fieldPath}.${celMethodByOperator[rule.operator]}(${quoteCELString(rule.value)}))`;
	}

	if (rule.operator === "is_empty") {
		return `${fieldPath} == ""`;
	}

	const method = rule.operator === "equals" ? "==" : `${rule.operator}(`;
	if (rule.operator === "equals") {
		return `${fieldPath} ${method} ${quoteCELString(rule.value)}`;
	}

	return `${fieldPath}.${celMethodByOperator[rule.operator]}(${quoteCELString(rule.value)})`;
}

function serializeGroup(group: GuardrailBuilderGroup, isRoot = false): string {
	const parts = group.rules
		.map((rule) => {
			if ("rules" in rule) {
				return serializeGroup(rule, false);
			}

			return serializeRule(rule);
		})
		.filter(Boolean);

	if (parts.length === 0) {
		return "";
	}

	const separator = group.combinator === "and" ? " && " : " || ";
	const expression = parts.join(separator);

	if (!isRoot && parts.length > 1) {
		return `(${expression})`;
	}

	return expression;
}

export function serializeGuardrailQuery(group: GuardrailBuilderGroup): string {
	return serializeGroup(group, true);
}

function parseCELStringLiteral(literal: string): string | null {
	if (!/^"(?:[^"\\]|\\.)*"$/u.test(literal)) {
		return null;
	}

	try {
		return JSON.parse(literal) as string;
	} catch {
		return null;
	}
}

function parseRuleExpression(expression: string): GuardrailBuilderRule | null {
	for (const field of Object.keys(fieldImportPatterns) as GuardrailBuilderField[]) {
		const match = expression.match(fieldImportPatterns[field]);
		if (!match) {
			continue;
		}

		const operatorToken = match[1];
		const stringLiteral = match[2] ?? match[3];

		if (stringLiteral === '""') {
			return {
				field,
				operator: "is_empty",
			};
		}

		if (!stringLiteral) {
			return null;
		}

		const value = parseCELStringLiteral(stringLiteral);
		if (value === null) {
			return null;
		}

		if (operatorToken === "contains" || operatorToken === "startsWith" || operatorToken === "endsWith") {
			return {
				field,
				operator: operatorToken === "startsWith" ? "starts_with" : operatorToken === "endsWith" ? "ends_with" : "contains",
				value,
			};
		}

		if (!operatorToken) {
			return {
				field,
				operator: "equals",
				value,
			};
		}

		return null;
	}

	return null;
}

function isWrappedByParens(expression: string): boolean {
	if (!expression.startsWith("(") || !expression.endsWith(")")) {
		return false;
	}

	let depth = 0;
	let inString = false;
	let escaped = false;
	for (let index = 0; index < expression.length; index += 1) {
		const char = expression[index];
		if (inString) {
			if (escaped) {
				escaped = false;
				continue;
			}

			if (char === "\\") {
				escaped = true;
				continue;
			}

			if (char === '"') {
				inString = false;
			}

			continue;
		}

		if (char === '"') {
			inString = true;
			continue;
		}

		if (char === "(") {
			depth += 1;
		} else if (char === ")") {
			depth -= 1;
		}

		if (depth === 0 && index < expression.length - 1) {
			return false;
		}
	}

	return depth === 0;
}

function splitTopLevel(expression: string, operator: "&&" | "||"): string[] {
	const parts: string[] = [];
	let depth = 0;
	let current = "";
	let inString = false;
	let escaped = false;

	for (let index = 0; index < expression.length; index += 1) {
		const char = expression[index];
		const nextTwo = expression.slice(index, index + 2);

		if (inString) {
			current += char;
			if (escaped) {
				escaped = false;
				continue;
			}

			if (char === "\\") {
				escaped = true;
				continue;
			}

			if (char === '"') {
				inString = false;
			}

			continue;
		}

		if (char === '"') {
			inString = true;
			current += char;
			continue;
		}

		if (char === "(") {
			depth += 1;
		} else if (char === ")") {
			depth -= 1;
		}

		if (depth === 0 && nextTwo === operator) {
			if (current.trim()) {
				parts.push(current.trim());
			}
			current = "";
			index += 1;
			continue;
		}

		current += char;
	}

	if (current.trim()) {
		parts.push(current.trim());
	}

	return parts;
}

function importGroup(expression: string): GuardrailBuilderGroup | GuardrailBuilderRule | null {
	const trimmed = expression.trim();
	if (!trimmed) {
		return null;
	}

	const normalized = isWrappedByParens(trimmed) ? trimmed.slice(1, -1).trim() : trimmed;
	const orParts = splitTopLevel(normalized, "||");
	if (orParts.length > 1) {
		const rules = orParts.map((part) => importGroup(part)).filter(Boolean) as Array<GuardrailBuilderGroup | GuardrailBuilderRule>;
		if (rules.length !== orParts.length) {
			return null;
		}

		return {
			combinator: "or",
			rules,
		};
	}

	const andParts = splitTopLevel(normalized, "&&");
	if (andParts.length > 1) {
		const rules = andParts.map((part) => importGroup(part)).filter(Boolean) as Array<GuardrailBuilderGroup | GuardrailBuilderRule>;
		if (rules.length !== andParts.length) {
			return null;
		}

		return {
			combinator: "and",
			rules,
		};
	}

	return parseRuleExpression(normalized);
}

export function importGuardrailQuery(expression: string): GuardrailBuilderGroup | null {
	const imported = importGroup(expression);
	if (!imported) {
		return null;
	}

	if ("field" in imported) {
		return {
			combinator: "and",
			rules: [imported],
		};
	}

	return imported;
}
