"use client";

import RolesView from "./views/rolesView";

export default function GovernanceRbacPage() {
	return (
		<div className="mx-auto w-full max-w-7xl space-y-6 p-6">
			<div>
				<h1 className="text-2xl font-semibold tracking-tight">Roles & Permissions</h1>
				<p className="text-muted-foreground text-sm">
					Manage system and custom roles. Assign permissions per role, then assign roles to users.
				</p>
			</div>
			<RolesView />
		</div>
	);
}
