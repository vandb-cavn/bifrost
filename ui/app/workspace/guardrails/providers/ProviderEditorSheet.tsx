"use client";

import { Button } from "@/components/ui/button";
import { Form, FormControl, FormField, FormItem, FormLabel, FormMessage } from "@/components/ui/form";
import { Input } from "@/components/ui/input";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { Switch } from "@/components/ui/switch";
import { useForm } from "react-hook-form";
import { GuardrailProfile } from "@/lib/types/guardrails";
import { useCreateGuardrailProfileMutation, useUpdateGuardrailProfileMutation } from "@/lib/store/apis/guardrailsApi";
import { useEffect, useState } from "react";
import { toast } from "sonner";
import { getErrorMessage } from "@/lib/store";
import { CodeEditor } from "@/components/ui/codeEditor";

interface ProviderEditorSheetProps {
	open: boolean;
	onOpenChange: (open: boolean) => void;
	editingProfile: GuardrailProfile | null;
	selectedProviderId: string;
}

interface FormValues {
	name: string;
	enabled: boolean;
	configStr: string;
}

export function ProviderEditorSheet({ open, onOpenChange, editingProfile, selectedProviderId }: ProviderEditorSheetProps) {
	const [createProfile, { isLoading: isCreating }] = useCreateGuardrailProfileMutation();
	const [updateProfile, { isLoading: isUpdating }] = useUpdateGuardrailProfileMutation();
	const [configError, setConfigError] = useState<string | null>(null);

	const form = useForm<FormValues>({
		defaultValues: {
			name: "",
			enabled: true,
			configStr: "{\n  \n}",
		},
	});

	useEffect(() => {
		if (open) {
			setConfigError(null);
			if (editingProfile) {
				form.reset({
					name: editingProfile.name,
					enabled: editingProfile.enabled,
					configStr: editingProfile.config ? JSON.stringify(editingProfile.config, null, 2) : "{\n  \n}",
				});
			} else {
				form.reset({
					name: "",
					enabled: true,
					configStr: "{\n  \n}",
				});
			}
		}
	}, [open, editingProfile, form]);

	const onSubmit = async (values: FormValues) => {
		let parsedConfig = {};
		try {
			if (values.configStr.trim()) {
				parsedConfig = JSON.parse(values.configStr);
			}
			setConfigError(null);
		} catch (e) {
			setConfigError("Invalid JSON configuration");
			return;
		}

		try {
			const requestBody = {
				name: values.name,
				provider_name: selectedProviderId,
				enabled: values.enabled,
				config: parsedConfig,
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
			toast.error(getErrorMessage(error));
		}
	};

	const isLoading = isCreating || isUpdating;

	return (
		<Sheet open={open} onOpenChange={onOpenChange}>
			<SheetContent className="flex w-full flex-col sm:max-w-xl p-0 overflow-hidden">
				<div className="flex-1 overflow-y-auto px-6 py-6">
					<SheetHeader className="mb-6">
						<SheetTitle>{editingProfile ? "Edit Configuration" : "Add New Configuration"}</SheetTitle>
						<SheetDescription>
							Provider settings for {selectedProviderId} guardrails.
						</SheetDescription>
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
											<Input placeholder="e.g. Free user scanning" {...field} />
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
											<Switch
												checked={field.value}
												onCheckedChange={field.onChange}
											/>
										</FormControl>
									</FormItem>
								)}
							/>

							<FormField
								control={form.control}
								name="configStr"
								render={({ field }) => (
									<FormItem>
										<FormLabel>Provider Config JSON <span className="text-destructive">*</span></FormLabel>
										<FormControl>
											<div className={`min-h-[200px] border rounded-md overflow-hidden ${configError ? 'border-destructive' : ''}`}>
												<CodeEditor
													value={field.value}
													onChange={field.onChange}
													language="json"
													className="h-full min-h-[200px] w-full"
												/>
											</div>
										</FormControl>
										{configError && (
											<p className="text-xs font-medium text-destructive">{configError}</p>
										)}
										<div className="text-xs text-muted-foreground mt-2">
											Enter the specific configuration values required by this provider in JSON format.
										</div>
										<FormMessage />
									</FormItem>
								)}
							/>
						</form>
					</Form>
				</div>
				<div className="flex shrink-0 items-center justify-end gap-2 border-t p-4 px-6">
					<Button variant="outline" onClick={() => onOpenChange(false)}>
						Cancel
					</Button>
					<Button type="submit" form="profile-form" disabled={isLoading}>
						{isLoading ? "Saving..." : "Save Configuration"}
					</Button>
				</div>
			</SheetContent>
		</Sheet>
	);
}
