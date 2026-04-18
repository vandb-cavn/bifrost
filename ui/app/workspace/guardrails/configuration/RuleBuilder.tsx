"use client";

import { Button } from "@/components/ui/button";
import { CodeEditor } from "@/components/ui/codeEditor";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { useValidateGuardrailRuleMutation } from "@/lib/store/apis/guardrailsApi";
import { CheckCircle, Copy, Loader2, Plus, X, XCircle } from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { ReactNode } from "react";
import { QueryBuilder } from "react-querybuilder";
import { toast } from "sonner";
import {
	defaultGuardrailBuilderGroup,
	getGuardrailBuilderFields,
	importGuardrailQuery,
	isGuardrailBuilderGroupCompatible,
	serializeGuardrailQuery,
	type GuardrailBuilderGroup,
} from "./rule-builder/guardrailBuilderModel";
import { getGuardrailBuilderSample } from "./rule-builder/guardrailBuilderSamples";

interface RuleBuilderProps {
	value: string;
	onChange: (val: string) => void;
	applyTo: "input" | "output" | "both";
}

type RuleBuilderMode = "builder" | "editor";

type BuilderOperator = "contains" | "equals" | "starts_with" | "ends_with" | "is_empty";

const builderOperators: Array<{ name: BuilderOperator; label: string }> = [
	{ name: "contains", label: "contains" },
	{ name: "equals", label: "equals" },
	{ name: "starts_with", label: "starts with" },
	{ name: "ends_with", label: "ends with" },
	{ name: "is_empty", label: "is empty" },
];

function createEmptyGroup(): GuardrailBuilderGroup {
	return {
		combinator: defaultGuardrailBuilderGroup.combinator,
		rules: [],
	};
}

function getInitialState(
	expression: string,
	applyTo: RuleBuilderProps["applyTo"],
): { mode: RuleBuilderMode; query: GuardrailBuilderGroup } {
	const imported = importGuardrailQuery(expression);

	if (imported && isGuardrailBuilderGroupCompatible(imported, applyTo)) {
		return {
			mode: "builder",
			query: imported,
		};
	}

	if (!expression.trim()) {
		return {
			mode: "builder",
			query: createEmptyGroup(),
		};
	}

	return {
		mode: "editor",
		query: createEmptyGroup(),
	};
}

function getCompatibleImportedQuery(expression: string, applyTo: RuleBuilderProps["applyTo"]): GuardrailBuilderGroup | null {
	const imported = importGuardrailQuery(expression);
	if (!imported || !isGuardrailBuilderGroupCompatible(imported, applyTo)) {
		return null;
	}

	return imported;
}

function BuilderFieldSelector({ value, handleOnChange, options, allowedFieldNames }: any) {
	const allowedNames = new Set(Array.isArray(allowedFieldNames) ? allowedFieldNames : []);
	const visibleOptions = options.filter((option: any) => {
		if ("options" in option || !option.name) {
			return false;
		}

		return allowedNames.size === 0 || allowedNames.has(option.name);
	});

	return (
		<Select value={value || ""} onValueChange={handleOnChange}>
			<SelectTrigger className="w-[220px] bg-white dark:bg-input/30" data-testid="guardrails-builder-field-select">
				<SelectValue placeholder="Select field" />
			</SelectTrigger>
			<SelectContent>
				{visibleOptions.map((option: any) => (
					<SelectItem key={option.name} value={option.name} disabled={option.disabled}>
						{option.label}
					</SelectItem>
				))}
			</SelectContent>
		</Select>
	);
}

function BuilderOperatorSelector({ value, handleOnChange, options }: any) {
	return (
		<Select value={value || ""} onValueChange={handleOnChange}>
			<SelectTrigger className="w-[180px] bg-white dark:bg-input/30" data-testid="guardrails-builder-operator-select">
				<SelectValue placeholder="Select operator" />
			</SelectTrigger>
			<SelectContent>
				{options.map((option: any) => {
					if ("options" in option) {
						return null;
					}

					if (!option.name) {
						return null;
					}

					return (
						<SelectItem key={option.name} value={option.name} disabled={option.disabled}>
							{option.label}
						</SelectItem>
					);
				})}
			</SelectContent>
		</Select>
	);
}

