"use client";

import { GuardrailRule } from "@/lib/types/guardrails";
import { Button } from "@/components/ui/button";
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
import { Switch } from "@/components/ui/switch";

interface RulesTableProps {
	rules: GuardrailRule[] | undefined;
	isLoading: boolean;
	onEdit: (rule: GuardrailRule) => void;
	canDelete?: boolean;
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
							<TableHead>Rule Name</TableHead>
							<TableHead>Description</TableHead>
							<TableHead>Apply To</TableHead>
							<TableHead>Sampling Rate</TableHead>
							<TableHead>Status</TableHead>
							<TableHead className="text-right">Actions</TableHead>
						</TableRow>
					</TableHeader>
					<TableBody>
						{[...Array(3)].map((_, i) => (
							<TableRow key={i}>
								<TableCell colSpan={6} className="h-10">
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
							<TableHead className="font-semibold">Rule Name</TableHead>
							<TableHead className="font-semibold">Description</TableHead>
							<TableHead className="font-semibold">Apply To</TableHead>
							<TableHead className="font-semibold">Sampling Rate</TableHead>
							<TableHead className="font-semibold">Status</TableHead>
							<TableHead className="text-right font-semibold">Actions</TableHead>
						</TableRow>
					</TableHeader>
					<TableBody>
						{sortedRules.length === 0 ? (
							<TableRow>
								<TableCell colSpan={6} className="h-24 text-center">
									<span className="text-muted-foreground text-sm">No guardrail rules found.</span>
								</TableCell>
							</TableRow>
						) : (
							sortedRules.map((rule) => (
								<TableRow key={rule.id} className="hover:bg-muted/50 transition-colors">
									<TableCell className="font-medium">
										<div className="flex flex-col gap-1">
											<span className="max-w-xs">{rule.name}</span>
											{/* If we strictly follow the mockup, description is its own column */}
										</div>
									</TableCell>
									<TableCell>
										<span className="text-muted-foreground block max-w-sm truncate text-sm">
											{rule.description || "-"}
										</span>
									</TableCell>
									<TableCell>
										<span className="capitalize">{rule.apply_to}</span>
									</TableCell>
									<TableCell>
										<span>{rule.sampling_rate}%</span>
									</TableCell>
									<TableCell>
										<Switch checked={rule.enabled} onCheckedChange={() => {}} disabled />
									</TableCell>
									<TableCell className="text-right" onClick={(e) => e.stopPropagation()}>
										<div className="flex items-center justify-end gap-2">
											<Button
												variant="ghost"
												size="sm"
												onClick={() => onEdit(rule)}
												aria-label="Edit guardrail rule"
											>
												<Edit className="h-4 w-4" />
											</Button>
											{canDelete && (
												<Button
													variant="ghost"
													size="sm"
													onClick={() => setDeleteRuleId(rule.id)}
													aria-label="Delete guardrail rule"
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
