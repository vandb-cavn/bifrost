"use client";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { getErrorMessage, useCreateSSOConfigMutation, useDeleteSSOConfigMutation, useGetSSOConfigsQuery, useTestSSOConfigMutation, useUpdateSSOConfigMutation } from "@/lib/store";
import { SSOConfig } from "@/lib/types/governance";
import { RbacOperation, RbacResource, useRbac } from "@enterprise/lib";
import { AlertTriangle, CheckCircle2, Loader2, Plus, RefreshCw, Settings2, Trash2 } from "lucide-react";
import { useState } from "react";
import { toast } from "sonner";
import { AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle } from "@/components/ui/alertDialog";

type SSOProvider = "okta" | "entra";

type SSOFormState = {
	provider: SSOProvider;
	issuer_url: string;
	client_id: string;
	client_secret: string;
	role_claim_key: string;
	group_claim_key: string;
};

const emptyFormState = (): SSOFormState => ({
	provider: "okta",
	issuer_url: "",
	client_id: "",
	client_secret: "",
	role_claim_key: "",
	group_claim_key: "",
});

function formatProvider(provider: SSOProvider) {
	return provider === "okta" ? "Okta" : "Microsoft Entra";
}

function SSOConfigCard({
	cfg,
	canManage,
	onEdit,
	onTest,
	onToggleEnabled,
	onDelete,
	isTesting,
}: {
	cfg: SSOConfig;
	canManage: boolean;
	onEdit: (cfg: SSOConfig) => void;
	onTest: (cfg: SSOConfig) => void;
	onToggleEnabled: (cfg: SSOConfig, enabled: boolean) => Promise<void>;
	onDelete: (cfg: SSOConfig) => void;
	isTesting: boolean;
}) {
	return (
		<div className="border-border bg-card rounded-lg border p-4" data-testid={`sso-config-card-${cfg.id}`}>
			<div className="flex flex-col gap-4 lg:flex-row lg:items-start lg:justify-between">
				<div className="space-y-3">
					<div className="flex flex-wrap items-center gap-2">
						<h3 className="text-base font-semibold">{formatProvider(cfg.provider)}</h3>
						<Badge variant={cfg.enabled ? "default" : "outline"}>{cfg.enabled ? "Enabled" : "Disabled"}</Badge>
						<Badge variant="secondary">{cfg.issuer_url}</Badge>
					</div>
					<div className="grid gap-2 text-sm text-muted-foreground md:grid-cols-2">
						<div>
							<div className="text-foreground font-medium">Client ID</div>
							<div className="break-all">{cfg.client_id}</div>
						</div>
						<div>
							<div className="text-foreground font-medium">Claim keys</div>
							<div>
								Role: {cfg.role_claim_key || "-"} | Group: {cfg.group_claim_key || "-"}
							</div>
						</div>
					</div>
				</div>

				<div className="flex flex-wrap items-center gap-2">
					<Button
						type="button"
						variant="outline"
						size="sm"
						disabled={!canManage || isTesting}
						onClick={() => onTest(cfg)}
						dataTestId={`sso-config-test-${cfg.id}`}
					>
						{isTesting ? <Loader2 className="size-4 animate-spin" /> : <CheckCircle2 className="size-4" />}
						Test Connection
					</Button>
					<Switch
						checked={cfg.enabled}
						disabled={!canManage}
						onAsyncCheckedChange={(checked) => onToggleEnabled(cfg, checked)}
						data-testid={`sso-config-toggle-${cfg.id}`}
					/>
					<Button
						type="button"
						variant="ghost"
						size="sm"
						disabled={!canManage}
						onClick={() => onEdit(cfg)}
						dataTestId={`sso-config-edit-${cfg.id}`}
					>
						<Settings2 className="size-4" />
						Edit
					</Button>
					<Button
						type="button"
						variant="destructive"
						size="sm"
						disabled={!canManage}
						onClick={() => onDelete(cfg)}
						dataTestId={`sso-config-delete-${cfg.id}`}
					>
						<Trash2 className="size-4" />
						Delete
					</Button>
				</div>
			</div>
		</div>
	);
}