function BuilderValueEditor({ value, handleOnChange, operator }: any) {
	if (operator === "is_empty") {
		return <span className="text-muted-foreground px-2 text-xs">No value required</span>;
	}

	return (
		<Input
			type="text"
			value={value || ""}
			onChange={(event) => handleOnChange(event.target.value)}
			placeholder="Enter value"
			className="min-w-[240px] bg-white dark:bg-input/30"
			data-testid="guardrails-builder-value-input"
		/>
	);
}

function BuilderActionButton({ handleOnClick, label, className, title }: any) {
	const labelText = typeof label === "string" ? label : "";
	const isRemove = labelText.toLowerCase().includes("remove") || title === "Remove rule" || title === "Remove group";
	const isAdd = labelText.toLowerCase().includes("add");

	return (
		<Button
			type="button"
			variant={isRemove ? "ghost" : "outline"}
			size="sm"
			className={className}
			onClick={(event) => handleOnClick(event)}
			aria-label={isRemove ? title || labelText || "Remove" : undefined}
		>
			{isRemove && <X className="h-4 w-4" />}
			{isAdd && <Plus className="h-4 w-4" />}
			{!isRemove && label}
		</Button>
	);
}

function BuilderCombinatorSelector({ value, handleOnChange, options }: any) {
	return (
		<div className="flex gap-1">
			{options.map((option: any) => {
				if ("options" in option) {
					return null;
				}

				return (
					<Button
						key={option.name}
						type="button"
						variant={value === option.name ? "default" : "outline"}
						size="sm"
						onClick={() => handleOnChange(option.name)}
						className="px-3"
					>
						{option.label.toUpperCase()}
					</Button>
				);
			})}
		</div>
	);
}

const QUERY_BUILDER_CSS = `
.guardrails-query-builder .queryBuilder { font-family: inherit; }
.guardrails-query-builder .ruleGroup { background-color: hsl(var(--muted) / 0.28); border: 1px solid hsl(var(--border)); border-radius: 0.5rem; margin-bottom: 0.5rem; }
.guardrails-query-builder .ruleGroup .ruleGroup { background-color: hsl(var(--background)); }
.guardrails-query-builder .ruleGroup-header { display: flex; align-items: center; flex-wrap: wrap; gap: 0.5rem; padding: 0.75rem 0.75rem 0.5rem; }
.guardrails-query-builder .ruleGroup-body { display: flex; flex-direction: column; gap: 0.5rem; padding: 0 0.75rem 0.75rem; }
.guardrails-query-builder .rule { display: flex; flex-wrap: wrap; align-items: center; gap: 0.5rem; padding: 0.75rem; background-color: hsl(var(--background)); border: 1px solid hsl(var(--border)); border-radius: 0.375rem; }
.guardrails-query-builder .rule > * { flex-shrink: 0; }
.guardrails-query-builder .ruleGroup-addRule, .guardrails-query-builder .ruleGroup-addGroup { margin-top: 0.25rem; }
.guardrails-query-builder .ruleGroup-header .ruleGroup-addRule, .guardrails-query-builder .ruleGroup-header .ruleGroup-addGroup { margin-top: 0; }
.guardrails-query-builder .ruleGroup .ruleGroup .ruleGroup-header .ruleGroup-remove { margin-left: 0.5rem; }
.guardrails-query-builder .queryBuilder-branches .ruleGroup-body { padding-left: 1rem; }
`;

const QUERY_BUILDER_CLASSNAMES = { queryBuilder: "queryBuilder-branches" };
const QUERY_BUILDER_TRANSLATIONS = {
	addRule: { label: "Add rule" },
	addGroup: { label: "Add group" },
};

function QueryBuilderSurface({ children }: { children: ReactNode }) {
	return (
		<div className="guardrails-query-builder">
			{/* eslint-disable-next-line react/no-danger */}
			<style dangerouslySetInnerHTML={{ __html: QUERY_BUILDER_CSS }} />
			{children}
		</div>
	);
}

