"use client";

import {
	AlertDialog,
	AlertDialogAction,
	AlertDialogCancel,
	AlertDialogContent,
	AlertDialogDescription,
	AlertDialogFooter,
	AlertDialogHeader,
	AlertDialogTitle,
	AlertDialogTrigger,
} from "@/components/ui/alertDialog";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { getErrorMessage, useDeleteUserMutation } from "@/lib/store";
import { Budget, GovernanceUser, RateLimit, Team } from "@/lib/types/governance";
import { cn } from "@/lib/utils";
import { formatCurrency, parseResetPeriod } from "@/lib/utils/governance";
import { RbacOperation, RbacResource, useRbac } from "@enterprise/lib";
import { ChevronLeft, ChevronRight, Edit, Plus, Search, Trash2 } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import UserDialog from "./userDialog";
import { UsersEmptyState } from "./usersEmptyState";

interface UsersTableProps {
	users: GovernanceUser[];
	totalCount: number;
	teams: Team[];
	budgets: Budget[];
	rateLimits: RateLimit[];
	search: string;
	debouncedSearch: string;
	onSearchChange: (value: string) => void;
	offset: number;
	limit: number;
	onOffsetChange: (offset: number) => void;
}

const getTeamName = (teamId: string | undefined, teams: Team[]) => {
	if (!teamId) return "-";
	const team = teams.find((item) => item.id === teamId);
	return team?.name || "Unknown team";
};

const getBudgetSummary = (budget: Budget | undefined) => {
	if (!budget) return "-";
	return `${formatCurrency(budget.max_limit)} / ${parseResetPeriod(budget.reset_duration)}`;
};

const getRateLimitSummary = (rateLimit: RateLimit | undefined) => {
	if (!rateLimit) return "-";
	const parts: string[] = [];
	if (rateLimit.token_max_limit !== undefined && rateLimit.token_max_limit !== null) {
		parts.push(`${rateLimit.token_max_limit.toLocaleString()} tokens`);
	}
	if (rateLimit.request_max_limit !== undefined && rateLimit.request_max_limit !== null) {
		parts.push(`${rateLimit.request_max_limit.toLocaleString()} requests`);
	}
	return parts.length > 0 ? parts.join(" / ") : "No limits";
};

const getAuthMethodLabel = (method: GovernanceUser["auth_method"]) => {
	if (method === "oidc") return "SSO";
	if (method === "password") return "Manual";
	return method;
};

