"use client";

import { Button } from "@/components/ui/button";
import { Form, FormControl, FormField, FormItem, FormLabel, FormMessage } from "@/components/ui/form";
import { Input } from "@/components/ui/input";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import { useEffect, type ReactNode } from "react";
import { useForm, useWatch } from "react-hook-form";
import type { Resolver } from "react-hook-form";
import { z } from "zod";
import { zodResolver } from "@hookform/resolvers/zod";
import { GuardrailRule, CreateGuardrailRuleRequest, UpdateGuardrailRuleRequest } from "@/lib/types/guardrails";
import {
	useCreateGuardrailRuleMutation,
	useGetGuardrailProfilesQuery,
	useLinkGuardrailProfileMutation,
	useUnlinkGuardrailProfileMutation,
	useUpdateGuardrailRuleMutation,
} from "@/lib/store/apis/guardrailsApi";
import { toast } from "sonner";
import { getErrorMessage } from "@/lib/store";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { RuleBuilder } from "./RuleBuilder";
import { MultiSelect } from "@/components/ui/multiSelect";

interface RuleEditorSheetProps {
	open: boolean;
	onOpenChange: (open: boolean) => void;
	editingRule: GuardrailRule | null;
}

function FormSection({
	title,
	description,
	children,
	className = "",
}: {
	title: string;
	description?: string;
	children: ReactNode;
	className?: string;
}) {
	return (
		<section className={`space-y-4 rounded-lg border bg-background p-4 ${className}`.trim()}>
			<div className="space-y-1">
				<h3 className="text-sm font-medium">{title}</h3>
				{description && <p className="text-muted-foreground text-xs">{description}</p>}
			</div>
			{children}
		</section>
	);
}

const formSchema = z.object({
	name: z.string().min(1, "Name is required"),
	description: z.string().default(""),
	enabled: z.boolean(),
	apply_to: z.enum(["input", "output", "both"]),
	action: z.enum(["block", "warn"]),
	sampling_rate: z.coerce.number().min(0).max(100),
	timeout_ms: z.coerce.number().min(0),
	priority: z.coerce.number().int(),
	scope: z.enum(["global", "virtual_key", "team"]),
	scope_id: z.string().default(""),
	block_message: z.string().default(""),
	fail_open: z.boolean(),
	cel_expression: z.string().min(1, "CEL expression is required"),
	profileIds: z.array(z.string()).default([]),
}).superRefine((values, ctx) => {
	if (values.scope !== "global" && !values.scope_id.trim()) {
		ctx.addIssue({
			code: z.ZodIssueCode.custom,
			path: ["scope_id"],
			message: "Scope ID is required when scope is not global",
		});
	}
});

type FormValues = z.infer<typeof formSchema>;

const defaultValues: FormValues = {
	name: "",
	description: "",
	enabled: true,
	apply_to: "both",
	action: "block",
	sampling_rate: 100,
	timeout_ms: 60000,
	priority: 0,
	scope: "global",
	scope_id: "",
	block_message: "",
	fail_open: true,
	cel_expression: "",
	profileIds: [],
};

