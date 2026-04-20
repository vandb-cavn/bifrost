"use client"

import FullPageLoader from "@/components/fullPageLoader"
import { useDebouncedValue } from "@/hooks/useDebounce"
import {
	getErrorMessage,
	useGetBudgetsQuery,
	useGetRateLimitsQuery,
	useGetTeamsQuery,
	useGetUsersQuery,
} from "@/lib/store"
import { useEffect, useRef, useState } from "react"
import { toast } from "sonner"
import UsersTable from "@/app/workspace/governance/views/usersTable"

const POLLING_INTERVAL = 5000
const PAGE_SIZE = 25

export default function GovernanceUsersPage() {
	const shownErrorsRef = useRef(new Set<string>())
	const [search, setSearch] = useState("")
	const [offset, setOffset] = useState(0)
	const debouncedSearch = useDebouncedValue(search, 300)

	useEffect(() => {
		setOffset(0)
	}, [debouncedSearch])

	const {
		data: usersData,
		error: usersError,
		isLoading: usersLoading,
	} = useGetUsersQuery(
		{
			limit: PAGE_SIZE,
			offset,
			search: debouncedSearch || undefined,
		},
		{
			pollingInterval: POLLING_INTERVAL,
		},
	)

	const {
		data: teamsData,
		error: teamsError,
		isLoading: teamsLoading,
	} = useGetTeamsQuery(undefined, {
		pollingInterval: POLLING_INTERVAL,
	})

	const {
		data: budgetsData,
		error: budgetsError,
		isLoading: budgetsLoading,
	} = useGetBudgetsQuery(undefined, {
		pollingInterval: POLLING_INTERVAL,
	})

	const {
		data: rateLimitsData,
		error: rateLimitsError,
		isLoading: rateLimitsLoading,
	} = useGetRateLimitsQuery(undefined, {
		pollingInterval: POLLING_INTERVAL,
	})

	useEffect(() => {
		if (!usersError && !teamsError && !budgetsError && !rateLimitsError) {
			shownErrorsRef.current.clear()
			return
		}

		const errorKey = `${!!usersError}-${!!teamsError}-${!!budgetsError}-${!!rateLimitsError}`
		if (shownErrorsRef.current.has(errorKey)) return
		shownErrorsRef.current.add(errorKey)

		if (usersError) toast.error(`Failed to load users: ${getErrorMessage(usersError)}`)
		if (teamsError) toast.error(`Failed to load teams: ${getErrorMessage(teamsError)}`)
		if (budgetsError) toast.error(`Failed to load budgets: ${getErrorMessage(budgetsError)}`)
		if (rateLimitsError) toast.error(`Failed to load rate limits: ${getErrorMessage(rateLimitsError)}`)
	}, [usersError, teamsError, budgetsError, rateLimitsError])

	const isLoading = usersLoading || teamsLoading || budgetsLoading || rateLimitsLoading

	useEffect(() => {
		if (!usersData) return
		const totalCount = usersData.total_count ?? 0
		if (offset < totalCount) return
		setOffset(totalCount === 0 ? 0 : Math.floor((totalCount - 1) / PAGE_SIZE) * PAGE_SIZE)
	}, [usersData, offset])

	if (isLoading) {
		return <FullPageLoader />
	}

	return (
		<div className="mx-auto w-full max-w-7xl">
			<UsersTable
				users={usersData?.users || []}
				totalCount={usersData?.total_count || 0}
				teams={teamsData?.teams || []}
				budgets={budgetsData?.budgets || []}
				rateLimits={rateLimitsData?.rate_limits || []}
				search={search}
				debouncedSearch={debouncedSearch}
				onSearchChange={setSearch}
				offset={offset}
				limit={PAGE_SIZE}
				onOffsetChange={setOffset}
			/>
		</div>
	)
}