export default function UsersTable({
	users,
	totalCount,
	teams,
	budgets,
	rateLimits,
	search,
	debouncedSearch,
	onSearchChange,
	offset,
	limit,
	onOffsetChange,
}: UsersTableProps) {
	const [showUserDialog, setShowUserDialog] = useState(false);
	const [editingUser, setEditingUser] = useState<GovernanceUser | null>(null);

	const hasCreateAccess = useRbac(RbacResource.Users, RbacOperation.Create);
	const hasUpdateAccess = useRbac(RbacResource.Users, RbacOperation.Update);
	const hasDeleteAccess = useRbac(RbacResource.Users, RbacOperation.Delete);

	const [deleteUser, { isLoading: isDeleting }] = useDeleteUserMutation();

	const handleDelete = async (userId: string) => {
		try {
			await deleteUser(userId).unwrap();
			toast.success("User deleted successfully");
		} catch (error) {
			toast.error(getErrorMessage(error));
		}
	};

	const handleAddUser = () => {
		setEditingUser(null);
		setShowUserDialog(true);
	};

	const handleEditUser = (user: GovernanceUser) => {
		setEditingUser(user);
		setShowUserDialog(true);
	};

	const handleUserSaved = () => {
		setShowUserDialog(false);
		setEditingUser(null);
	};

	const hasActiveFilters = debouncedSearch.trim().length > 0;

	if (totalCount === 0 && !hasActiveFilters) {
		return (
			<>
				{showUserDialog && (
					<UserDialog
						user={editingUser}
						teams={teams}
						budgets={budgets}
						rateLimits={rateLimits}
						onSave={handleUserSaved}
						onCancel={() => setShowUserDialog(false)}
					/>
				)}
				<UsersEmptyState onAddClick={handleAddUser} canCreate={hasCreateAccess} />
			</>
		);
	}

	return (
		<>
			{showUserDialog && (
				<UserDialog
					user={editingUser}
					teams={teams}
					budgets={budgets}
					rateLimits={rateLimits}
					onSave={handleUserSaved}
					onCancel={() => setShowUserDialog(false)}
				/>
			)}

			<div className="space-y-4">
				<div className="flex items-center justify-between">
					<div>
						<h2 className="text-lg font-semibold">Users</h2>
						<p className="text-muted-foreground text-sm">Manage users, their assignments, and whether they came from SSO or manual setup.</p>
					</div>
					<Button data-testid="create-user-btn" onClick={handleAddUser} disabled={!hasCreateAccess}>
						<Plus className="h-4 w-4" />
						Add User
					</Button>
				</div>

				<div className="flex items-center gap-3">
					<div className="relative max-w-sm flex-1">
						<Search className="text-muted-foreground absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2" />
						<Input
							aria-label="Search users by name or email"
							placeholder="Search by name or email..."
							value={search}
							onChange={(e) => onSearchChange(e.target.value)}
							className="pl-9"
							data-testid="users-search-input"
						/>
					</div>
				</div>

				<div className="rounded-sm border overflow-hidden" data-testid="users-table">
					<Table>
						<TableHeader>
							<TableRow>
								<TableHead>Email</TableHead>
								<TableHead>Name</TableHead>
								<TableHead>Team</TableHead>
								<TableHead>Budget</TableHead>
								<TableHead>Rate Limit</TableHead>
								<TableHead>Auth Method</TableHead>
								<TableHead className="text-right"></TableHead>
							</TableRow>
						</TableHeader>
						<TableBody>
							{users.length === 0 ? (
								<TableRow>
									<TableCell colSpan={7} className="h-24 text-center">
										<span className="text-muted-foreground text-sm">No matching users found.</span>
									</TableCell>
								</TableRow>
							) : (
								users.map((user) => (
									<TableRow key={user.id} data-testid={`user-row-${user.id}`} className={cn("group transition-colors")}>
										<TableCell className="max-w-[240px] py-4">
											<div className="flex flex-col gap-1">
												<span className="truncate font-medium">{user.email}</span>
											</div>
										</TableCell>
										<TableCell className="max-w-[220px]">
											<span className="truncate">{user.name || "-"}</span>
										</TableCell>
										<TableCell>
											{user.team_id ? (
												<Badge variant="secondary" className="max-w-[180px] truncate whitespace-nowrap">
													{getTeamName(user.team_id, teams)}
												</Badge>
											) : (
												<span className="text-muted-foreground text-sm">-</span>
											)}
										</TableCell>
										<TableCell className="max-w-[220px]">
											{user.budget ? (
												<Badge variant="outline" className="max-w-[220px] truncate whitespace-nowrap">
													{getBudgetSummary(user.budget)}
												</Badge>
											) : (
												<span className="text-muted-foreground text-sm">-</span>
											)}
										</TableCell>
										<TableCell className="max-w-[240px]">
											{user.rate_limit ? (
												<Badge variant="outline" className="max-w-[240px] truncate whitespace-nowrap">
													{getRateLimitSummary(user.rate_limit)}
												</Badge>
											) : (
												<span className="text-muted-foreground text-sm">-</span>
											)}
										</TableCell>
										<TableCell>
											<Badge variant={user.auth_method === "oidc" ? "secondary" : "outline"}>{getAuthMethodLabel(user.auth_method)}</Badge>
										</TableCell>
										<TableCell className="text-right">
											<div className="flex items-center justify-end gap-1 opacity-0 transition-opacity group-focus-within:opacity-100 group-hover:opacity-100">
												<Button
													variant="ghost"
													size="icon"
													className="h-8 w-8"
													onClick={() => handleEditUser(user)}
													disabled={!hasUpdateAccess}
													aria-label={`Edit user ${user.email}`}
													data-testid={`user-edit-btn-${user.id}`}
												>
													<Edit className="h-4 w-4" />
												</Button>
												<AlertDialog>
													<AlertDialogTrigger asChild>
														<Button
															variant="ghost"
															size="icon"
															className="h-8 w-8 text-red-500 hover:bg-red-500/10 hover:text-red-500"
															disabled={!hasDeleteAccess}
															aria-label={`Delete user ${user.email}`}
															data-testid={`user-delete-btn-${user.id}`}
														>
															<Trash2 className="h-4 w-4" />
														</Button>
													</AlertDialogTrigger>
													<AlertDialogContent>
														<AlertDialogHeader>
															<AlertDialogTitle>Delete User</AlertDialogTitle>
															<AlertDialogDescription>
																Are you sure you want to delete &quot;{user.email}&quot;? This will remove the user and any owned budget or rate limit rows.
																This action cannot be undone.
															</AlertDialogDescription>
														</AlertDialogHeader>
														<AlertDialogFooter>
															<AlertDialogCancel>Cancel</AlertDialogCancel>
															<AlertDialogAction onClick={() => handleDelete(user.id)} disabled={isDeleting} className="bg-red-600 hover:bg-red-700">
																{isDeleting ? "Deleting..." : "Delete"}
															</AlertDialogAction>
														</AlertDialogFooter>
													</AlertDialogContent>
												</AlertDialog>
											</div>
										</TableCell>
									</TableRow>
								))
							)}
						</TableBody>
					</Table>
				</div>

				{totalCount > 0 && (
					<div className="flex items-center justify-between px-2">
						<p className="text-muted-foreground text-sm">
							Showing {offset + 1}-{Math.min(offset + limit, totalCount)} of {totalCount}
						</p>
						<div className="flex gap-2">
							<Button variant="outline" size="sm" disabled={offset === 0} onClick={() => onOffsetChange(Math.max(0, offset - limit))} data-testid="users-pagination-prev-btn">
								<ChevronLeft className="mr-1 h-4 w-4" /> Previous
							</Button>
							<Button
								variant="outline"
								size="sm"
								disabled={offset + limit >= totalCount}
								onClick={() => onOffsetChange(offset + limit)}
								data-testid="users-pagination-next-btn"
							>
								Next <ChevronRight className="ml-1 h-4 w-4" />
							</Button>
						</div>
					</div>
				)}
			</div>
		</>
	);
}
