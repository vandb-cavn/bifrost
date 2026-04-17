"use client";

import { GuardrailRule } from "@/lib/types/guardrails";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import {
	AlertDialog,
	AlertDialogAction,
	AlertDialogCancel,
	AlertDialogContent,
	AlertDialogDescription,
	AlertDialogFooter,
	AlertDialogHeader,
	AlertDialogTitle,
} from "@/components/ui/alertDialog";
import { Edit, Trash2 } from "lucide-react";
import { useState } from "react";
import { useDeleteGuardrailRuleMutation } from "@/lib/store/apis/guardrailsApi";
import { toast } from "sonner";
import { getErrorMessage } from "@/lib/store";
import { formatDistanceToNow } from "date-fns";

interface RulesTableProps {
	rules: GuardrailRule[] | undefined;
	isLoading: boolean;
	onEdit: (rule: GuardrailRule) => void;
	canDelete?: boolean;
}

function renderScope(rule: GuardrailRule) {
	if (rule.scope === "global") {
		return "global";
	}
	return `${rule.scope}:${rule.scope_id ?? "missing"}`;
}

function renderProfileSummary(rule: GuardrailRule) {
	const count = rule.profiles?.length ?? 0;
	if (count === 0) return "CEL only";
	if (count === 1) return rule.profiles?.[0]?.name ?? "1 profile";
	return `${count} profiles`;
}

function renderStatus(enabled: boolean) {
	return enabled ? <Badge>Enabled</Badge> : <Badge variant="secondary">Disabled</Badge>;
}

function renderAction(action: GuardrailRule["action"]) {
	return action === "block" ? <Badge>Block</Badge> : <Badge variant="outline">Warn</Badge>;
}

export function RulesTable({
	rules,
	isLoading,
	onEdit,
	canDelete = false,
}: RulesTableProps) {
	const [deleteRuleId, setDeleteRuleId] = useState<string | null>(null);
	const [deleteGuardrailRule, { isLoading: isDeleting }] = useDeleteGuardrailRuleMutation();

	const handleDelete = async () => {
		if (!canDelete || !deleteRuleId) return;

		try {
			await deleteGuardrailRule(deleteRuleId).unwrap();
			toast.success("Guardrail rule deleted successfully");
			setDeleteRuleId(null);
		} catch (error: any) {
			toast.error(getErrorMessage(error));
		}
	};

	if (isLoading) {
		return (
			<div className="rounded-sm border bg-card">
				<Table>
					<TableHeader>
						<TableRow>
							<TableHead>Name</TableHead>
							<TableHead>Apply To</TableHead>
							<TableHead>Action</TableHead>
							<TableHead>Priority</TableHead>
							<TableHead>Scope</TableHead>
							<TableHead>Profiles</TableHead>
							<TableHead>Status</TableHead>
							<TableHead>Updated</TableHead>
							<TableHead className="text-right">Actions</TableHead>
						</TableRow>
					</TableHeader>
					<TableBody>
						{[...Array(3)].map((_, i) => (
							<TableRow key={i}>
								<TableCell colSpan={9} className="h-10">
									<div className="bg-muted h-2 w-32 animate-pulse rounded" />
								</TableCell>
							</TableRow>
						))}
					</TableBody>
				</Table>
			</div>
		);
	}

	const sortedRules = rules ? [...rules].sort((a, b) => a.priority - b.priority) : [];
	const ruleToDelete = sortedRules.find((r) => r.id === deleteRuleId);

	return (
		<>
			<div className="overflow-hidden rounded-sm border bg-card">
				<Table>
					<TableHeader>
						<TableRow className="bg-muted/50">
							<TableHead className="font-semibold">Name</TableHead>
							<TableHead className="font-semibold">Apply To</TableHead>
							<TableHead className="font-semibold">Action</TableHead>
							<TableHead className="font-semibold">Priority</TableHead>
							<TableHead className="font-semibold">Scope</TableHead>
							<TableHead className="font-semibold">Profiles</TableHead>
							<TableHead className="font-semibold">Status</TableHead>
							<TableHead className="font-semibold">Updated</TableHead>
							<TableHead className="text-right font-semibold">Actions</TableHead>
						</TableRow>
					</TableHeader>
					<TableBody>
						{sortedRules.length === 0 ? (
							<TableRow>
								<TableCell colSpan={9} className="h-24 text-center">
									<span className="text-muted-foreground text-sm">No guardrail rules found.</span>
								</TableCell>
							</TableRow>
						) : (
							sortedRules.map((rule) => (
								<TableRow key={rule.id} data-testid={`guardrails-rule-row-${rule.id}`} className="hover:bg-muted/50 transition-colors">
									<TableCell className="font-medium">
										<div className="flex max-w-xs flex-col gap-1">
											<span className="truncate">{rule.name}</span>
											<span className="text-muted-foreground truncate text-sm">{rule.description || "-"}</span>
										</div>
									</TableCell>
									<TableCell className="capitalize">{rule.apply_to}</TableCell>
									<TableCell>{renderAction(rule.action)}</TableCell>
									<TableCell>{rule.priority}</TableCell>
									<TableCell>{renderScope(rule)}</TableCell>
									<TableCell>{renderProfileSummary(rule)}</TableCell>
									<TableCell>{renderStatus(rule.enabled)}</TableCell>
									<TableCell>{formatDistanceToNow(new Date(rule.updated_at), { addSuffix: true })}</TableCell>
									<TableCell className="text-right" onClick={(e) => e.stopPropagation()}>
										<div className="flex items-center justify-end gap-2">
											<Button
												variant="ghost"
												size="sm"
												onClick={() => onEdit(rule)}
												aria-label="Edit guardrail rule"
												data-testid={`guardrails-rule-edit-${rule.id}`}
											>
												<Edit className="h-4 w-4" />
											</Button>
											{canDelete && (
												<Button
													variant="ghost"
													size="sm"
													onClick={() => setDeleteRuleId(rule.id)}
													aria-label="Delete guardrail rule"
													data-testid={`guardrails-rule-delete-${rule.id}`}
												>
													<Trash2 className="h-4 w-4 text-destructive" />
												</Button>
											)}
										</div>
									</TableCell>
								</TableRow>
							))
						)}
					</TableBody>
				</Table>
			</div>

			<AlertDialog open={!!deleteRuleId} onOpenChange={(open) => !open && setDeleteRuleId(null)}>
				<AlertDialogContent>
					<AlertDialogHeader>
						<AlertDialogTitle>Delete Guardrail Rule</AlertDialogTitle>
						<AlertDialogDescription>
							Are you sure you want to delete &quot;{ruleToDelete?.name}&quot;? This action cannot be undone.
						</AlertDialogDescription>
					</AlertDialogHeader>
					<AlertDialogFooter>
						<AlertDialogCancel disabled={isDeleting}>Cancel</AlertDialogCancel>
						<AlertDialogAction onClick={handleDelete} disabled={isDeleting} className="bg-destructive hover:bg-destructive/90">
							{isDeleting ? "Deleting..." : "Delete"}
						</AlertDialogAction>
					</AlertDialogFooter>
				</AlertDialogContent>
			</AlertDialog>
		</>
	);
}
