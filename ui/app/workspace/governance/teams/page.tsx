"use client"

import TeamsTable from "@/app/workspace/governance/views/teamsTable"
import FullPageLoader from "@/components/fullPageLoader"
import { useDebouncedValue } from "@/hooks/useDebounce"
import { getErrorMessage, useGetCustomersQuery, useGetTeamsQuery, useGetVirtualKeysQuery } from "@/lib/store"
import { useEffect, useRef, useState } from "react"
import { toast } from "sonner"

const POLLING_INTERVAL = 5000
const PAGE_SIZE = 25

export default function GovernanceTeamsPage() {
	const shownErrorsRef = useRef(new Set<string>())
	const [search, setSearch] = useState("")
	const [offset, setOffset] = useState(0)
	const debouncedSearch = useDebouncedValue(search, 300)

	useEffect(() => {
		setOffset(0)
	}, [debouncedSearch])

	const {
		data: teamsData,
		error: teamsError,
		isLoading: teamsLoading,
	} = useGetTeamsQuery(
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
		data: customersData,
		error: customersError,
		isLoading: customersLoading,
	} = useGetCustomersQuery(undefined, {
		pollingInterval: POLLING_INTERVAL,
	})

	const {
		data: virtualKeysData,
		error: virtualKeysError,
		isLoading: virtualKeysLoading,
	} = useGetVirtualKeysQuery(undefined, {
		pollingInterval: POLLING_INTERVAL,
	})

	useEffect(() => {
		if (!teamsError && !customersError && !virtualKeysError) {
			shownErrorsRef.current.clear()
			return
		}

		const errorKey = `${!!teamsError}-${!!customersError}-${!!virtualKeysError}`
		if (shownErrorsRef.current.has(errorKey)) return
		shownErrorsRef.current.add(errorKey)

		if (teamsError && customersError && virtualKeysError) {
			toast.error("Failed to load governance data.")
			return
		}

		if (teamsError) toast.error(`Failed to load teams: ${getErrorMessage(teamsError)}`)
		if (customersError) toast.error(`Failed to load customers: ${getErrorMessage(customersError)}`)
		if (virtualKeysError) toast.error(`Failed to load virtual keys: ${getErrorMessage(virtualKeysError)}`)
	}, [teamsError, customersError, virtualKeysError])

	const isLoading = teamsLoading || customersLoading || virtualKeysLoading

	useEffect(() => {
		if (!teamsData) return
		const totalCount = teamsData.total_count ?? 0
		if (offset < totalCount) return
		setOffset(totalCount === 0 ? 0 : Math.floor((totalCount - 1) / PAGE_SIZE) * PAGE_SIZE)
	}, [teamsData, offset])

	if (isLoading) {
		return <FullPageLoader />
	}

	return (
		<div className="mx-auto w-full max-w-7xl">
			<TeamsTable
				teams={teamsData?.teams || []}
				totalCount={teamsData?.total_count || 0}
				customers={customersData?.customers || []}
				virtualKeys={virtualKeysData?.virtual_keys || []}
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
