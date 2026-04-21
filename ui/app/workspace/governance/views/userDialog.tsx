"use client";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
	Dialog,
	DialogContent,
	DialogDescription,
	DialogFooter,
	DialogHeader,
	DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import {
	getErrorMessage,
	useAssignUserRoleMutation,
	useCreateUserMutation,
	useGetUserRolesQuery,
	useListRolesQuery,
	useRemoveUserRoleMutation,
	useUpdateUserMutation,
} from "@/lib/store";
import { Budget, CreateUserRequest, GovernanceUser, RateLimit, Team, UpdateUserRequest } from "@/lib/types/governance";
import { formatCurrency, parseResetPeriod } from "@/lib/utils/governance";
import { Validator } from "@/lib/utils/validation";
import { RbacOperation, RbacResource, useRbac } from "@enterprise/lib";
import isEqual from "lodash.isequal";
import { X } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { toast } from "sonner";

interface UserDialogProps {
	user?: GovernanceUser | null;
	teams: Team[];
	budgets: Budget[];
	rateLimits: RateLimit[];
	onSave: () => void;
	onCancel: () => void;
}

interface UserFormData {
	email: string;
	name: string;
	teamId: string;
	budgetId: string;
	rateLimitId: string;
	isDirty: boolean;
}

const createInitialState = (user?: GovernanceUser | null): Omit<UserFormData, "isDirty"> => {
	return {
		email: user?.email || "",
		name: user?.name || "",
		teamId: user?.team_id || "none",
		budgetId: user?.budget_id || "none",
		rateLimitId: user?.rate_limit_id || "none",
	};
};

const getBudgetLabel = (budget: Budget) => `${formatCurrency(budget.max_limit)} / ${parseResetPeriod(budget.reset_duration)}`;

const getRateLimitLabel = (rateLimit: RateLimit) => {
	const parts: string[] = [];
	if (rateLimit.token_max_limit !== undefined && rateLimit.token_max_limit !== null) {
		parts.push(`${rateLimit.token_max_limit.toLocaleString()} tokens`);
	}
	if (rateLimit.request_max_limit !== undefined && rateLimit.request_max_limit !== null) {
		parts.push(`${rateLimit.request_max_limit.toLocaleString()} requests`);
	}
	return parts.length > 0 ? parts.join(" / ") : "No limits";
};

function RolesSection({ userId, hasPermission }: { userId: string; hasPermission: boolean }) {
	const { data: allRolesData } = useListRolesQuery();
	const { data: userRolesData, isLoading: rolesLoading, refetch } = useGetUserRolesQuery(userId);
	const [assignRole, { isLoading: isAssigning }] = useAssignUserRoleMutation();
	const [removeRole, { isLoading: isRemoving }] = useRemoveUserRoleMutation();

	const userRoleIds = useMemo(() => new Set((userRolesData?.roles ?? []).map((r) => r.id)), [userRolesData]);
	const assignableRoles = useMemo(
		() => (allRolesData?.roles ?? []).filter((r) => !userRoleIds.has(r.id)),
		[allRolesData, userRoleIds],
	);

	const handleAssign = async (roleId: string) => {
		try {
			await assignRole({ userId, roleId }).unwrap();
			refetch();
		} catch {
			toast.error("Failed to assign role");
		}
	};

	const handleRemove = async (roleId: string) => {
		try {
			await removeRole({ userId, roleId }).unwrap();
			refetch();
		} catch {
			toast.error("Failed to remove role");
		}
	};

	return (
		<div className="space-y-2">
			<div className="flex flex-wrap gap-2">
				{rolesLoading ? (
					<span className="text-muted-foreground text-sm">Loading roles...</span>
				) : (userRolesData?.roles ?? []).length === 0 ? (
					<span className="text-muted-foreground text-sm">No roles assigned</span>
				) : (
					(userRolesData?.roles ?? []).map((role) => (
						<Badge key={role.id} variant="secondary" className="flex items-center gap-1">
							{role.name}
							{hasPermission && (
								<button
									type="button"
									onClick={() => handleRemove(role.id)}
									disabled={isRemoving}
									className="hover:text-destructive ml-1"
									aria-label={`Remove role ${role.name}`}
								>
									<X className="h-3 w-3" />
								</button>
							)}
						</Badge>
					))
				)}
			</div>
			{hasPermission && assignableRoles.length > 0 && (
				<Select onValueChange={handleAssign} disabled={isAssigning} value="">
					<SelectTrigger className="w-48" data-testid="user-role-assign-select">
						<SelectValue placeholder="Add role..." />
					</SelectTrigger>
					<SelectContent>
						{assignableRoles.map((role) => (
							<SelectItem key={role.id} value={role.id}>
								{role.name}
							</SelectItem>
						))}
					</SelectContent>
				</Select>
			)}
		</div>
	);
}