export function RuleEditorSheet({ open, onOpenChange, editingRule }: RuleEditorSheetProps) {
	const [createRule, { isLoading: isCreating }] = useCreateGuardrailRuleMutation();
	const [updateRule, { isLoading: isUpdating }] = useUpdateGuardrailRuleMutation();
	const [linkProfile] = useLinkGuardrailProfileMutation();
	const [unlinkProfile] = useUnlinkGuardrailProfileMutation();
	
	const { data: profiles } = useGetGuardrailProfilesQuery();

	const form = useForm<FormValues>({
		resolver: zodResolver(formSchema) as Resolver<FormValues>,
		defaultValues,
	});

	const selectedApplyTo = useWatch({ control: form.control, name: "apply_to" });
	const selectedAction = useWatch({ control: form.control, name: "action" });
	const selectedScope = useWatch({ control: form.control, name: "scope" });
	const celExpression = useWatch({ control: form.control, name: "cel_expression" });

	useEffect(() => {
		if (open) {
			if (editingRule) {
				form.reset({
					name: editingRule.name,
					description: editingRule.description,
					enabled: editingRule.enabled,
					apply_to: editingRule.apply_to,
					action: editingRule.action,
					sampling_rate: editingRule.sampling_rate,
					timeout_ms: editingRule.timeout_ms,
					priority: editingRule.priority,
					scope: editingRule.scope,
					scope_id: editingRule.scope_id ?? "",
					block_message: editingRule.block_message,
					fail_open: editingRule.fail_open,
					cel_expression: editingRule.cel_expression,
					profileIds: editingRule.profiles?.map((p) => p.id) || [],
				});
			} else {
				form.reset(defaultValues);
			}
		}
	}, [open, editingRule, form]);

	const syncRuleProfiles = async (ruleId: string, previousIds: string[], nextIds: string[]) => {
		const previous = new Set(previousIds);
		const next = new Set(nextIds);
		const toLink = nextIds.filter((id) => !previous.has(id));
		const toUnlink = previousIds.filter((id) => !next.has(id));

		await Promise.all([
			...toLink.map((profileId) => linkProfile({ ruleId, profileId }).unwrap()),
			...toUnlink.map((profileId) => unlinkProfile({ ruleId, profileId }).unwrap()),
		]);
	};

	const onSubmit = async (values: FormValues) => {
		try {
			const requestBody: CreateGuardrailRuleRequest | UpdateGuardrailRuleRequest = {
				name: values.name,
				description: values.description,
				enabled: values.enabled,
				apply_to: values.apply_to,
				action: values.action,
				sampling_rate: Number(values.sampling_rate),
				timeout_ms: Number(values.timeout_ms),
				priority: Number(values.priority),
				scope: values.scope,
				scope_id: values.scope === "global" ? null : values.scope_id.trim() || null,
				block_message: values.block_message,
				fail_open: values.fail_open,
				cel_expression: values.cel_expression,
			};

			if (editingRule) {
				await updateRule({ id: editingRule.id, data: requestBody }).unwrap();
				await syncRuleProfiles(
					editingRule.id,
					editingRule.profiles?.map((profile) => profile.id) ?? [],
					values.profileIds,
				);
				toast.success("Rule updated successfully");
			} else {
				const createdRule = await createRule(requestBody as CreateGuardrailRuleRequest).unwrap();
				await syncRuleProfiles(createdRule.id, [], values.profileIds);
				toast.success("Rule created successfully");
			}
			onOpenChange(false);
		} catch (error: any) {
			toast.error(getErrorMessage(error));
		}
	};

	const isLoading = isCreating || isUpdating;

	// Options for Apply On
	const applyToOptions = [
		{ value: "input", label: "Input Only", desc: "Evaluate on incoming requests" },
		{ value: "output", label: "Output Only", desc: "Evaluate on outgoing responses" },
		{ value: "both", label: "Both", desc: "Evaluate on both input and output" },
	];

	return (
		<Sheet open={open} onOpenChange={onOpenChange}>
			<SheetContent className="flex w-full flex-col overflow-hidden p-0 sm:max-w-4xl lg:max-w-6xl">
				<div className="flex-1 overflow-y-auto px-6 py-6">
					<SheetHeader className="mb-6">
						<SheetTitle>{editingRule ? "Edit Guardrail Rule" : "Add New Guardrail Rule"}</SheetTitle>
						<SheetDescription>
							Create custom filtering rules using Common Expression Language (CEL) expressions to control when to execute guardrails.
						</SheetDescription>
					</SheetHeader>

					<Form {...form}>
						<form id="rule-form" onSubmit={form.handleSubmit(onSubmit)} className="space-y-6">
							<div className="grid gap-6 lg:grid-cols-[minmax(0,0.92fr)_minmax(0,1.08fr)]">
								<div className="space-y-6">
									<FormSection
										title="Rule details"
										description="Identity and activation state for this guardrail rule."
									>
										<div className="space-y-4">
											<FormField
												control={form.control}
												name="name"
												render={({ field }) => (
													<FormItem>
														<FormLabel>Rule Name <span className="text-destructive">*</span></FormLabel>
														<FormControl>
															<Input className="bg-white dark:bg-input/30" placeholder="e.g. medical-query-rule-v1.1" {...field} />
														</FormControl>
														<FormMessage />
													</FormItem>
												)}
											/>

											<FormField
												control={form.control}
												name="description"
												render={({ field }) => (
													<FormItem>
														<FormLabel>Description</FormLabel>
														<FormControl>
															<Textarea
																className="min-h-28 bg-white dark:bg-input/30"
																placeholder="Short summary of what this rule protects"
																{...field}
															/>
														</FormControl>
														<FormMessage />
													</FormItem>
												)}
											/>

											<FormField
												control={form.control}
												name="enabled"
												render={({ field }) => (
													<FormItem className="flex flex-row items-center justify-between rounded-md border p-4">
														<div className="space-y-0.5 pr-4">
															<FormLabel className="text-base">Enable Rule</FormLabel>
															<div className="text-sm text-muted-foreground">
																Active rules are evaluated for matching requests
															</div>
														</div>
														<FormControl>
															<Switch checked={field.value} onCheckedChange={field.onChange} />
														</FormControl>
													</FormItem>
												)}
											/>
										</div>
									</FormSection>

									<FormSection
										title="Behavior"
										description="Choose when the rule runs, how it behaves, and what scope it applies to."
									>
										<div className="space-y-4">
											<FormField
												control={form.control}
												name="apply_to"
												render={({ field }) => (
													<FormItem>
														<FormLabel>Apply on</FormLabel>
														<div className="grid gap-2 sm:grid-cols-3">
															{applyToOptions.map((opt) => (
																<button
																	key={opt.value}
																	type="button"
																	onClick={() => field.onChange(opt.value)}
																	className={`flex min-h-20 cursor-pointer flex-col rounded-md border p-3 text-left transition-colors ${
																		field.value === opt.value
																			? "border-primary bg-primary/5"
																			: "border-input hover:bg-accent"
																	}`}
																>
																	<div className="mb-1 flex items-center gap-2">
																		<div
																			className={`flex h-4 w-4 items-center justify-center rounded-full border ${
																				field.value === opt.value ? "border-primary" : "border-muted-foreground"
																			}`}
																		>
																			{field.value === opt.value && <div className="h-2 w-2 rounded-full bg-primary" />}
																		</div>
																		<span className="text-sm font-semibold">{opt.label}</span>
																	</div>
																	<span className="pl-6 text-xs text-muted-foreground">{opt.desc}</span>
																</button>
															))}
														</div>
														<FormMessage />
													</FormItem>
												)}
											/>

											<div className="grid gap-4 sm:grid-cols-2">
												<FormField
													control={form.control}
													name="action"
													render={({ field }) => (
														<FormItem>
															<FormLabel>Action</FormLabel>
															<FormControl>
																<Select value={field.value} onValueChange={field.onChange}>
																	<SelectTrigger className="w-full bg-white dark:bg-input/30" data-testid="guardrails-rule-action-select">
																		<SelectValue placeholder="Select action" />
																	</SelectTrigger>
																	<SelectContent>
																		<SelectItem value="block">Block</SelectItem>
																		<SelectItem value="warn">Warn</SelectItem>
																	</SelectContent>
																</Select>
															</FormControl>
															<FormMessage />
														</FormItem>
													)}
												/>

												<FormField
													control={form.control}
													name="priority"
													render={({ field }) => (
														<FormItem>
															<FormLabel>Priority</FormLabel>
															<FormControl>
																<Input className="bg-white dark:bg-input/30" type="number" min="0" step="1" data-testid="guardrails-rule-priority-input" {...field} />
															</FormControl>
															<div className="text-xs text-muted-foreground">Lower values run first within the same scope</div>
															<FormMessage />
														</FormItem>
													)}
												/>
											</div>

											<div className="grid gap-4 sm:grid-cols-2">
												<FormField
													control={form.control}
													name="scope"
													render={({ field }) => (
														<FormItem>
															<FormLabel>Scope</FormLabel>
															<FormControl>
																<Select
																	value={field.value}
																	onValueChange={(value) => {
																		field.onChange(value);
																		if (value === "global") {
																			form.setValue("scope_id", "");
																		}
																	}}
																>
																	<SelectTrigger className="w-full bg-white dark:bg-input/30" data-testid="guardrails-rule-scope-select">
																		<SelectValue placeholder="Select scope" />
																	</SelectTrigger>
																	<SelectContent>
																		<SelectItem value="global">Global</SelectItem>
																		<SelectItem value="virtual_key">Virtual key</SelectItem>
																		<SelectItem value="team">Team</SelectItem>
																	</SelectContent>
																</Select>
															</FormControl>
															<FormMessage />
														</FormItem>
													)}
												/>

												{selectedScope !== "global" ? (
													<FormField
														control={form.control}
														name="scope_id"
														render={({ field }) => (
														<FormItem>
															<FormLabel>Scope ID</FormLabel>
															<FormControl>
																<Input
																	className="bg-white dark:bg-input/30"
																	{...field}
																	value={field.value ?? ""}
																	data-testid="guardrails-rule-scope-id-input"
																	placeholder={selectedScope === "virtual_key" ? "Virtual key ID" : "Team ID"}
																	/>
																</FormControl>
																<FormMessage />
															</FormItem>
														)}
													/>
												) : (
													<div className="rounded-md border border-dashed px-4 py-3 text-xs text-muted-foreground">
														Global scope does not require a scope ID.
													</div>
												)}
											</div>
										</div>
									</FormSection>

									<FormSection
										title="Profiles"
										description="Attach one or more guardrail profiles. The rule can also run without profiles."
									>
										<FormField
													control={form.control}
													name="profileIds"
													render={({ field }) => (
														<FormItem>
															<FormControl>
																<MultiSelect
																	options={(profiles || []).map((p) => ({
																		label: p.name,
																		value: p.id,
																	}))}
																	onValueChange={field.onChange}
																	defaultValue={field.value}
																	placeholder="Select profiles"
																	className="w-full justify-start bg-white text-foreground hover:bg-white dark:bg-input/30 dark:hover:bg-input/30"
																	data-testid="guardrails-rule-profiles-select"
																/>
															</FormControl>
															<FormMessage />
												</FormItem>
											)}
										/>
									</FormSection>
								</div>

								<div className="space-y-6">
									<FormSection
										title="Expression"
										description="Write the CEL expression used to evaluate the request or response."
									>
										<RuleBuilder value={celExpression} onChange={(val) => form.setValue("cel_expression", val)} applyTo={selectedApplyTo} />
									</FormSection>

									<FormSection
										title="Execution"
										description="Tune execution cost and the response sent when the rule blocks traffic."
									>
										<div className="space-y-4">
											<div className="grid gap-4 sm:grid-cols-2">
												<FormField
													control={form.control}
													name="sampling_rate"
													render={({ field }) => (
														<FormItem>
															<FormLabel>Sampling Rate (%) <span className="text-destructive">*</span></FormLabel>
															<FormControl>
																<Input className="bg-white dark:bg-input/30" type="number" min="0" max="100" step="1" {...field} />
															</FormControl>
															<div className="text-xs text-muted-foreground">Percentage of matching requests to process</div>
															<FormMessage />
														</FormItem>
													)}
												/>

												<FormField
													control={form.control}
													name="timeout_ms"
													render={({ field }) => (
														<FormItem>
															<FormLabel>Timeout (ms) <span className="text-destructive">*</span></FormLabel>
															<FormControl>
																<Input className="bg-white dark:bg-input/30" type="number" min="0" {...field} />
															</FormControl>
															<div className="text-xs text-muted-foreground">Max wait time for guardrail execution</div>
															<FormMessage />
														</FormItem>
													)}
												/>
											</div>

											<FormField
												control={form.control}
												name="fail_open"
												render={({ field }) => (
													<FormItem className="flex flex-row items-center justify-between rounded-md border p-4">
														<div className="space-y-0.5 pr-4">
															<FormLabel className="text-base">Fail Open</FormLabel>
															<div className="text-sm text-muted-foreground">
																Allow the request to continue if guardrail evaluation errors
															</div>
														</div>
														<FormControl>
															<Switch checked={field.value} onCheckedChange={field.onChange} />
														</FormControl>
													</FormItem>
												)}
											/>

											{selectedAction === "block" && (
												<FormField
													control={form.control}
													name="block_message"
													render={({ field }) => (
														<FormItem>
															<FormLabel>Block Message</FormLabel>
															<FormControl>
																<Textarea
																	className="min-h-28 bg-white dark:bg-input/30"
																	{...field}
																	data-testid="guardrails-rule-block-message-input"
																	placeholder="Explain why the request was blocked"
																/>
															</FormControl>
															<div className="text-xs text-muted-foreground">
																Shown to clients when this rule blocks a request
															</div>
															<FormMessage />
														</FormItem>
													)}
												/>
											)}
										</div>
									</FormSection>
								</div>
							</div>

						</form>
					</Form>
				</div>
				<div className="flex shrink-0 items-center justify-end gap-2 border-t p-4 px-6">
					<Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
						Cancel
					</Button>
					<Button type="submit" form="rule-form" disabled={isLoading} data-testid="guardrails-rule-save-button">
						{isLoading ? "Saving..." : "Save Rule"}
					</Button>
				</div>
			</SheetContent>
		</Sheet>
	);
}
