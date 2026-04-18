"use client";

import { zodResolver } from "@hookform/resolvers/zod";
import { Button } from "@/components/ui/button";
import { CodeEditor } from "@/components/ui/codeEditor";
import { Form, FormControl, FormField, FormItem, FormLabel, FormMessage } from "@/components/ui/form";
import { Input } from "@/components/ui/input";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { Switch } from "@/components/ui/switch";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Textarea } from "@/components/ui/textarea";
import { useEffect, useState } from "react";
import { useForm, useWatch, type Resolver } from "react-hook-form";
import { z } from "zod";
import { toast } from "sonner";
import { getErrorMessage } from "@/lib/store";
import { GuardrailProfile, GuardrailProviderName } from "@/lib/types/guardrails";
import {
	useCreateGuardrailProfileMutation,
	useUpdateGuardrailProfileMutation,
} from "@/lib/store/apis/guardrailsApi";
import {
	guardrailProviderLabels,
	getDefaultProfileConfig,
	mergeProfileConfigDefaults,
	parseProfileConfigJson,
	stringifyProfileConfig,
} from "../shared/profileConfig";

const formSchema = z.object({
	name: z.string().min(1, "Name is required"),
	enabled: z.boolean(),
	timeout_ms: z.coerce.number().int().min(1000, "Timeout must be at least 1000ms"),
	config: z.record(z.string(), z.unknown()),
});

type FormValues = z.infer<typeof formSchema>;

interface ProviderEditorSheetProps {
	open: boolean;
	onOpenChange: (open: boolean) => void;
	editingProfile: GuardrailProfile | null;
	selectedProviderId: GuardrailProviderName;
}

type ConfigFieldProps = {
	control: any;
	provider: GuardrailProviderName;
	name: string;
	label: string;
	description?: string;
	type?: "text" | "number";
	step?: string;
	multiline?: boolean;
};

function ConfigField({
	control,
	provider,
	name,
	label,
	description,
	type = "text",
	step,
	multiline = false,
}: ConfigFieldProps) {
	return (
		<FormField
			control={control}
			name={`config.${name}` as any}
			render={({ field }) => (
				<FormItem>
					<FormLabel>{label}</FormLabel>
					<FormControl>
						{multiline ? (
							<Textarea
								data-testid={`guardrails-profile-${provider}-${name}`}
								placeholder={label}
								value={(field.value as string | undefined) ?? ""}
								onChange={field.onChange}
								className="min-h-[120px] bg-white dark:bg-input/30"
							/>
						) : (
							<Input
								data-testid={`guardrails-profile-${provider}-${name}`}
								type={type}
								step={step}
								placeholder={label}
								value={field.value ?? ""}
								className="bg-white dark:bg-input/30"
								onChange={(event) => {
									if (type === "number") {
										field.onChange(event.target.value === "" ? undefined : Number(event.target.value));
										return;
									}
									field.onChange(event.target.value);
								}}
							/>
						)}
					</FormControl>
					{description && <div className="text-xs text-muted-foreground">{description}</div>}
					<FormMessage />
				</FormItem>
			)}
		/>
	);
}

