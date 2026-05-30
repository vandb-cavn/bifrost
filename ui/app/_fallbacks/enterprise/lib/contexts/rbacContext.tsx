import { createContext, useContext, useMemo } from "react";
import { useIsAuthEnabledQuery, useGetMeQuery } from "@/lib/store/apis";

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

// RBAC Operation Names (must match backend definitions)
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

// Admin-restricted resources
const ADMIN_RESTRICTED_RESOURCES = new Set<RbacResource>([
	RbacResource.Users,
	RbacResource.UserProvisioning,
	RbacResource.RBAC,
	RbacResource.AccessProfiles,
	RbacResource.Settings,
]);

// Real RBAC Provider
export function RbacProvider({ children }: { children: React.ReactNode }) {
	const { data: authData, isLoading: isAuthLoading } = useIsAuthEnabledQuery();
	const { data: user, isLoading: isMeLoading, refetch } = useGetMeQuery(undefined, {
		skip: authData && !authData.is_auth_enabled,
	});

	const isAllowed = (resource: RbacResource, operation: RbacOperation): boolean => {
		// If auth is disabled, allow everything
		if (authData && !authData.is_auth_enabled) {
			return true;
		}

		// If loading auth or user, or no user in auth-enabled context, deny everything (fail-closed)
		if (isAuthLoading || isMeLoading || !user) {
			return false;
		}

		const role = user.role;

		// admin has full access
		if (role === "admin") {
			return true;
		}

		// operator has access to everything except admin-restricted resources
		if (role === "operator") {
			if (ADMIN_RESTRICTED_RESOURCES.has(resource)) {
				return false;
			}
			return true;
		}

		// viewer has view/read-only access on non-admin resources
		if (role === "viewer") {
			if (ADMIN_RESTRICTED_RESOURCES.has(resource)) {
				return false;
			}
			return operation === RbacOperation.Read || operation === RbacOperation.View;
		}

		return false;
	};

	const permissions = useMemo(() => {
		const map: Record<string, Record<string, boolean>> = {};
		if (!user) return map;
		return map;
	}, [user]);

	return (
		<RbacContext.Provider
			value={{
				isAllowed,
				permissions,
				isLoading: isAuthLoading || isMeLoading,
				refetch: () => {
					refetch();
				},
			}}
		>
			{children}
		</RbacContext.Provider>
	);
}

// Hook to check individual permission
export function useRbac(resource: RbacResource, operation: RbacOperation): boolean {
	const context = useContext(RbacContext);
	if (!context) {
		return true; // Default fallback for OSS context with no provider
	}
	return context.isAllowed(resource, operation);
}

// Hook to access full RBAC context
export function useRbacContext() {
	const context = useContext(RbacContext);
	if (!context) {
		// Return dummy values if used outside provider
		return {
			isAllowed: () => true,
			permissions: {},
			isLoading: false,
			refetch: () => {},
		};
	}
	return context;
}