"use client";

import { createContext, useCallback, useContext, useMemo } from "react";
import { useGetMyPermissionsQuery } from "@/lib/store";

// RBAC Resource Names (must match backend definitions)
export enum RbacResource {
	GuardrailsConfig = "GuardrailsConfig",
	GuardrailsProviders = "GuardrailsProviders",
	GuardrailRules = "GuardrailRules",
	UserProvisioning = "UserProvisioning",
	Cluster = "Cluster",
	Settings = "Settings",
	Users = "Users",
	Logs = "Logs",
	Observability = "Observability",
	VirtualKeys = "VirtualKeys",
	ModelProvider = "ModelProvider",
	Plugins = "Plugins",
	MCPGateway = "MCPGateway",
	AdaptiveRouter = "AdaptiveRouter",
	AuditLogs = "AuditLogs",
	Customers = "Customers",
	Teams = "Teams",
	RBAC = "RBAC",
	Governance = "Governance",
	RoutingRules = "RoutingRules",
	PIIRedactor = "PIIRedactor",
	PromptRepository = "PromptRepository",
	PromptDeploymentStrategy = "PromptDeploymentStrategy",
	AccessProfiles = "AccessProfiles",
}

export enum RbacOperation {
	Read = "Read",
	View = "View",
	Create = "Create",
	Update = "Update",
	Delete = "Delete",
	Download = "Download",
}

interface RbacContextType {
	isAllowed: (resource: RbacResource, operation: RbacOperation) => boolean;
	permissions: Record<string, Record<string, boolean>>;
	isLoading: boolean;
	refetch: () => void;
}

const RbacContext = createContext<RbacContextType | null>(null);

export function RbacProvider({ children }: { children: React.ReactNode }) {
	const { data, isLoading, refetch } = useGetMyPermissionsQuery();

	const permissions = useMemo<Record<string, Record<string, boolean>>>(() => {
		if (!data) return {};
		if (data.is_admin) {
			// Legacy admin session: synthesize allow-all map so existing useRbac() calls work.
			return {};
		}
		const map: Record<string, Record<string, boolean>> = {};
		for (const p of data.permissions) {
			if (!map[p.resource]) map[p.resource] = {};
			map[p.resource][p.operation] = true;
		}
		return map;
	}, [data]);

	const isAllowed = useCallback(
		(resource: RbacResource, operation: RbacOperation): boolean => {
			if (!data) return false;
			if (data.is_admin) return true;
			return permissions[resource]?.[operation] ?? false;
		},
		[data, permissions],
	);

	return (
		<RbacContext.Provider value={{ isAllowed, permissions, isLoading, refetch }}>
			{children}
		</RbacContext.Provider>
	);
}

export function useRbac(resource: RbacResource, operation: RbacOperation): boolean {
	const context = useContext(RbacContext);
	if (!context) return true; // Outside provider: fail open (same as before)
	return context.isAllowed(resource, operation);
}

export function useRbacContext() {
	const context = useContext(RbacContext);
	if (!context) {
		return {
			isAllowed: () => true,
			permissions: {},
			isLoading: false,
			refetch: () => {},
		};
	}
	return context;
}
