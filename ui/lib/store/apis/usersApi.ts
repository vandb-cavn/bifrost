import { baseApi } from "./baseApi";

export interface UserMeResponse {
	id: string;
	email: string;
	name: string;
	role: string;
	is_active: boolean;
}

export const usersApi = baseApi.injectEndpoints({
	overrideExisting: false,
	endpoints: (builder) => ({
		getMe: builder.query<UserMeResponse, void>({
			query: () => ({
				url: "/users/me",
				method: "GET",
			}),
			providesTags: ["User"] as any,
		}),
	}),
});

export const { useGetMeQuery } = usersApi;