export function RuleBuilder({ value, onChange, applyTo }: RuleBuilderProps) {
	const [validateRule, { isLoading: isValidating }] = useValidateGuardrailRuleMutation();
	const [validationResult, setValidationResult] = useState<{
		valid: boolean;
		result?: boolean;
		error?: string;
	} | null>(null);
	const [mode, setMode] = useState<RuleBuilderMode>(() => getInitialState(value, applyTo).mode);
	const [builderQuery, setBuilderQuery] = useState<GuardrailBuilderGroup>(() => getInitialState(value, applyTo).query);
	const lastSyncedValueRef = useRef(value);
	const lastSyncedApplyToRef = useRef(applyTo);
	const builderFields = useMemo(() => getGuardrailBuilderFields(applyTo), [applyTo]);
	const allowedFieldNames = useMemo(() => builderFields.map((f) => f.name), [builderFields]);
	const fieldSelector = useCallback(
		(props: any) => <BuilderFieldSelector {...props} allowedFieldNames={allowedFieldNames} />,
		[allowedFieldNames],
	);
	const controlElements = useMemo(
		() => ({
			fieldSelector,
			operatorSelector: BuilderOperatorSelector,
			valueEditor: BuilderValueEditor,
			addRuleAction: BuilderActionButton,
			addGroupAction: BuilderActionButton,
			removeRuleAction: BuilderActionButton,
			removeGroupAction: BuilderActionButton,
			combinatorSelector: BuilderCombinatorSelector,
		}),
		[fieldSelector],
	);

	const validationSample = useMemo(() => getGuardrailBuilderSample(applyTo), [applyTo]);
	const builderExpression = useMemo(() => serializeGuardrailQuery(builderQuery), [builderQuery]);
	const activeExpression = mode === "builder" ? builderExpression : value;
	const validationContextLabel = validationSample.output ? "Request + response sample" : "Request-only sample";

	useEffect(() => {
		if (value === lastSyncedValueRef.current && applyTo === lastSyncedApplyToRef.current) {
			return;
		}

		const imported = getCompatibleImportedQuery(value, applyTo);
		if (imported) {
			setBuilderQuery(imported);
			setMode("builder");
		} else if (!value.trim()) {
			setBuilderQuery(createEmptyGroup());
			setMode("builder");
		} else {
			setMode("editor");
		}

		lastSyncedValueRef.current = value;
		lastSyncedApplyToRef.current = applyTo;
	}, [value, applyTo]);

	useEffect(() => {
		if (mode !== "builder") {
			return;
		}

		if (!isGuardrailBuilderGroupCompatible(builderQuery, applyTo)) {
			setMode("editor");
		}
	}, [applyTo, builderQuery, mode]);

	useEffect(() => {
		setValidationResult(null);
	}, [activeExpression]);

	const handleBuilderChange = (nextQuery: GuardrailBuilderGroup) => {
		setBuilderQuery(nextQuery);
		const nextExpression = serializeGuardrailQuery(nextQuery);
		lastSyncedValueRef.current = nextExpression;
		onChange(nextExpression);
	};

	const handleEditorChange = (nextValue: string) => {
		lastSyncedValueRef.current = nextValue;
		onChange(nextValue);

		const imported = getCompatibleImportedQuery(nextValue, applyTo);
		if (imported) {
			setBuilderQuery(imported);
		}
	};

	const handleModeChange = (nextMode: string) => {
		if (nextMode === "editor") {
			setMode("editor");
			return;
		}

		const imported = getCompatibleImportedQuery(value, applyTo);
		if (imported) {
			setBuilderQuery(imported);
			setMode("builder");
			return;
		}

		if (!value.trim()) {
			setBuilderQuery(createEmptyGroup());
			setMode("builder");
			return;
		}

		setMode("editor");
	};

	const copyToClipboard = async () => {
		try {
			await navigator.clipboard.writeText(activeExpression);
			toast.success("Copied to clipboard");
		} catch {
			toast.error("Could not copy expression");
		}
	};

	const handleValidate = async () => {
		try {
			const res = await validateRule({
				cel_expression: activeExpression,
				sample: validationSample,
			}).unwrap();

			setValidationResult(res);
			if (res.valid) {
				toast.success("Expression is valid");
			} else {
				toast.error(`Invalid expression: ${res.error}`);
			}
		} catch (error: any) {
			toast.error("An error occurred during validation");
			setValidationResult({ valid: false, error: error?.message });
		}
	};

	const QueryBuilderComponent = QueryBuilder as any;

	return (
		<div className="space-y-4" data-testid="guardrails-rule-builder-root">
			<Tabs value={mode} onValueChange={handleModeChange} className="space-y-4">
				<TabsList className="grid h-10 w-full grid-cols-2 rounded-sm" aria-label="Guardrails rule editor mode">
					<TabsTrigger value="builder" data-testid="guardrails-rule-builder-tab">
						Visual Builder
					</TabsTrigger>
					<TabsTrigger value="editor" data-testid="guardrails-rule-editor-tab">
						CEL Editor
					</TabsTrigger>
				</TabsList>

				<TabsContent value="builder" className="space-y-4 pt-1">
					<div className="space-y-3">
						<div className="flex items-start justify-between gap-4">
							<div className="space-y-1">
								<p className="text-sm font-medium">Visual rule builder</p>
								<p className="text-muted-foreground text-xs">
									Build supported guardrail rules visually. The CEL expression updates as you edit the query.
								</p>
							</div>
							<Button
								type="button"
								variant="ghost"
								size="sm"
								onClick={copyToClipboard}
								className="h-8 shrink-0"
								disabled={!activeExpression}
							>
								<Copy className="mr-1 h-3.5 w-3.5" />
								<span className="text-xs">Copy CEL</span>
							</Button>
						</div>

						<div className="overflow-hidden rounded-md border bg-background">
							<QueryBuilderSurface>
								<QueryBuilderComponent
									key={applyTo}
									fields={builderFields}
									query={builderQuery}
									onQueryChange={handleBuilderChange}
									operators={builderOperators}
									controlClassnames={QUERY_BUILDER_CLASSNAMES}
									controlElements={controlElements}
									translations={QUERY_BUILDER_TRANSLATIONS}
								/>
							</QueryBuilderSurface>
						</div>

						<div className="space-y-2 rounded-md border bg-background p-3">
							<div className="flex items-center justify-between gap-3">
								<div>
									<p className="text-sm font-medium">CEL preview</p>
									<p className="text-muted-foreground text-xs">Generated from the current visual query.</p>
								</div>
								<p className="text-muted-foreground text-xs">{validationContextLabel}</p>
							</div>
							<CodeEditor
								id="guardrails-rule-expression-preview"
								code={activeExpression}
								lang="plaintext"
								readonly
								className="w-full"
								height={160}
								options={{ lineNumbers: "off", scrollBeyondLastLine: false }}
							/>
						</div>
					</div>
				</TabsContent>

				<TabsContent value="editor" className="space-y-4 pt-1">
					<div className="space-y-3">
						<div className="flex items-start justify-between gap-4">
							<div className="space-y-1">
								<p className="text-sm font-medium">CEL editor</p>
								<p className="text-muted-foreground text-xs">
									Use raw CEL when the expression cannot be represented by the visual builder.
								</p>
							</div>
							<Button
								type="button"
								variant="ghost"
								size="sm"
								onClick={copyToClipboard}
								className="h-8 shrink-0"
								disabled={!activeExpression}
							>
								<Copy className="mr-1 h-3.5 w-3.5" />
								<span className="text-xs">Copy CEL</span>
							</Button>
						</div>

						<div className="overflow-hidden rounded-md border bg-background">
							<CodeEditor
								id="guardrails-rule-expression-editor"
								code={value}
								onChange={handleEditorChange}
								lang="plaintext"
								className="w-full"
								height={220}
								options={{ lineNumbers: "off", scrollBeyondLastLine: false }}
							/>
						</div>
					</div>
				</TabsContent>
			</Tabs>

			<div className="flex flex-col gap-3 lg:flex-row lg:items-start lg:justify-between">
				<div className="min-h-[3rem] flex-1">
					{validationResult ? (
						<div
							className={`flex items-start gap-2 rounded-md border p-3 text-sm ${
								validationResult.valid
									? "border-emerald-500/20 bg-emerald-500/10 text-emerald-700 dark:text-emerald-400"
									: "border-destructive/20 bg-destructive/10 text-destructive"
							}`}
						>
							{validationResult.valid ? (
								<CheckCircle className="mt-0.5 h-4 w-4 shrink-0" />
							) : (
								<XCircle className="mt-0.5 h-4 w-4 shrink-0" />
							)}
							<div className="space-y-1 break-all">
								<p className="font-semibold">{validationResult.valid ? "Valid syntax" : "Invalid syntax"}</p>
								{validationResult.error && <p className="text-xs font-mono">{validationResult.error}</p>}
							</div>
						</div>
					) : (
						<p className="text-muted-foreground text-xs">
							Validation uses a sample that matches the current apply-on setting.
						</p>
					)}
				</div>
				<div className="flex flex-col items-stretch gap-2 sm:flex-row sm:items-center">
					<Button type="button" variant="secondary" onClick={handleValidate} disabled={isValidating || !activeExpression.trim()} data-testid="guardrails-rule-validate-button">
						{isValidating && <Loader2 className="mr-2 h-4 w-4 animate-spin" />}
						Validate CEL
					</Button>
				</div>
			</div>
		</div>
	);
}
