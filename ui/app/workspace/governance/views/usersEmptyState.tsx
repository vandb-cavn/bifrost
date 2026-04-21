"use client";

import { Button } from "@/components/ui/button";
import { Users } from "lucide-react";

interface UsersEmptyStateProps {
	onAddClick: () => void;
	canCreate?: boolean;
}

export function UsersEmptyState({ onAddClick, canCreate = true }: UsersEmptyStateProps) {
	return (
		<div className="flex min-h-[80vh] w-full flex-col items-center justify-center gap-4 py-16 text-center">
			<div className="text-muted-foreground">
				<Users className="h-[5.5rem] w-[5.5rem]" strokeWidth={1} />
			</div>
			<div className="flex flex-col gap-1">
				<h1 className="text-muted-foreground text-xl font-medium">Users keep manual and SSO access in one place</h1>
				<div className="text-muted-foreground mx-auto mt-2 max-w-[620px] text-sm font-normal">
					Create users, assign them to teams, budgets, and rate limits, and track whether they were added manually or provisioned via SSO.
				</div>
				<div className="mx-auto mt-6 flex flex-row flex-wrap items-center justify-center gap-2">
					<Button aria-label="Add your first user" onClick={onAddClick} disabled={!canCreate} data-testid="user-button-add">
						Add User
					</Button>
				</div>
			</div>
		</div>
	);
}