export default function SSOConfigTab() {
	const canManage = useRbac(RbacResource.Settings, RbacOperation.Update);
	const { data, isLoading, isFetching, error } = useGetSSOConfigsQuery();
	const [createConfig, { isLoading: isCreating }] = useCreateSSOConfigMutation();
	const [updateConfig, { isLoading: isUpdating }] = useUpdateSSOConfigMutation();
	const [deleteConfig, { isLoading: isDeleting }] = useDeleteSSOConfigMutation();
	const [testConfig, { isLoading: isTestingFromMutation }] = useTestSSOConfigMutation();
	const [createForm, setCreateForm] = useState<SSOFormState>(emptyFormState);
	const [createAdvancedOpen, setCreateAdvancedOpen] = useState(false);
	const [editingId, setEditingId] = useState<string | null>(null);
	const [editForm, setEditForm] = useState<SSOFormState>(emptyFormState);
	const [editAdvancedOpen, setEditAdvancedOpen] = useState(false);
	const [testingId, setTestingId] = useState<string | null>(null);
	const [deleteTarget, setDeleteTarget] = useState<SSOConfig | null>(null);

	const configs = data?.configs ?? [];
	const busy = isCreating || isUpdating || isDeleting || isTestingFromMutation;
	const isInitialLoading = isLoading && !data;
	const isRefreshing = isFetching && !isInitialLoading;
	const issuerPlaceholder = createForm.provider === "entra" ? "https://login.microsoftonline.com/{tenant-id}/v2.0" : "https://your-org.okta.com";
	const editIssuerPlaceholder = editForm.provider === "entra" ? "https://login.microsoftonline.com/{tenant-id}/v2.0" : "https://your-org.okta.com";

	const handleCreate = async () => {
		try {
			await createConfig(createForm).unwrap();
			toast.success("SSO config created");
			setCreateForm(emptyFormState());
			setCreateAdvancedOpen(false);
		} catch (error) {
			toast.error(getErrorMessage(error));
		}
	};

	const handleEditStart = (cfg: SSOConfig) => {
		setEditingId(cfg.id);
		setEditForm({
			provider: cfg.provider,
			issuer_url: cfg.issuer_url,
			client_id: cfg.client_id,
			client_secret: "",
			role_claim_key: cfg.role_claim_key || "",
			group_claim_key: cfg.group_claim_key || "",
		});
		setEditAdvancedOpen(false);
	};

	const handleEditCancel = () => {
		setEditingId(null);
		setEditForm(emptyFormState());
		setEditAdvancedOpen(false);
	};

	const handleEditSave = async () => {
		if (!editingId) return;
		try {
			const payload: Partial<SSOFormState> & { client_secret?: string } = { ...editForm };
			if (!payload.client_secret) {
				delete payload.client_secret;
			}
			await updateConfig({
				id: editingId,
				data: payload,
			}).unwrap();
			toast.success("SSO config updated");
			handleEditCancel();
		} catch (error) {
			toast.error(getErrorMessage(error));
		}
	};

	const handleToggleEnabled = async (cfg: SSOConfig, enabled: boolean) => {
		try {
			await updateConfig({
				id: cfg.id,
				data: { enabled },
			}).unwrap();
			toast.success(enabled ? "SSO enabled" : "SSO disabled");
		} catch (error) {
			toast.error(getErrorMessage(error));
		}
	};

	const handleTest = async (cfg: SSOConfig) => {
		setTestingId(cfg.id);
		try {
			await testConfig(cfg.id).unwrap();
			toast.success("Provider reachable");
		} catch (error) {
			toast.error(getErrorMessage(error));
		} finally {
			setTestingId(null);
		}
	};

	const handleDelete = async (cfg: SSOConfig) => {
		setDeleteTarget(cfg);
	};

	const handleConfirmDelete = async () => {
		if (!deleteTarget) return;
		try {
			await deleteConfig(deleteTarget.id).unwrap();
			toast.success("SSO config deleted");
			if (editingId === deleteTarget.id) {
				handleEditCancel();
			}
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
					Failed to load SSO configs
				</div>
				<p className="text-muted-foreground mt-2 text-sm">{getErrorMessage(error)}</p>
			</div>
		);
	}

	return (
		<div className="space-y-6">
			<div className="flex items-center justify-between gap-3">
				<div>
					<h2 className="text-lg font-semibold tracking-tight">SSO / IdP Settings</h2>
					<p className="text-muted-foreground text-sm">Configure one or more identity providers. Only one provider can be active at a time.</p>
				</div>
				{isRefreshing && (
					<div className="text-muted-foreground flex items-center gap-2 text-xs">
						<Loader2 className="size-3.5 animate-spin" />
						Refreshing
					</div>
				)}
				<Button
					type="button"
					variant="outline"
					disabled={!canManage}
					onClick={() => setCreateAdvancedOpen((v) => !v)}
					dataTestId="sso-create-advanced-toggle"
				>
					<RefreshCw className="size-4" />
					{createAdvancedOpen ? "Hide" : "Show"} advanced
				</Button>
			</div>

			<div className="space-y-3">
				<div className="border-border bg-card rounded-lg border p-4">
					<div className="mb-4 flex items-center justify-between gap-2">
						<div className="space-y-1">
							<h3 className="font-medium">Add Provider</h3>
							<p className="text-muted-foreground text-sm">Create a new Okta or Microsoft Entra configuration.</p>
						</div>
						<Badge variant="secondary">Login button follows active config</Badge>
					</div>

					<div className="grid gap-4 md:grid-cols-2">
						<div className="space-y-2">
							<Label htmlFor="sso-create-provider">Provider</Label>
							<Select
								value={createForm.provider}
								onValueChange={(value) => setCreateForm((prev) => ({ ...prev, provider: value as SSOProvider }))}
								disabled={!canManage}
							>
								<SelectTrigger id="sso-create-provider" data-testid="sso-create-provider-select">
									<SelectValue placeholder="Choose provider" />
								</SelectTrigger>
								<SelectContent>
									<SelectItem value="okta">Okta</SelectItem>
									<SelectItem value="entra">Microsoft Entra</SelectItem>
								</SelectContent>
							</Select>
						</div>
						<div className="space-y-2">
							<Label htmlFor="sso-create-issuer">Issuer URL</Label>
							<Input
								id="sso-create-issuer"
								placeholder={issuerPlaceholder}
								value={createForm.issuer_url}
								onChange={(e) => setCreateForm((prev) => ({ ...prev, issuer_url: e.target.value }))}
								disabled={!canManage}
								data-testid="sso-create-issuer-input"
							/>
						</div>
						<div className="space-y-2">
							<Label htmlFor="sso-create-client-id">Client ID</Label>
							<Input
								id="sso-create-client-id"
								value={createForm.client_id}
								onChange={(e) => setCreateForm((prev) => ({ ...prev, client_id: e.target.value }))}
								disabled={!canManage}
								data-testid="sso-create-client-id-input"
							/>
						</div>
						<div className="space-y-2">
							<Label htmlFor="sso-create-client-secret">Client Secret</Label>
							<Input
								id="sso-create-client-secret"
								type="password"
								value={createForm.client_secret}
								onChange={(e) => setCreateForm((prev) => ({ ...prev, client_secret: e.target.value }))}
								disabled={!canManage}
								data-testid="sso-create-client-secret-input"
							/>
						</div>
					</div>

					{createAdvancedOpen && (
						<div className="mt-4 grid gap-4 md:grid-cols-2">
							<div className="space-y-2">
								<Label htmlFor="sso-create-role-claim">Role Claim Key</Label>
								<Input
									id="sso-create-role-claim"
									placeholder="role"
									value={createForm.role_claim_key}
									onChange={(e) => setCreateForm((prev) => ({ ...prev, role_claim_key: e.target.value }))}
									disabled={!canManage}
									data-testid="sso-create-role-claim-input"
								/>
							</div>
							<div className="space-y-2">
								<Label htmlFor="sso-create-group-claim">Group Claim Key</Label>
								<Input
									id="sso-create-group-claim"
									placeholder="groups"
									value={createForm.group_claim_key}
									onChange={(e) => setCreateForm((prev) => ({ ...prev, group_claim_key: e.target.value }))}
									disabled={!canManage}
									data-testid="sso-create-group-claim-input"
								/>
							</div>
						</div>
					)}

					<div className="mt-4 flex items-center justify-end gap-2">
						<Button
							type="button"
							onClick={handleCreate}
							disabled={
								!canManage ||
								busy ||
								!createForm.issuer_url.trim() ||
								!createForm.client_id.trim() ||
								!createForm.client_secret.trim()
							}
							dataTestId="sso-create-submit-btn"
						>
							{isCreating && <Loader2 className="size-4 animate-spin" />}
							Create SSO Config
						</Button>
					</div>
				</div>

				<div className="space-y-3">
					{isInitialLoading ? (
						<div className="border-border bg-card rounded-lg border p-6">
							<div className="text-muted-foreground flex items-center gap-2 text-sm">
								<Loader2 className="size-4 animate-spin" />
								Loading SSO configs...
							</div>
						</div>
					) : configs.length === 0 ? (
						<div className="border-border bg-card rounded-lg border p-6" data-testid="sso-configs-empty-state">
							<div className="flex items-center gap-2 text-sm font-medium">
								<AlertTriangle className="text-muted-foreground size-4" />
								No SSO configs yet
							</div>
							<p className="text-muted-foreground mt-2 text-sm">Add an Okta or Microsoft Entra provider above to enable SSO login.</p>
						</div>
					) : (
						configs.map((cfg) => (
							<div key={cfg.id} className="space-y-3">
								<SSOConfigCard
									cfg={cfg}
									canManage={canManage}
									onEdit={handleEditStart}
									onTest={handleTest}
									onToggleEnabled={handleToggleEnabled}
									onDelete={handleDelete}
									isTesting={testingId === cfg.id}
								/>

								{editingId === cfg.id && (
									<div className="border-border bg-muted/30 rounded-lg border p-4">
										<div className="mb-4 flex items-center justify-between gap-2">
											<div>
												<h4 className="font-medium">Edit Provider</h4>
												<p className="text-muted-foreground text-sm">Leave client secret empty to keep the existing value.</p>
											</div>
											<Badge variant="outline">Editing</Badge>
										</div>

										<div className="grid gap-4 md:grid-cols-2">
											<div className="space-y-2">
												<Label htmlFor={`sso-edit-provider-${cfg.id}`}>Provider</Label>
												<Select
													value={editForm.provider}
													onValueChange={(value) => setEditForm((prev) => ({ ...prev, provider: value as SSOProvider }))}
													disabled={!canManage}
												>
													<SelectTrigger id={`sso-edit-provider-${cfg.id}`} data-testid={`sso-edit-provider-select-${cfg.id}`}>
														<SelectValue placeholder="Choose provider" />
													</SelectTrigger>
													<SelectContent>
														<SelectItem value="okta">Okta</SelectItem>
														<SelectItem value="entra">Microsoft Entra</SelectItem>
													</SelectContent>
												</Select>
											</div>
											<div className="space-y-2">
												<Label htmlFor={`sso-edit-issuer-${cfg.id}`}>Issuer URL</Label>
												<Input
													id={`sso-edit-issuer-${cfg.id}`}
													placeholder={editIssuerPlaceholder}
													value={editForm.issuer_url}
													onChange={(e) => setEditForm((prev) => ({ ...prev, issuer_url: e.target.value }))}
													disabled={!canManage}
													data-testid={`sso-edit-issuer-input-${cfg.id}`}
												/>
											</div>
											<div className="space-y-2">
												<Label htmlFor={`sso-edit-client-id-${cfg.id}`}>Client ID</Label>
												<Input
													id={`sso-edit-client-id-${cfg.id}`}
													value={editForm.client_id}
													onChange={(e) => setEditForm((prev) => ({ ...prev, client_id: e.target.value }))}
													disabled={!canManage}
													data-testid={`sso-edit-client-id-input-${cfg.id}`}
												/>
											</div>
											<div className="space-y-2">
												<Label htmlFor={`sso-edit-client-secret-${cfg.id}`}>Client Secret</Label>
												<Input
													id={`sso-edit-client-secret-${cfg.id}`}
													type="password"
													value={editForm.client_secret}
													onChange={(e) => setEditForm((prev) => ({ ...prev, client_secret: e.target.value }))}
													disabled={!canManage}
													data-testid={`sso-edit-client-secret-input-${cfg.id}`}
												/>
											</div>
										</div>

										<div className="mt-4 space-y-4">
											<Button
												type="button"
												variant="ghost"
												size="sm"
												onClick={() => setEditAdvancedOpen((value) => !value)}
												disabled={!canManage}
												dataTestId={`sso-edit-advanced-toggle-${cfg.id}`}
											>
												{editAdvancedOpen ? "Hide" : "Show"} advanced
											</Button>

											{editAdvancedOpen && (
												<div className="grid gap-4 md:grid-cols-2">
													<div className="space-y-2">
														<Label htmlFor={`sso-edit-role-claim-${cfg.id}`}>Role Claim Key</Label>
														<Input
															id={`sso-edit-role-claim-${cfg.id}`}
															value={editForm.role_claim_key}
															onChange={(e) => setEditForm((prev) => ({ ...prev, role_claim_key: e.target.value }))}
															disabled={!canManage}
															data-testid={`sso-edit-role-claim-input-${cfg.id}`}
														/>
													</div>
													<div className="space-y-2">
														<Label htmlFor={`sso-edit-group-claim-${cfg.id}`}>Group Claim Key</Label>
														<Input
															id={`sso-edit-group-claim-${cfg.id}`}
															value={editForm.group_claim_key}
															onChange={(e) => setEditForm((prev) => ({ ...prev, group_claim_key: e.target.value }))}
															disabled={!canManage}
															data-testid={`sso-edit-group-claim-input-${cfg.id}`}
														/>
													</div>
												</div>
											)}

											<div className="flex items-center justify-end gap-2">
												<Button type="button" variant="outline" onClick={handleEditCancel} disabled={!canManage} dataTestId={`sso-edit-cancel-btn-${cfg.id}`}>
													Cancel
												</Button>
												<Button
													type="button"
													onClick={handleEditSave}
													disabled={!canManage || !editForm.issuer_url.trim() || !editForm.client_id.trim()}
													dataTestId={`sso-edit-submit-btn-${cfg.id}`}
												>
													{isUpdating && <Loader2 className="size-4 animate-spin" />}
													Save Changes
												</Button>
											</div>
										</div>
									</div>
								)}
							</div>
						))
					)}
				</div>
			</div>

			<AlertDialog open={!!deleteTarget} onOpenChange={(open) => !open && setDeleteTarget(null)}>
				<AlertDialogContent>
					<AlertDialogHeader>
						<AlertDialogTitle>Delete SSO config?</AlertDialogTitle>
						<AlertDialogDescription>
							{deleteTarget ? (
								<>
									This will remove <span className="font-medium">{formatProvider(deleteTarget.provider)}</span> for{" "}
									<span className="font-medium">{deleteTarget.issuer_url}</span>. Users will no longer be able to sign in through this provider.
								</>
							) : (
								"This action cannot be undone."
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
							{isDeleting ? "Deleting..." : "Delete"}
						</AlertDialogAction>
					</AlertDialogFooter>
				</AlertDialogContent>
			</AlertDialog>
		</div>
	);
}
