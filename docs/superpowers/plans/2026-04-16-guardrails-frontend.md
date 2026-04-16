# Guardrails Frontend Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship a usable OSS Guardrails admin UI for rules and profiles that matches the implemented backend contract, including CEL validation and rule/profile association.

**Architecture:** Keep the existing workspace routing and RBAC gates, replace the current placeholder-level guardrails UI with two working admin surfaces (`Configuration` and `Providers`), and treat the Go handlers as the API source of truth. Rules save through direct CRUD plus profile link/unlink diffing; provider profiles use structured forms that serialize into `config` with an advanced JSON fallback.

**Tech Stack:** Next.js 15, React 19, RTK Query, React Hook Form, Zod, existing shadcn/ui components, Vitest, Playwright E2E.

---

## File Map

**Create:**
- `ui/lib/types/guardrails.ts` — finalized rule/profile request and response types aligned to the backend
- `ui/app/workspace/guardrails/shared/profileConfig.ts` — provider config types, defaults, serializers, and helpers
- `ui/app/workspace/guardrails/shared/profileConfig.test.ts` — unit tests for provider config serialization/parsing
- `tests/e2e/features/guardrails/guardrails.spec.ts` — end-to-end coverage for rules and profiles
- `tests/e2e/features/guardrails/guardrails.data.ts` — test data factories
- `tests/e2e/features/guardrails/pages/guardrails.page.ts` — page object for the guardrails workspace

**Modify:**
- `transports/bifrost-http/server/guardrails_handlers.go` — expose profile `config` in JSON responses so edit flows can hydrate correctly
- `ui/lib/store/apis/guardrailsApi.ts` — normalize rule/profile responses, add missing request fields, tighten invalidation behavior
- `ui/app/workspace/guardrails/configuration/GuardrailsConfigurationView.tsx` — page shell, create/edit orchestration, empty-state messaging
- `ui/app/workspace/guardrails/configuration/RulesTable.tsx` — operational rules table, status/action/scope/profile columns
- `ui/app/workspace/guardrails/configuration/RuleEditorSheet.tsx` — full rule form and link/unlink synchronization
- `ui/app/workspace/guardrails/configuration/RuleBuilder.tsx` — validation samples driven by `apply_to`
- `ui/app/workspace/guardrails/providers/ProvidersLayout.tsx` — provider navigation, provider list, edit orchestration
- `ui/app/workspace/guardrails/providers/ProviderProfilesTable.tsx` — profile table with better columns and test IDs
- `ui/app/workspace/guardrails/providers/ProviderEditorSheet.tsx` — structured provider forms + advanced JSON editor
- `tests/e2e/features/placeholders/placeholders.spec.ts` — remove or narrow guardrails placeholder assertions once real tests exist

---

### Task 1: Add the profile-config compatibility layer and align frontend types

**Files:**
- Modify: `transports/bifrost-http/server/guardrails_handlers.go`
- Modify: `ui/lib/types/guardrails.ts`
- Modify: `ui/lib/store/apis/guardrailsApi.ts`

- [ ] **Step 1: Add a UI-safe profile response shape in `guardrails_handlers.go`**

```go
type guardrailProfileResponse struct {
	ID           string                 `json:"id"`
	Name         string                 `json:"name"`
	ProviderName string                 `json:"provider_name"`
	Enabled      bool                   `json:"enabled"`
	Config       map[string]interface{} `json:"config,omitempty"`
	CreatedAt    time.Time              `json:"created_at"`
	UpdatedAt    time.Time              `json:"updated_at"`
}

func toGuardrailProfileResponse(profile *tables.TableGuardrailProfile) (guardrailProfileResponse, error) {
	resp := guardrailProfileResponse{
		ID:           profile.ID,
		Name:         profile.Name,
		ProviderName: profile.ProviderName,
		Enabled:      profile.Enabled,
		CreatedAt:    profile.CreatedAt,
		UpdatedAt:    profile.UpdatedAt,
	}
	if profile.ConfigJSON == "" {
		return resp, nil
	}
	var cfg map[string]interface{}
	if err := json.Unmarshal([]byte(profile.ConfigJSON), &cfg); err != nil {
		return guardrailProfileResponse{}, fmt.Errorf("parse guardrail profile config: %w", err)
	}
	resp.Config = cfg
	return resp, nil
}
```

