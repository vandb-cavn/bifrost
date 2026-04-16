"use client";

import { useValidateGuardrailRuleMutation } from "@/lib/store/apis/guardrailsApi";
import { Button } from "@/components/ui/button";
import { CodeEditor } from "@/components/ui/codeEditor";
import { Loader2, CheckCircle, XCircle, Copy } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";

interface RuleBuilderProps {
	value: string;
	onChange: (val: string) => void;
}

export function RuleBuilder({ value, onChange }: RuleBuilderProps) {
	const [validateRule, { isLoading: isValidating }] = useValidateGuardrailRuleMutation();
	const [validationResult, setValidationResult] = useState<{
		valid: boolean;
		result?: boolean;
		error?: string;
	} | null>(null);

	const handleValidate = async () => {
		try {
			// Provide a dummy sample conforming to input scheme to validate syntax correctly.
			const res = await validateRule({
				cel_expression: value,
				sample: {
					messages: [{ role: "user", content: "Test message" }],
					model: "gpt-4",
				},
			}).unwrap();

			setValidationResult(res);
			if (res.valid) {
				toast.success("Expression is valid");
			} else {
				toast.error(`Invalid Expression: ${res.error}`);
			}
		} catch (e: any) {
			toast.error("An error occurred during validation");
			setValidationResult({ valid: false, error: e.message });
		}
	};

	const copyToClipboard = () => {
		navigator.clipboard.writeText(value);
		toast.success("Copied to clipboard");
	};

	return (
		<div className="rounded-lg border p-4 bg-muted/20">
			<Tabs defaultValue="editor" className="w-full">
				<TabsList className="grid w-full grid-cols-2">
					<TabsTrigger value="builder" disabled>Visual Builder (Coming Soon)</TabsTrigger>
					<TabsTrigger value="editor">CEL Editor</TabsTrigger>
				</TabsList>
				
				<TabsContent value="editor" className="space-y-4 pt-4">
					<div className="flex justify-between items-center bg-card border border-b-0 rounded-t-md p-2">
						<span className="text-xs font-semibold px-2 text-muted-foreground">CEL Expression Preview</span>
						<Button type="button" variant="ghost" size="sm" onClick={copyToClipboard} className="h-6">
							<Copy className="h-3 w-3 mr-1" />
							<span className="text-xs">Copy</span>
						</Button>
					</div>
					<div className="min-h-[120px] rounded-b-md border overflow-hidden -mt-4 border-t-0">
						<CodeEditor
							value={value}
							onChange={onChange}
							language="plaintext" // CEL isn't typically supported, plaintext is fallback
							className="h-full min-h-[120px] w-full"
						/>
					</div>

					<div className="flex items-start justify-between">
						<div className="flex-1 mr-4">
							{validationResult && (
								<div className={`p-3 rounded flex items-start gap-2 text-sm ${
									validationResult.valid 
										? 'bg-green-500/10 text-green-600 dark:text-green-400' 
										: 'bg-destructive/10 text-destructive'
								}`}>
									{validationResult.valid ? (
										<CheckCircle className="h-4 w-4 mt-0.5 shrink-0" />
									) : (
										<XCircle className="h-4 w-4 mt-0.5 shrink-0" />
									)}
									<div className="break-all">
										<p className="font-semibold">{validationResult.valid ? 'Valid Syntax' : 'Invalid Syntax'}</p>
										{validationResult.error && (
											<p className="text-xs mt-1 font-mono">{validationResult.error}</p>
										)}
									</div>
								</div>
							)}
						</div>
						<Button 
							type="button" 
							variant="secondary" 
							onClick={handleValidate} 
							disabled={isValidating || !value.trim()}
						>
							{isValidating && <Loader2 className="mr-2 h-4 w-4 animate-spin" />}
							Validate CEL
						</Button>
					</div>
				</TabsContent>
			</Tabs>
		</div>
	);
}
