"use client";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
	getErrorMessage,
	useCreateRoleMutation,
	useDeleteRoleMutation,
	useGetRoleQuery,
	useListPermissionsQuery,
	useListRolesQuery,
	useSetRolePermissionsMutation,
} from "@/lib/store";
import { Permission, Role } from "@/lib/types/governance";
import { RbacOperation, RbacResource, useRbac } from "@enterprise/lib";
import { AlertTriangle, ChevronDown, ChevronUp, Loader2, Shield, Trash2 } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
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

// Group permissions by resource for the permission picker UI.
function groupByResource(permissions: Permission[]): Record<string, Permission[]> {
	return permissions.reduce(
		(acc, p) => {
			if (!acc[p.resource]) acc[p.resource] = [];
			acc[p.resource].push(p);
			return acc;
		},
		{} as Record<string, Permission[]>,
	);
}

function RoleCard({
	role,
	canManage,
	onDelete,
	isDeleting,
}: {
	role: Role;
	canManage: boolean;
	onDelete: (role: Role) => void;
	isDeleting: boolean;
}) {
	const [open, setOpen] = useState(false);
	const { data: roleDetail } = useGetRoleQuery(role.id, { skip: !open });
	const { data: allPermsData } = useListPermissionsQuery(undefined, { skip: !open });
	const [setPerms, { isLoading: isSaving }] = useSetRolePermissionsMutation();

	const currentPermIDs = new Set((roleDetail?.permissions ?? []).map((p) => p.id));
	const grouped = groupByResource(allPermsData?.permissions ?? []);

	const togglePermission = async (permId: string) => {
		const next = new Set(currentPermIDs);
		if (next.has(permId)) {
			next.delete(permId);
		} else {
			next.add(permId);
		}
		try {
			await setPerms({ roleId: role.id, permissionIds: [...next] }).unwrap();
		} catch (error) {
			toast.error(getErrorMessage(error));
		}
	};

	return (
		<div className="border-border bg-card rounded-lg border p-4" data-testid={`role-card-${role.id}`}>
			<div className="flex items-center justify-between gap-3">
				<div className="flex items-center gap-2">
					<Shield className="text-muted-foreground size-4" />
					<span className="font-medium">{role.name}</span>
					{role.is_system && (
						<Badge variant="secondary" className="text-xs">
							System
						</Badge>
					)}
					{role.description && <span className="text-muted-foreground text-sm">{role.description}</span>}
				</div>
				<div className="flex items-center gap-2">
					<Button type="button" variant="ghost" size="sm" onClick={() => setOpen((v) => !v)} data-testid={`role-expand-${role.id}`}>
						{open ? <ChevronUp className="size-4" /> : <ChevronDown className="size-4" />}
						{open ? "Hide" : "Permissions"}
					</Button>
					{!role.is_system && canManage && (
						<Button
							type="button"
							variant="destructive"
							size="sm"
							disabled={isDeleting}
							onClick={() => onDelete(role)}
							data-testid={`role-delete-${role.id}`}
						>
							<Trash2 className="size-4" />
						</Button>
					)}
				</div>
			</div>

			{open && (
				<div className="mt-4 space-y-3">
					{Object.entries(grouped).map(([resource, perms]) => (
						<div key={resource}>
							<div className="text-muted-foreground mb-1 text-xs font-semibold uppercase tracking-wider">{resource}</div>
							<div className="flex flex-wrap gap-2">
								{perms.map((p) => {
									const checked = currentPermIDs.has(p.id);
									return (
										<button
											key={p.id}
											type="button"
											disabled={!canManage || isSaving}
											onClick={() => togglePermission(p.id)}
											className={`rounded-md border px-2 py-0.5 text-xs font-medium transition-colors ${
												checked
													? "border-primary bg-primary text-primary-foreground"
													: "border-border text-muted-foreground hover:border-primary/50"
											}`}
											data-testid={`perm-toggle-${p.id}`}
										>
											{p.operation}
										</button>
									);
								})}
							</div>
						</div>
					))}
					{isSaving && (
						<div className="text-muted-foreground flex items-center gap-1 text-xs">
							<Loader2 className="size-3 animate-spin" />
							Saving…
						</div>
					)}
				</div>
			)}
		</div>
	);
}