- [ ] **Step 2: Use the response mapper in list/get/create/update profile handlers**

```go
func (s *BifrostHTTPServer) handleGetGuardrailProfile(ctx *fasthttp.RequestCtx) {
	id := ctx.UserValue("id").(string)
	profile, err := s.Config.ConfigStore.GetGuardrailProfileByID(context.Background(), id)
	if err != nil {
		handlers.SendError(ctx, http.StatusNotFound, err.Error())
		return
	}
	resp, err := toGuardrailProfileResponse(profile)
	if err != nil {
		handlers.SendError(ctx, http.StatusInternalServerError, err.Error())
		return
	}
	handlers.SendJSONWithStatus(ctx, resp, http.StatusOK)
}
```

- [ ] **Step 3: Update UI types so requests and responses match the real contract**

```ts
export interface GuardrailProfile {
	id: string;
	name: string;
	provider_name: "bedrock" | "azure" | "grayswan" | "patronus_ai" | "model_armor";
	enabled: boolean;
	config: Record<string, unknown>;
	created_at: string;
	updated_at: string;
}

export interface CreateGuardrailRuleRequest {
	name: string;
	description?: string;
	enabled: boolean;
	cel_expression: string;
	apply_to: "input" | "output" | "both";
	action: "block" | "warn";
	sampling_rate: number;
	timeout_ms: number;
	priority: number;
	scope: "global" | "virtual_key" | "team";
	scope_id: string | null;
	block_message: string;
	fail_open: boolean;
}
```

- [ ] **Step 4: Tighten RTK Query transforms and invalidation**

```ts
getGuardrailProfiles: builder.query<GuardrailProfile[], void>({
	query: () => ({ url: "/guardrails/profiles", method: "GET" }),
	transformResponse: (response: GuardrailProfile[]) => response ?? [],
	providesTags: (result) =>
		result
			? [
					...result.map((profile) => ({ type: "GuardrailProfiles" as const, id: profile.id })),
					"GuardrailProfiles",
				]
			: ["GuardrailProfiles"],
}),
```

- [ ] **Step 5: Run focused verification**

Run: `cd /Users/vanduong/Documents/vibecoding/bifost2/ui && npm run lint`

Expected: lint passes or only reports unrelated pre-existing issues.

- [ ] **Step 6: Commit**

```bash
git add transports/bifrost-http/server/guardrails_handlers.go \
        ui/lib/types/guardrails.ts \
        ui/lib/store/apis/guardrailsApi.ts
git commit -m "feat(guardrails): align profile API responses with frontend contract"
```

---

### Task 2: Finish the rules management surface

**Files:**
- Modify: `ui/app/workspace/guardrails/configuration/GuardrailsConfigurationView.tsx`
- Modify: `ui/app/workspace/guardrails/configuration/RulesTable.tsx`

- [ ] **Step 1: Expand the rules page shell to support a real management flow**

```tsx
<div className="flex items-center justify-between">
	<div>
		<h1 className="text-foreground text-lg font-semibold">Guardrail Rules</h1>
		<p className="text-muted-foreground text-sm">
			Define when requests and responses should be checked, and which guardrail profiles should run.
		</p>
	</div>
	{canCreate && (
		<Button data-testid="guardrails-rules-create-button" onClick={handleCreateNew} className="gap-2">
			<Plus className="h-4 w-4" />
			Add Rule
		</Button>
	)}
</div>
```

- [ ] **Step 2: Replace the minimal table with operational columns**

```tsx
<TableHeader>
	<TableRow className="bg-muted/50">
		<TableHead>Name</TableHead>
		<TableHead>Apply To</TableHead>
		<TableHead>Action</TableHead>
		<TableHead>Priority</TableHead>
		<TableHead>Scope</TableHead>
		<TableHead>Profiles</TableHead>
		<TableHead>Enabled</TableHead>
		<TableHead>Updated</TableHead>
		<TableHead className="text-right">Actions</TableHead>
	</TableRow>
</TableHeader>
```

- [ ] **Step 3: Add render helpers for status, scope, and profile count**

```tsx
function renderScope(rule: GuardrailRule) {
	if (rule.scope === "global") return "global";
	return `${rule.scope}:${rule.scope_id ?? "missing"}`;
}

function renderProfileSummary(rule: GuardrailRule) {
	const count = rule.profiles?.length ?? 0;
	if (count === 0) return "CEL only";
	if (count === 1) return rule.profiles?.[0]?.name ?? "1 profile";
	return `${count} profiles`;
}
```

