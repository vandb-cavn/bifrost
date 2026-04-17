"use client";

import { NoPermissionView } from "@/components/noPermissionView";
import { RbacOperation, RbacResource, useRbac } from "@enterprise/lib";

export default function GuardrailsLayout({ children }: { children: React.ReactNode }) {
	const hasGuardrailsConfigAccess = useRbac(RbacResource.GuardrailsConfig, RbacOperation.View);
	const hasGuardrailsProvidersAccess = useRbac(RbacResource.GuardrailsProviders, RbacOperation.View);
	if (!hasGuardrailsConfigAccess && !hasGuardrailsProvidersAccess) {
		return <NoPermissionView entity="guardrails configuration" />;
	}
	return <div className="flex h-full flex-col">{children}</div>;
}
