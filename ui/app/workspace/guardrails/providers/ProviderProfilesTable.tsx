"use client";

import { GuardrailProfile } from "@/lib/types/guardrails";
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
import { useDeleteGuardrailProfileMutation } from "@/lib/store/apis/guardrailsApi";
import { toast } from "sonner";
import { getErrorMessage } from "@/lib/store";
import { formatDistanceToNow } from "date-fns";
import { guardrailProviderLabels } from "../shared/profileConfig";

interface ProviderProfilesTableProps {
	providerName: string;
	profiles: GuardrailProfile[];
	isLoading: boolean;
	canCreate: boolean;
	canDelete: boolean;
	onCreateNew: () => void;
	onEdit: (profile: GuardrailProfile) => void;
}

export function ProviderProfilesTable({
	providerName,
	profiles,
	isLoading,
	canCreate,
	canDelete,
	onCreateNew,
	onEdit,
}: ProviderProfilesTableProps) {
	const [deleteProfileId, setDeleteProfileId] = useState<string | null>(null);
	const [deleteGuardrailProfile, { isLoading: isDeleting }] = useDeleteGuardrailProfileMutation();

	const handleDelete = async () => {
		if (!canDelete || !deleteProfileId) return;

		try {
			await deleteGuardrailProfile(deleteProfileId).unwrap();
			toast.success("Profile deleted successfully");
			setDeleteProfileId(null);
		} catch (error: any) {
			toast.error(getErrorMessage(error));
		}
	};

	const profileToDelete = profiles.find((p) => p.id === deleteProfileId);

	return (
		<div className="space-y-4">
			<div className="flex items-center justify-between">
				<h2 className="text-lg font-semibold">{providerName} Configurations</h2>
				{canCreate && (
					<Button onClick={onCreateNew} data-testid="guardrails-profiles-create-button">
						Add configuration
					</Button>
				)}
			</div>

			<div className="overflow-hidden rounded-sm border bg-card">
				<Table>
					<TableHeader>
						<TableRow className="bg-muted/50">
							<TableHead className="font-semibold">Name</TableHead>
							<TableHead className="font-semibold">Provider</TableHead>
							<TableHead className="font-semibold">Status</TableHead>
							<TableHead className="font-semibold">Updated</TableHead>
							<TableHead className="text-right font-semibold">Actions</TableHead>
						</TableRow>
					</TableHeader>
					<TableBody>
						{isLoading ? (
							<TableRow>
								<TableCell colSpan={5} className="h-10">
									<div className="bg-muted h-2 w-32 animate-pulse rounded" />
								</TableCell>
							</TableRow>
						) : profiles.length === 0 ? (
							<TableRow>
								<TableCell colSpan={5} className="h-24 text-center">
									<span className="text-muted-foreground text-sm">No configurations found.</span>
								</TableCell>
							</TableRow>
						) : (
							profiles.map((profile) => (
								<TableRow key={profile.id} data-testid={`guardrails-profile-row-${profile.id}`} className="hover:bg-muted/50 transition-colors">
									<TableCell className="font-medium">{profile.name}</TableCell>
									<TableCell>
										{guardrailProviderLabels[profile.provider_name]}
									</TableCell>
									<TableCell>
										{profile.enabled ? <Badge>Enabled</Badge> : <Badge variant="secondary">Disabled</Badge>}
									</TableCell>
									<TableCell>
										{formatDistanceToNow(new Date(profile.updated_at), { addSuffix: true })}
									</TableCell>
									<TableCell className="text-right">
										<div className="flex items-center justify-end gap-2">
											<Button
												variant="ghost"
												size="sm"
												onClick={() => onEdit(profile)}
												data-testid={`guardrails-profile-edit-${profile.id}`}
												aria-label="Edit guardrail profile"
											>
												<Edit className="h-4 w-4" />
											</Button>
											{canDelete && (
												<Button
													variant="ghost"
													size="sm"
													onClick={() => setDeleteProfileId(profile.id)}
													data-testid={`guardrails-profile-delete-${profile.id}`}
													aria-label="Delete guardrail profile"
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

			<AlertDialog open={!!deleteProfileId} onOpenChange={(open) => !open && setDeleteProfileId(null)}>
				<AlertDialogContent>
					<AlertDialogHeader>
						<AlertDialogTitle>Delete Configuration</AlertDialogTitle>
						<AlertDialogDescription>
							Are you sure you want to delete &quot;{profileToDelete?.name}&quot;? 
							This action cannot be undone, and will affect any rules currently using this profile.
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
		</div>
	);
}