- [ ] **Step 4: Add stable test IDs to list rows and actions**

```tsx
<TableRow key={rule.id} data-testid={`guardrails-rule-row-${rule.id}`}>
	<Button data-testid={`guardrails-rule-edit-${rule.id}`} ... />
	<Button data-testid={`guardrails-rule-delete-${rule.id}`} ... />
</TableRow>
```

- [ ] **Step 5: Verify the page compiles cleanly**

Run: `cd /Users/vanduong/Documents/vibecoding/bifost2/ui && npx tsc --noEmit`

Expected: no TypeScript errors from the configuration page changes.

- [ ] **Step 6: Commit**

```bash
git add ui/app/workspace/guardrails/configuration/GuardrailsConfigurationView.tsx \
        ui/app/workspace/guardrails/configuration/RulesTable.tsx
git commit -m "feat(guardrails): finish rules management table and page shell"
```

---

### Task 3: Complete the rule editor and profile link/unlink flow

**Files:**
- Modify: `ui/app/workspace/guardrails/configuration/RuleEditorSheet.tsx`
- Modify: `ui/app/workspace/guardrails/configuration/RuleBuilder.tsx`

- [ ] **Step 1: Introduce a real rule form schema with all supported fields**

```ts
const formSchema = z.object({
	name: z.string().min(1, "Name is required"),
	description: z.string().default(""),
	enabled: z.boolean(),
	apply_to: z.enum(["input", "output", "both"]),
	action: z.enum(["block", "warn"]),
	sampling_rate: z.coerce.number().min(0).max(100),
	timeout_ms: z.coerce.number().min(0),
	priority: z.coerce.number().int(),
	scope: z.enum(["global", "virtual_key", "team"]),
	scope_id: z.string().nullable(),
	block_message: z.string().default(""),
	fail_open: z.boolean(),
	cel_expression: z.string().min(1, "CEL expression is required"),
	profileIds: z.array(z.string()).default([]),
});
```

- [ ] **Step 2: Render conditional fields for `scope_id`, `block_message`, and `fail_open`**

```tsx
{scope !== "global" && (
	<FormField
		control={form.control}
		name="scope_id"
		render={({ field }) => (
			<FormItem>
				<FormLabel>Scope ID</FormLabel>
				<FormControl>
					<Input {...field} value={field.value ?? ""} data-testid="guardrails-rule-scope-id-input" />
				</FormControl>
			</FormItem>
		)}
	/>
)}
```

- [ ] **Step 3: Save rule metadata first, then synchronize profile associations**

```ts
async function syncRuleProfiles(ruleId: string, previousIds: string[], nextIds: string[]) {
	const prev = new Set(previousIds);
	const next = new Set(nextIds);

	const toLink = nextIds.filter((id) => !prev.has(id));
	const toUnlink = previousIds.filter((id) => !next.has(id));

	await Promise.all([
		...toLink.map((profileId) => linkGuardrailProfile({ ruleId, profileId }).unwrap()),
		...toUnlink.map((profileId) => unlinkGuardrailProfile({ ruleId, profileId }).unwrap()),
	]);
}
```

- [ ] **Step 4: Make CEL validation sample depend on `apply_to`**

```ts
function getValidationSample(applyTo: "input" | "output" | "both") {
	if (applyTo === "input") {
		return {
			messages: [{ role: "user", content: "Test message" }],
			model: "gpt-4o",
		};
	}
	return {
		messages: [{ role: "user", content: "Original request" }],
		model: "gpt-4o",
		output: {
			content: "Assistant response",
			finish_reason: "stop",
		},
	};
}
```

- [ ] **Step 5: Add test IDs to the editor controls**

```tsx
<Button data-testid="guardrails-rule-save-button" ... />
<Button data-testid="guardrails-rule-validate-button" ... />
<MultiSelect data-testid="guardrails-rule-profiles-select" ... />
```

- [ ] **Step 6: Run focused verification**

Run: `cd /Users/vanduong/Documents/vibecoding/bifost2/ui && npm run lint`

Expected: lint passes for the editor and validator changes.

