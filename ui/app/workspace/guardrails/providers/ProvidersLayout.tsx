"use client";

import { useGetGuardrailProfilesQuery } from "@/lib/store/apis/guardrailsApi";
import { useState } from "react";
import { RbacOperation, RbacResource, useRbac } from "@/app/_fallbacks/enterprise/lib/contexts/rbacContext";
import { GuardrailProfile, GuardrailProviderName } from "@/lib/types/guardrails";
import { ProviderEditorSheet } from "./ProviderEditorSheet";
import { ProviderProfilesTable } from "./ProviderProfilesTable";
import { guardrailProviderLabels } from "../shared/profileConfig";

const PROVIDERS = [
	"bedrock",
	"azure",
	"grayswan",
	"patronus_ai",
	"model_armor",
] as const satisfies GuardrailProviderName[];

export function ProvidersLayout() {
	const [activeProvider, setActiveProvider] = useState<GuardrailProviderName>(PROVIDERS[0]);
	const [dialogOpen, setDialogOpen] = useState(false);
	const [editingProfile, setEditingProfile] = useState<GuardrailProfile | null>(null);

	const canCreate = useRbac(RbacResource.GuardrailsProviders, RbacOperation.Create);
	const canDelete = useRbac(RbacResource.GuardrailsProviders, RbacOperation.Delete);

	const { data: profiles, isLoading } = useGetGuardrailProfilesQuery();

	const activeProviderProfiles = profiles?.filter((p) => p.provider_name === activeProvider) || [];

	const handleCreateNew = () => {
		setEditingProfile(null);
		setDialogOpen(true);
	};

	const handleEdit = (profile: GuardrailProfile) => {
		setEditingProfile(profile);
		setDialogOpen(true);
	};

	return (
		<>
		<div className="flex h-full w-full gap-6">
			<div className="w-64 shrink-0 h-full overflow-y-auto border-r pr-6">
				<h3 className="font-semibold mb-4 text-sm text-muted-foreground px-2">Providers</h3>
				<div className="flex flex-col gap-1">
					{PROVIDERS.map((provider) => (
						<button
							key={provider}
							type="button"
							data-testid={`guardrails-provider-tab-${provider}`}
							onClick={() => setActiveProvider(provider)}
							className={`text-left px-3 py-2 rounded-md text-sm transition-colors ${
								activeProvider === provider
									? "bg-accent text-accent-foreground font-medium"
									: "text-muted-foreground hover:bg-muted hover:text-foreground"
							}`}
						>
							{guardrailProviderLabels[provider]}
						</button>
					))}
				</div>
			</div>

			<div className="flex-1 h-full overflow-y-auto flex flex-col gap-4">
				<ProviderProfilesTable
					providerName={guardrailProviderLabels[activeProvider]}
					profiles={activeProviderProfiles}
					isLoading={isLoading}
					canCreate={canCreate}
					canDelete={canDelete}
					onCreateNew={handleCreateNew}
					onEdit={handleEdit}
				/>
			</div>
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
		</>
	);
}
