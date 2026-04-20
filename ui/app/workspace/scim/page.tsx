"use client";

import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import SSOConfigTab from "./views/ssoConfigTab";

export default function SCIMPage() {
	return (
		<div className="mx-auto w-full max-w-7xl space-y-6 p-6">
			<div>
				<h1 className="text-2xl font-semibold tracking-tight">User Provisioning</h1>
				<p className="text-muted-foreground text-sm">
					Manage identity provider settings and reserve the SCIM surface for future directory sync.
				</p>
			</div>

			<Tabs defaultValue="sso" className="space-y-4">
				<TabsList data-testid="user-provisioning-tabs">
					<TabsTrigger value="sso" data-testid="user-provisioning-tab-sso">
						SSO / IdP Settings
					</TabsTrigger>
					<TabsTrigger value="scim" data-testid="user-provisioning-tab-scim">
						SCIM
					</TabsTrigger>
				</TabsList>

				<TabsContent value="sso">
					<SSOConfigTab />
				</TabsContent>

				<TabsContent value="scim">
					<div className="border-border bg-card rounded-lg border p-8">
						<div className="text-muted-foreground flex items-center gap-3 text-sm">
							<span>SCIM provisioning</span>
							<span className="inline-flex items-center rounded-full border px-2.5 py-0.5 text-xs font-semibold">Coming soon</span>
						</div>
					</div>
				</TabsContent>
			</Tabs>
		</div>
	);
}