- [ ] **Step 7: Commit**

```bash
git add ui/app/workspace/guardrails/configuration/RuleEditorSheet.tsx \
        ui/app/workspace/guardrails/configuration/RuleBuilder.tsx
git commit -m "feat(guardrails): implement full rule editor and profile linking flow"
```

---

### Task 4: Build the provider profiles management UI

**Files:**
- Create: `ui/app/workspace/guardrails/shared/profileConfig.ts`
- Create: `ui/app/workspace/guardrails/shared/profileConfig.test.ts`
- Modify: `ui/app/workspace/guardrails/providers/ProvidersLayout.tsx`
- Modify: `ui/app/workspace/guardrails/providers/ProviderProfilesTable.tsx`
- Modify: `ui/app/workspace/guardrails/providers/ProviderEditorSheet.tsx`

- [ ] **Step 1: Extract provider config defaults and serializers**

```ts
export const defaultProfileConfigByProvider = {
	bedrock: { endpoint: "", guardrail_id: "", version: "DRAFT" },
	azure: { endpoint: "", api_key: "", severity_threshold: 4 },
	grayswan: { endpoint: "", api_key: "", score_threshold: 0.5 },
	patronus_ai: { endpoint: "", api_key: "", evaluator: "lynx" },
	model_armor: { project_id: "", location: "", template_id: "", credentials_json: "" },
} satisfies Record<GuardrailProfile["provider_name"], Record<string, unknown>>;
```

- [ ] **Step 2: Expand the provider list and page metadata**

```tsx
const PROVIDERS = [
	{ id: "bedrock", name: "AWS Bedrock" },
	{ id: "azure", name: "Azure Content Safety" },
	{ id: "grayswan", name: "GraySwan" },
	{ id: "patronus_ai", name: "Patronus AI" },
	{ id: "model_armor", name: "Google Cloud Model Armor" },
];
```

- [ ] **Step 3: Upgrade the provider table columns and test IDs**

```tsx
<TableRow key={profile.id} data-testid={`guardrails-profile-row-${profile.id}`}>
	<TableCell className="font-medium">{profile.name}</TableCell>
	<TableCell className="capitalize">{profile.provider_name}</TableCell>
	<TableCell>{profile.enabled ? <Badge>Enabled</Badge> : <Badge variant="secondary">Disabled</Badge>}</TableCell>
	<TableCell>{formatDistanceToNow(new Date(profile.updated_at), { addSuffix: true })}</TableCell>
</TableRow>
```

- [ ] **Step 4: Replace the JSON-only editor with provider-aware structured fields plus advanced JSON**

```tsx
{selectedProviderId === "bedrock" && (
	<>
		<Input {...register("config.endpoint")} data-testid="guardrails-profile-bedrock-endpoint" />
		<Input {...register("config.guardrail_id")} data-testid="guardrails-profile-bedrock-guardrail-id" />
		<Input {...register("config.version")} data-testid="guardrails-profile-bedrock-version" />
	</>
)}
```

- [ ] **Step 5: Keep a bidirectional advanced JSON editor**

```tsx
<Tabs defaultValue="form">
	<TabsTrigger value="form">Structured Form</TabsTrigger>
	<TabsTrigger value="json">Advanced JSON</TabsTrigger>
	<TabsContent value="json">
		<CodeEditor value={configJson} onChange={setConfigJson} language="json" />
	</TabsContent>
</Tabs>
```

- [ ] **Step 6: Add unit tests for provider config parsing**

```ts
it("fills missing optional fields for bedrock configs", () => {
	expect(mergeProfileConfigDefaults("bedrock", { endpoint: "https://bedrock", guardrail_id: "gr-1" })).toEqual({
		endpoint: "https://bedrock",
		guardrail_id: "gr-1",
		version: "DRAFT",
	});
});
```

- [ ] **Step 7: Run focused verification**

Run: `cd /Users/vanduong/Documents/vibecoding/bifost2/ui && npx vitest run ui/app/workspace/guardrails/shared/profileConfig.test.ts`

Expected: all provider config helper tests pass.

- [ ] **Step 8: Commit**

