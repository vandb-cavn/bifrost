import { baseApi } from "./baseApi";
import {
	GuardrailRule,
	GuardrailProfile,
	CreateGuardrailRuleRequest,
	UpdateGuardrailRuleRequest,
	CreateGuardrailProfileRequest,
	UpdateGuardrailProfileRequest,
	ValidateRuleRequest,
	ValidateRuleResponse,
} from "@/lib/types/guardrails";

function normalizeGuardrailRule(rule: GuardrailRule): GuardrailRule {
	return {
		...rule,
		description: rule.description ?? "",
		block_message: rule.block_message ?? "",
		scope_id: rule.scope_id ?? null,
		profiles: rule.profiles ?? [],
	};
}

function normalizeGuardrailProfile(profile: GuardrailProfile): GuardrailProfile {
	return {
		...profile,
		timeout_ms: profile.timeout_ms ?? 10000,
		config: profile.config ?? {},
	};
}

export const guardrailsApi = baseApi.injectEndpoints({
	endpoints: (builder) => ({
		// Rules
		getGuardrailRules: builder.query<GuardrailRule[], void>({
			query: () => ({
				url: "/guardrails/rules",
				method: "GET",
			}),
			transformResponse: (response: GuardrailRule[]) => (response ?? []).map(normalizeGuardrailRule),
			providesTags: (result) =>
				result
					? [...result.map((rule) => ({ type: "GuardrailRules" as const, id: rule.id })), "GuardrailRules"]
					: ["GuardrailRules"],
		}),

		getGuardrailRule: builder.query<GuardrailRule, string>({
			query: (id) => ({
				url: `/guardrails/rules/${id}`,
				method: "GET",
			}),
			transformResponse: (response: GuardrailRule) => normalizeGuardrailRule(response),
			providesTags: (result, error, arg) => [{ type: "GuardrailRules", id: arg }],
		}),

		createGuardrailRule: builder.mutation<GuardrailRule, CreateGuardrailRuleRequest>({
			query: (body) => ({
				url: "/guardrails/rules",
				method: "POST",
				body,
			}),
			invalidatesTags: (result) =>
				result
					? [{ type: "GuardrailRules", id: result.id }, "GuardrailRules"]
					: ["GuardrailRules"],
		}),

		updateGuardrailRule: builder.mutation<GuardrailRule, { id: string; data: UpdateGuardrailRuleRequest }>({
			query: ({ id, data }) => ({
				url: `/guardrails/rules/${id}`,
				method: "PUT",
				body: data,
			}),
			invalidatesTags: (result, error, arg) =>
				result
					? [{ type: "GuardrailRules", id: result.id }, "GuardrailRules"]
					: [{ type: "GuardrailRules", id: arg.id }, "GuardrailRules"],
		}),

		deleteGuardrailRule: builder.mutation<void, string>({
			query: (id) => ({
				url: `/guardrails/rules/${id}`,
				method: "DELETE",
			}),
			invalidatesTags: ["GuardrailRules"],
		}),

		validateGuardrailRule: builder.mutation<ValidateRuleResponse, ValidateRuleRequest>({
			query: (body) => ({
				url: "/guardrails/rules/validate",
				method: "POST",
				body,
			}),
		}),

		// Profiles
		getGuardrailProfiles: builder.query<GuardrailProfile[], void>({
			query: () => ({
				url: "/guardrails/profiles",
				method: "GET",
			}),
			transformResponse: (response: GuardrailProfile[]) => (response ?? []).map(normalizeGuardrailProfile),
			providesTags: (result) =>
				result
					? [
							...result.map((profile) => ({ type: "GuardrailProfiles" as const, id: profile.id })),
							"GuardrailProfiles",
						]
					: ["GuardrailProfiles"],
		}),

		getGuardrailProfile: builder.query<GuardrailProfile, string>({
			query: (id) => ({
				url: `/guardrails/profiles/${id}`,
				method: "GET",
			}),
			transformResponse: (response: GuardrailProfile) => normalizeGuardrailProfile(response),
			providesTags: (result, error, arg) => [{ type: "GuardrailProfiles", id: arg }],
		}),

		createGuardrailProfile: builder.mutation<GuardrailProfile, CreateGuardrailProfileRequest>({
			query: (body) => ({
				url: "/guardrails/profiles",
				method: "POST",
				body,
			}),
			invalidatesTags: (result) =>
				result
					? [{ type: "GuardrailProfiles", id: result.id }, "GuardrailProfiles", "GuardrailRules"]
					: ["GuardrailProfiles"],
		}),

		updateGuardrailProfile: builder.mutation<GuardrailProfile, { id: string; data: UpdateGuardrailProfileRequest }>({
			query: ({ id, data }) => ({
				url: `/guardrails/profiles/${id}`,
				method: "PUT",
				body: data,
			}),
			invalidatesTags: (result, error, arg) =>
				result
					? [{ type: "GuardrailProfiles", id: result.id }, "GuardrailProfiles", "GuardrailRules"]
					: [{ type: "GuardrailProfiles", id: arg.id }, "GuardrailProfiles", "GuardrailRules"],
		}),

		deleteGuardrailProfile: builder.mutation<void, string>({
			query: (id) => ({
				url: `/guardrails/profiles/${id}`,
				method: "DELETE",
			}),
			invalidatesTags: (result, error, id) => [
				{ type: "GuardrailProfiles", id },
				"GuardrailProfiles",
				"GuardrailRules",
			],
		}),

		linkGuardrailProfile: builder.mutation<void, { ruleId: string; profileId: string }>({
			query: ({ ruleId, profileId }) => ({
				url: `/guardrails/rules/${ruleId}/profiles/${profileId}`,
				method: "POST",
			}),
			invalidatesTags: (result, error, arg) => [{ type: "GuardrailRules", id: arg.ruleId }, "GuardrailRules"],
		}),

		unlinkGuardrailProfile: builder.mutation<void, { ruleId: string; profileId: string }>({
			query: ({ ruleId, profileId }) => ({
				url: `/guardrails/rules/${ruleId}/profiles/${profileId}`,
				method: "DELETE",
			}),
			invalidatesTags: (result, error, arg) => [{ type: "GuardrailRules", id: arg.ruleId }, "GuardrailRules"],
		}),
	}),
});

export const {
	useGetGuardrailRulesQuery,
	useGetGuardrailRuleQuery,
	useCreateGuardrailRuleMutation,
	useUpdateGuardrailRuleMutation,
	useDeleteGuardrailRuleMutation,
	useValidateGuardrailRuleMutation,
	useGetGuardrailProfilesQuery,
	useGetGuardrailProfileQuery,
	useCreateGuardrailProfileMutation,
	useUpdateGuardrailProfileMutation,
	useDeleteGuardrailProfileMutation,
	useLinkGuardrailProfileMutation,
	useUnlinkGuardrailProfileMutation,
} = guardrailsApi;
