"use client";

import { useGetGuardrailProfilesQuery } from "@/lib/store/apis/guardrailsApi";
import { useState } from "react";
import { RbacOperation, RbacResource, useRbac } from "@/app/_fallbacks/enterprise/lib/contexts/rbacContext";
import { GuardrailProfile } from "@/lib/types/guardrails";
import { ProviderEditorSheet } from "./ProviderEditorSheet";
import { ProviderProfilesTable } from "./ProviderProfilesTable";

const PROVIDERS = [
	{ id: "bedrock", name: "AWS Bedrock", icon: "aws" as const },
	{ id: "azure", name: "Azure Content Moderation", icon: "azure" as const },
	{ id: "patronus_ai", name: "Patronus AI", icon: "patronus_ai" as const },
	{ id: "grayswan", name: "GraySwan", icon: "grayswan" as const },
];

export function ProvidersLayout() {
	const [activeProvider, setActiveProvider] = useState(PROVIDERS[0].id);
	const [dialogOpen, setDialogOpen] = useState(false);
	const [editingProfile, setEditingProfile] = useState<GuardrailProfile | null>(null);

	const canCreate = useRbac(RbacResource.Guardrails, RbacOperation.Create);
	const canDelete = useRbac(RbacResource.Guardrails, RbacOperation.Delete);

	const { data: profiles, isLoading } = useGetGuardrailProfilesQuery(undefined, {
		pollingInterval: 5000,
	});

	const activeProviderProfiles = profiles?.filter((p) => p.provider_name === activeProvider) || [];
	const activeProviderData = PROVIDERS.find((p) => p.id === activeProvider);

	const handleCreateNew = () => {
		setEditingProfile(null);
		setDialogOpen(true);
	};

	const handleEdit = (profile: GuardrailProfile) => {
		setEditingProfile(profile);
		setDialogOpen(true);
	};

	return (
		<div className="flex h-full w-full gap-6">
			{/* Left Sidebar */}
			<div className="w-64 border-r pr-6 shrink-0 h-full overflow-y-auto">
				<h3 className="font-semibold mb-4 text-sm text-muted-foreground px-2">Providers</h3>
				<div className="flex flex-col gap-1">
					{PROVIDERS.map((provider) => (
						<button
							key={provider.id}
							onClick={() => setActiveProvider(provider.id)}
							className={`text-left px-3 py-2 rounded-md text-sm transition-colors ${
								activeProvider === provider.id
									? "bg-accent text-accent-foreground font-medium"
									: "text-muted-foreground hover:bg-muted hover:text-foreground"
							}`}
						>
							{provider.name}
						</button>
					))}
				</div>
			</div>

			{/* Right Content Area */}
			<div className="flex-1 h-full overflow-y-auto flex flex-col gap-4">
				<ProviderProfilesTable 
					providerName={activeProviderData?.name || activeProvider}
					profiles={activeProviderProfiles}
					isLoading={isLoading}
					canCreate={canCreate}
					canDelete={canDelete}
					onCreateNew={handleCreateNew}
					onEdit={handleEdit}
				/>
			</div>

			<ProviderEditorSheet 
				open={dialogOpen}
				onOpenChange={(open) => {
					setDialogOpen(open);
					if (!open) setEditingProfile(null);
				}}
				editingProfile={editingProfile}
				selectedProviderId={activeProvider}
			/>
		</div>
	);
}