export default function RolesView() {
	const canManage = useRbac(RbacResource.Users, RbacOperation.Update);
	const { data, isLoading, error } = useListRolesQuery();
	const [createRole, { isLoading: isCreating }] = useCreateRoleMutation();
	const [deleteRole, { isLoading: isDeleting }] = useDeleteRoleMutation();

	const [newName, setNewName] = useState("");
	const [newDesc, setNewDesc] = useState("");
	const [deleteTarget, setDeleteTarget] = useState<Role | null>(null);

	const roles = data?.roles ?? [];

	const handleCreate = async () => {
		if (!newName.trim()) return;
		try {
			await createRole({ name: newName.trim(), description: newDesc.trim() }).unwrap();
			toast.success("Role created");
			setNewName("");
			setNewDesc("");
		} catch (error) {
			toast.error(getErrorMessage(error));
		}
	};

	const handleConfirmDelete = async () => {
		if (!deleteTarget) return;
		try {
			await deleteRole(deleteTarget.id).unwrap();
			toast.success("Role deleted");
		} catch (error) {
			toast.error(getErrorMessage(error));
		} finally {
			setDeleteTarget(null);
		}
	};

	if (error) {
		return (
			<div className="border-destructive/30 bg-destructive/5 rounded-lg border p-6">
				<div className="text-destructive flex items-center gap-2 text-sm font-medium">
					<AlertTriangle className="size-4" />
					Failed to load roles
				</div>
				<p className="text-muted-foreground mt-2 text-sm">{getErrorMessage(error)}</p>
			</div>
		);
	}

	return (
		<div className="space-y-6">
			<div>
				<h2 className="text-lg font-semibold tracking-tight">Roles & Permissions</h2>
				<p className="text-muted-foreground text-sm">
					Define roles and assign permissions. System roles cannot be deleted.
				</p>
			</div>

			{canManage && (
				<div className="border-border bg-card rounded-lg border p-4">
					<h3 className="mb-3 font-medium">Create Role</h3>
					<div className="grid gap-3 md:grid-cols-2">
						<div className="space-y-1">
							<Label htmlFor="rbac-new-name">Name</Label>
							<Input
								id="rbac-new-name"
								placeholder="e.g. ML Engineer"
								value={newName}
								onChange={(e) => setNewName(e.target.value)}
								data-testid="rbac-create-name-input"
							/>
						</div>
						<div className="space-y-1">
							<Label htmlFor="rbac-new-desc">Description</Label>
							<Input
								id="rbac-new-desc"
								placeholder="Optional description"
								value={newDesc}
								onChange={(e) => setNewDesc(e.target.value)}
								data-testid="rbac-create-desc-input"
							/>
						</div>
					</div>
					<div className="mt-3 flex justify-end">
						<Button
							type="button"
							onClick={handleCreate}
							disabled={!newName.trim() || isCreating}
							data-testid="rbac-create-submit-btn"
						>
							{isCreating && <Loader2 className="size-4 animate-spin" />}
							Create Role
						</Button>
					</div>
				</div>
			)}

			<div className="space-y-3">
				{isLoading ? (
					<div className="border-border bg-card rounded-lg border p-6">
						<div className="text-muted-foreground flex items-center gap-2 text-sm">
							<Loader2 className="size-4 animate-spin" />
							Loading roles…
						</div>
					</div>
				) : roles.length === 0 ? (
					<div className="border-border bg-card rounded-lg border p-6" data-testid="rbac-roles-empty">
						<p className="text-muted-foreground text-sm">No roles yet. Create one above.</p>
					</div>
				) : (
					roles.map((role) => (
						<RoleCard
							key={role.id}
							role={role}
							canManage={canManage}
							onDelete={setDeleteTarget}
							isDeleting={isDeleting && deleteTarget?.id === role.id}
						/>
					))
				)}
			</div>

			<AlertDialog open={!!deleteTarget} onOpenChange={(open) => !open && setDeleteTarget(null)}>
				<AlertDialogContent>
					<AlertDialogHeader>
						<AlertDialogTitle>Delete role?</AlertDialogTitle>
						<AlertDialogDescription>
							{deleteTarget && (
								<>
									This will remove <span className="font-medium">{deleteTarget.name}</span> and unassign it from all users.
								</>
							)}
						</AlertDialogDescription>
					</AlertDialogHeader>
					<AlertDialogFooter>
						<AlertDialogCancel>Cancel</AlertDialogCancel>
						<AlertDialogAction
							onClick={handleConfirmDelete}
							disabled={isDeleting}
							className="bg-red-600 hover:bg-red-700"
						>
							{isDeleting ? "Deleting…" : "Delete"}
						</AlertDialogAction>
					</AlertDialogFooter>
				</AlertDialogContent>
			</AlertDialog>
		</div>
	);
}