export function ProviderEditorSheet({ open, onOpenChange, editingProfile, selectedProviderId }: ProviderEditorSheetProps) {
	const [createProfile, { isLoading: isCreating }] = useCreateGuardrailProfileMutation();
	const [updateProfile, { isLoading: isUpdating }] = useUpdateGuardrailProfileMutation();
	const [jsonMode, setJsonMode] = useState(false);
	const [configJson, setConfigJson] = useState("");
	const [jsonError, setJsonError] = useState<string | null>(null);

	const form = useForm<FormValues>({
		resolver: zodResolver(formSchema) as Resolver<FormValues, any, FormValues>,
		defaultValues: {
			name: "",
			enabled: true,
			config: getDefaultProfileConfig(selectedProviderId),
		},
	});

	const watchedConfig = useWatch({ control: form.control, name: "config" });

	useEffect(() => {
		if (!open) {
			return;
		}

		const config = mergeProfileConfigDefaults(selectedProviderId, editingProfile?.config ?? {});
		form.reset({
			name: editingProfile?.name ?? "",
			enabled: editingProfile?.enabled ?? true,
			timeout_ms: editingProfile?.timeout_ms ?? 10000,
			config,
		});
		setConfigJson(stringifyProfileConfig(config));
		setJsonError(null);
		setJsonMode(false);
	}, [open, editingProfile, selectedProviderId, form]);

	useEffect(() => {
		if (!jsonMode) {
			setConfigJson(stringifyProfileConfig(mergeProfileConfigDefaults(selectedProviderId, watchedConfig)));
		}
	}, [jsonMode, selectedProviderId, watchedConfig]);

	const handleJsonChange = (value: string) => {
		setConfigJson(value);
		try {
			const parsed = mergeProfileConfigDefaults(selectedProviderId, parseProfileConfigJson(value));
			setJsonError(null);
			form.setValue("config", parsed, { shouldDirty: true, shouldValidate: true });
		} catch (error) {
			setJsonError(error instanceof Error ? error.message : "Invalid JSON configuration");
		}
	};

	const buildRequestConfig = (values: FormValues) => {
		if (jsonMode) {
			return mergeProfileConfigDefaults(selectedProviderId, parseProfileConfigJson(configJson));
		}
		return mergeProfileConfigDefaults(selectedProviderId, values.config);
	};

	const onSubmit = async (values: FormValues) => {
		try {
			const requestBody = {
				name: values.name,
				provider_name: selectedProviderId,
				enabled: values.enabled,
				timeout_ms: Number(values.timeout_ms),
				config: buildRequestConfig(values),
			};

			if (editingProfile) {
				await updateProfile({ id: editingProfile.id, data: requestBody }).unwrap();
				toast.success("Configuration updated successfully");
			} else {
				await createProfile(requestBody).unwrap();
				toast.success("Configuration created successfully");
			}
			onOpenChange(false);
		} catch (error: any) {
			if (jsonMode && error instanceof Error) {
				setJsonError(error.message);
			}
			toast.error(getErrorMessage(error));
		}
	};

	const isLoading = isCreating || isUpdating;

	return (
		<Sheet open={open} onOpenChange={onOpenChange}>
			<SheetContent className="flex w-full flex-col overflow-hidden p-0 sm:max-w-2xl">
				<div className="flex-1 overflow-y-auto px-6 py-6">
					<SheetHeader className="mb-6">
						<SheetTitle>{editingProfile ? "Edit Configuration" : "Add New Configuration"}</SheetTitle>
						<SheetDescription>Provider settings for {guardrailProviderLabels[selectedProviderId]} guardrails.</SheetDescription>
					</SheetHeader>

					<Form {...form}>
						<form id="profile-form" onSubmit={form.handleSubmit(onSubmit)} className="space-y-6">
								<FormField
									control={form.control}
									name="name"
									render={({ field }) => (
										<FormItem>
											<FormLabel>Configuration Name <span className="text-destructive">*</span></FormLabel>
											<FormControl>
											<Input className="bg-white dark:bg-input/30" data-testid="guardrails-profile-name-input" placeholder="e.g. Free user scanning" {...field} />
											</FormControl>
											<FormMessage />
										</FormItem>
									)}
							/>

							<FormField
								control={form.control}
								name="enabled"
								render={({ field }) => (
									<FormItem className="flex flex-row items-center justify-between rounded-lg border p-4">
										<div className="space-y-0.5">
											<FormLabel className="text-base">Enable Configuration</FormLabel>
											<div className="text-sm text-muted-foreground">
												This configuration will be available for use in rules
											</div>
										</div>
										<FormControl>
											<Switch checked={field.value} onCheckedChange={field.onChange} data-testid="guardrails-profile-enabled-switch" />
										</FormControl>
									</FormItem>
								)}
							/>

							<FormField
								control={form.control}
								name="timeout_ms"
								render={({ field }) => (
									<FormItem>
										<FormLabel>Timeout (ms)</FormLabel>
										<FormControl>
											<Input
												type="number"
												min="1000"
												step="500"
												className="bg-white dark:bg-input/30"
												data-testid="guardrails-profile-timeout-input"
												{...field}
											/>
										</FormControl>
										<div className="text-xs text-muted-foreground">
											Max wait time for this provider to respond. Default: 10,000ms (10s).
										</div>
										<FormMessage />
									</FormItem>
								)}
							/>

							<Tabs
								value={jsonMode ? "json" : "form"}
								onValueChange={(value) => setJsonMode(value === "json")}
								className="space-y-4"
							>
								<TabsList className="grid w-full grid-cols-2">
									<TabsTrigger value="form">Structured Form</TabsTrigger>
									<TabsTrigger value="json">Advanced JSON</TabsTrigger>
								</TabsList>

								<TabsContent value="form" className="space-y-4 pt-1">
									<div className="space-y-4 rounded-lg border bg-card p-4">
										<div className="text-sm font-medium">{guardrailProviderLabels[selectedProviderId]} settings</div>

										{selectedProviderId === "bedrock" && (
											<div className="grid gap-4 md:grid-cols-3">
												<ConfigField
													control={form.control}
													provider={selectedProviderId}
													name="endpoint"
													label="Endpoint"
												/>
												<ConfigField
													control={form.control}
													provider={selectedProviderId}
													name="guardrail_id"
													label="Guardrail ID"
												/>
												<ConfigField
													control={form.control}
													provider={selectedProviderId}
													name="version"
													label="Version"
												/>
											</div>
										)}

										{selectedProviderId === "azure" && (
											<div className="grid gap-4 md:grid-cols-3">
												<ConfigField control={form.control} provider={selectedProviderId} name="endpoint" label="Endpoint" />
												<ConfigField control={form.control} provider={selectedProviderId} name="api_key" label="API Key" />
												<ConfigField
													control={form.control}
													provider={selectedProviderId}
													name="severity_threshold"
													label="Severity Threshold"
													type="number"
													step="1"
													description="Requests at or above this severity are blocked"
												/>
											</div>
										)}

										{selectedProviderId === "grayswan" && (
											<div className="grid gap-4 md:grid-cols-3">
												<ConfigField control={form.control} provider={selectedProviderId} name="endpoint" label="Endpoint" />
												<ConfigField control={form.control} provider={selectedProviderId} name="api_key" label="API Key" />
												<ConfigField
													control={form.control}
													provider={selectedProviderId}
													name="score_threshold"
													label="Score Threshold"
													type="number"
													step="0.1"
												/>
											</div>
										)}

										{selectedProviderId === "patronus_ai" && (
											<div className="grid gap-4 md:grid-cols-3">
												<ConfigField control={form.control} provider={selectedProviderId} name="endpoint" label="Endpoint" />
												<ConfigField control={form.control} provider={selectedProviderId} name="api_key" label="API Key" />
												<ConfigField control={form.control} provider={selectedProviderId} name="evaluator" label="Evaluator" />
											</div>
										)}

										{selectedProviderId === "model_armor" && (
											<div className="grid gap-4 md:grid-cols-2">
												<ConfigField control={form.control} provider={selectedProviderId} name="project_id" label="Project ID" />
												<ConfigField control={form.control} provider={selectedProviderId} name="location" label="Location" />
												<ConfigField control={form.control} provider={selectedProviderId} name="template_id" label="Template ID" />
												<ConfigField
													control={form.control}
													provider={selectedProviderId}
													name="credentials_json"
													label="Credentials JSON"
													multiline
													description="Paste a base64-encoded Google credentials JSON string"
												/>
											</div>
										)}
									</div>
								</TabsContent>

								<TabsContent value="json" className="space-y-3 pt-1">
									<div className="rounded-lg border bg-card p-4">
										<div className="min-h-[240px] overflow-hidden rounded-md border">
											<CodeEditor
												code={configJson}
												onChange={handleJsonChange}
												lang="json"
												className="h-full min-h-[240px] w-full"
											/>
										</div>
										{jsonError && <p className="mt-3 text-xs font-medium text-destructive">{jsonError}</p>}
									</div>
								</TabsContent>
							</Tabs>
						</form>
					</Form>
				</div>
				<div className="flex shrink-0 items-center justify-end gap-2 border-t p-4 px-6">
					<Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
						Cancel
					</Button>
					<Button type="submit" form="profile-form" disabled={isLoading} data-testid="guardrails-profile-save-button">
						{isLoading ? "Saving..." : "Save Configuration"}
					</Button>
				</div>
			</SheetContent>
		</Sheet>
	);
}