```bash
git add ui/app/workspace/guardrails/shared/profileConfig.ts \
        ui/app/workspace/guardrails/shared/profileConfig.test.ts \
        ui/app/workspace/guardrails/providers/ProvidersLayout.tsx \
        ui/app/workspace/guardrails/providers/ProviderProfilesTable.tsx \
        ui/app/workspace/guardrails/providers/ProviderEditorSheet.tsx
git commit -m "feat(guardrails): implement provider profile management UI"
```

---

### Task 5: Replace placeholder coverage with real guardrails E2E tests

**Files:**
- Create: `tests/e2e/features/guardrails/guardrails.data.ts`
- Create: `tests/e2e/features/guardrails/pages/guardrails.page.ts`
- Create: `tests/e2e/features/guardrails/guardrails.spec.ts`
- Modify: `tests/e2e/features/placeholders/placeholders.spec.ts`

- [ ] **Step 1: Add guardrails page object locators for rules and providers**

```ts
export class GuardrailsPage extends BasePage {
	readonly createRuleButton = this.page.getByTestId("guardrails-rules-create-button");
	readonly saveRuleButton = this.page.getByTestId("guardrails-rule-save-button");
	readonly createProfileButton = this.page.getByTestId("guardrails-profiles-create-button");
	readonly saveProfileButton = this.page.getByTestId("guardrails-profile-save-button");

	async gotoConfiguration() {
		await this.page.goto("/workspace/guardrails/configuration");
		await waitForNetworkIdle(this.page);
	}

	async gotoProviders() {
		await this.page.goto("/workspace/guardrails/providers");
		await waitForNetworkIdle(this.page);
	}
}
```

- [ ] **Step 2: Add deterministic test data builders**

```ts
export function createGuardrailProfileData() {
	return {
		name: `e2e-profile-${Date.now()}`,
		provider_name: "azure" as const,
		enabled: true,
		config: {
			endpoint: "https://example.cognitiveservices.azure.com",
			api_key: "test-api-key",
			severity_threshold: 4,
		},
	};
}
```

- [ ] **Step 3: Cover the ship-ready CRUD flow in Playwright**

```ts
test("should create a profile, create a rule, validate CEL, and link the profile", async ({ guardrailsPage }) => {
	await guardrailsPage.gotoProviders();
	const profile = createGuardrailProfileData();
	await guardrailsPage.createProfile(profile);

	await guardrailsPage.gotoConfiguration();
	await guardrailsPage.openCreateRule();
	await guardrailsPage.fillRule({
		name: `e2e-rule-${Date.now()}`,
		apply_to: "input",
		action: "block",
		cel_expression: "request.messages.exists(m, m.content.contains(\"secret\"))",
		profileNames: [profile.name],
	});
	await guardrailsPage.validateRule();
	await guardrailsPage.saveRule();

	await expect(guardrailsPage.page.getByText(profile.name)).toBeVisible();
});
```

- [ ] **Step 4: Narrow the placeholder spec so it no longer owns guardrails route coverage**

```ts
// remove dedicated guardrails placeholder assertions once real feature tests exist
```

- [ ] **Step 5: Run the test suite for the new flow**

Run: `cd /Users/vanduong/Documents/vibecoding/bifost2 && make run-e2e FLOW=guardrails`

Expected: the new guardrails feature suite passes.

- [ ] **Step 6: Commit**

```bash
git add tests/e2e/features/guardrails/guardrails.data.ts \
        tests/e2e/features/guardrails/pages/guardrails.page.ts \
        tests/e2e/features/guardrails/guardrails.spec.ts \
        tests/e2e/features/placeholders/placeholders.spec.ts
git commit -m "test(guardrails): add end-to-end coverage for rules and profiles"
```

---

## Self-Review

- **Spec coverage:** This plan covers both frontend routes from the spec, the rule editor fields, link/unlink association flow, provider-specific config forms, and the profile-config dependency needed for edit hydration.
- **Placeholder scan:** No task relies on “implement later” steps; each task names exact files, expected code shapes, and verification commands.
- **Type consistency:** The plan standardizes on `timeout_ms`, `provider_name`, `config`, `scope_id`, and `profileIds`, and uses the same names across UI, RTK Query, and E2E tasks.

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-04-16-guardrails-frontend.md`. Two execution options:

**1. Subagent-Driven (recommended)** - I dispatch a fresh subagent per task, review between tasks, fast iteration

**2. Inline Execution** - Execute tasks in this session using executing-plans, batch execution with checkpoints

Which approach?