export default function UserDialog({ user, teams, budgets, rateLimits, onSave, onCancel }: UserDialogProps) {
	const isEditing = !!user;
	const [initialState] = useState<Omit<UserFormData, "isDirty">>(createInitialState(user));
	const [formData, setFormData] = useState<UserFormData>({
		...initialState,
		isDirty: false,
	});

	const hasCreateAccess = useRbac(RbacResource.Users, RbacOperation.Create);
	const hasUpdateAccess = useRbac(RbacResource.Users, RbacOperation.Update);
	const hasPermission = isEditing ? hasUpdateAccess : hasCreateAccess;

	const [createUser, { isLoading: isCreating }] = useCreateUserMutation();
	const [updateUser, { isLoading: isUpdating }] = useUpdateUserMutation();
	const loading = isCreating || isUpdating;

	useEffect(() => {
		const currentData = {
			email: formData.email,
			name: formData.name,
			teamId: formData.teamId,
			budgetId: formData.budgetId,
			rateLimitId: formData.rateLimitId,
		};
		setFormData((prev) => ({
			...prev,
			isDirty: !isEqual(initialState, currentData),
		}));
	}, [formData.email, formData.name, formData.teamId, formData.budgetId, formData.rateLimitId, initialState]);

	const updateField = <K extends keyof UserFormData>(field: K, value: UserFormData[K]) => {
		setFormData((prev) => ({ ...prev, [field]: value }));
	};

	const validator = useMemo(
		() =>
			new Validator([
				...(isEditing ? [] : [Validator.email(formData.email.trim(), "A valid email is required")]),
				Validator.required(formData.name.trim(), "User name is required"),
				Validator.custom(formData.isDirty, "No changes to save"),
			]),
		[formData.email, formData.name, formData.isDirty, isEditing],
	);

	const handleSubmit = async (e: React.FormEvent) => {
		e.preventDefault();

		if (!validator.isValid()) {
			toast.error(validator.getFirstError());
			return;
		}

		try {
			if (isEditing && user) {
				const updateData: UpdateUserRequest = {
					name: formData.name.trim(),
					team_id: formData.teamId === "none" ? null : formData.teamId,
					budget_id: formData.budgetId === "none" ? null : formData.budgetId,
					rate_limit_id: formData.rateLimitId === "none" ? null : formData.rateLimitId,
				};
				await updateUser({ id: user.id, data: updateData }).unwrap();
				toast.success("User updated successfully");
			} else {
				const createData: CreateUserRequest = {
					email: formData.email.trim(),
					name: formData.name.trim(),
					...(formData.teamId === "none" ? {} : { team_id: formData.teamId }),
					...(formData.budgetId === "none" ? {} : { budget_id: formData.budgetId }),
					...(formData.rateLimitId === "none" ? {} : { rate_limit_id: formData.rateLimitId }),
				};
				await createUser(createData).unwrap();
				toast.success("User created successfully");
			}

			onSave();
		} catch (error) {
			toast.error(getErrorMessage(error));
		}
	};

	return (
		<Dialog open onOpenChange={onCancel}>
			<DialogContent className="max-w-2xl" data-testid="user-dialog-content">
				<DialogHeader>
					<DialogTitle className="flex items-center gap-2">{isEditing ? "Edit User" : "Create User"}</DialogTitle>
					<DialogDescription>
						Manage the user identity and optional governance assignments. Authentication method is tracked automatically and shown in the list.
					</DialogDescription>
				</DialogHeader>

				<form className="space-y-4" onSubmit={handleSubmit}>
					<div className="grid gap-4 sm:grid-cols-2">
						<div className="space-y-2">
							<Label htmlFor="user-email">Email</Label>
							<Input
								id="user-email"
								data-testid="user-email-input"
								type="email"
								value={formData.email}
								onChange={(e) => updateField("email", e.target.value)}
								placeholder="user@example.com"
								disabled={isEditing}
							/>
						</div>
						<div className="space-y-2">
							<Label htmlFor="user-name">Name</Label>
							<Input
								id="user-name"
								data-testid="user-name-input"
								value={formData.name}
								onChange={(e) => updateField("name", e.target.value)}
								placeholder="Display name"
								disabled={!hasPermission}
							/>
						</div>
					</div>

					<div className="grid gap-4 sm:grid-cols-3">
						<div className="space-y-2">
							<Label>Team</Label>
							<Select value={formData.teamId} onValueChange={(value) => updateField("teamId", value)} disabled={!hasPermission}>
								<SelectTrigger data-testid="user-team-select">
									<SelectValue placeholder="No team" />
								</SelectTrigger>
								<SelectContent>
									<SelectItem value="none">No team</SelectItem>
									{teams.map((team) => (
										<SelectItem key={team.id} value={team.id}>
											{team.name}
										</SelectItem>
									))}
								</SelectContent>
							</Select>
						</div>
						<div className="space-y-2">
							<Label>Budget</Label>
							<Select value={formData.budgetId} onValueChange={(value) => updateField("budgetId", value)} disabled={!hasPermission}>
								<SelectTrigger data-testid="user-budget-select">
									<SelectValue placeholder="No budget" />
								</SelectTrigger>
								<SelectContent>
									<SelectItem value="none">No budget</SelectItem>
									{budgets.map((budget) => (
										<SelectItem key={budget.id} value={budget.id}>
											{getBudgetLabel(budget)}
										</SelectItem>
									))}
								</SelectContent>
							</Select>
						</div>
						<div className="space-y-2">
							<Label>Rate Limit</Label>
							<Select value={formData.rateLimitId} onValueChange={(value) => updateField("rateLimitId", value)} disabled={!hasPermission}>
								<SelectTrigger data-testid="user-rate-limit-select">
									<SelectValue placeholder="No rate limit" />
								</SelectTrigger>
								<SelectContent>
									<SelectItem value="none">No rate limit</SelectItem>
									{rateLimits.map((rateLimit) => (
										<SelectItem key={rateLimit.id} value={rateLimit.id}>
											{getRateLimitLabel(rateLimit)}
										</SelectItem>
									))}
								</SelectContent>
							</Select>
						</div>
					</div>

					{isEditing && user && (
						<div className="space-y-2">
							<Label>Roles</Label>
							<RolesSection userId={user.id} hasPermission={hasPermission} />
						</div>
					)}

					<DialogFooter className="pt-2">
						<Button type="button" variant="outline" onClick={onCancel} data-testid="user-cancel-btn">
							Cancel
						</Button>
						<Button type="submit" disabled={loading || !hasPermission} data-testid="user-save-btn">
							{loading ? "Saving..." : isEditing ? "Save Changes" : "Create User"}
						</Button>
					</DialogFooter>
				</form>
			</DialogContent>
		</Dialog>
	);
}
