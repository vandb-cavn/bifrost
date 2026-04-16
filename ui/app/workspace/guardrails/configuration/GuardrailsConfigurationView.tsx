"use client";

import { RbacOperation, RbacResource, useRbac } from "@/app/_fallbacks/enterprise/lib/contexts/rbacContext";
import { Button } from "@/components/ui/button";
import { Plus } from "lucide-react";
import { useState } from "react";
import { GuardrailRule } from "@/lib/types/guardrails";
import { useGetGuardrailRulesQuery } from "@/lib/store/apis/guardrailsApi";
import { RulesTable } from "./RulesTable";
import { RuleEditorSheet } from "./RuleEditorSheet";

export function GuardrailsConfigurationView() {
	const [dialogOpen, setDialogOpen] = useState(false);
	const [editingRule, setEditingRule] = useState<GuardrailRule | null>(null);

	// Permissions
	const canCreate = useRbac(RbacResource.Guardrails, RbacOperation.Create);
	const canDelete = useRbac(RbacResource.Guardrails, RbacOperation.Delete);

	// API
	const { data: rules, isLoading } = useGetGuardrailRulesQuery(undefined, {
		pollingInterval: 5000,
	});

	const handleCreateNew = () => {
		setEditingRule(null);
		setDialogOpen(true);
	};

	const handleEdit = (rule: GuardrailRule) => {
		setEditingRule(rule);
		setDialogOpen(true);
	};

	const handleDialogOpenChange = (open: boolean) => {
		setDialogOpen(open);
		if (!open) {
			setEditingRule(null);
		}
	};

	return (
		<div className="space-y-4">
			{/* Header */}
			<div className="flex items-center justify-between">
				<div>
					<h1 className="text-foreground text-lg font-semibold">Guardrail Rules</h1>
					<p className="text-muted-foreground text-sm">Configure guardrail rules to control when to execute guardrails.</p>
				</div>
				<div className="flex items-center gap-2">
					{canCreate && (
						<Button
							data-testid="create-guardrail-rule-btn"
							onClick={handleCreateNew}
							disabled={isLoading}
							className="gap-2"
						>
							<Plus className="h-4 w-4" />
							<span className="hidden sm:inline">Add New Rule</span>
						</Button>
					)}
				</div>
			</div>

			<RulesTable
				rules={rules}
				isLoading={isLoading}
				onEdit={handleEdit}
				canDelete={canDelete}
			/>

			<RuleEditorSheet open={dialogOpen} onOpenChange={handleDialogOpenChange} editingRule={editingRule} />
		</div>
	);
}
