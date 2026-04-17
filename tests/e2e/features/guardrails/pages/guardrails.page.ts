import { Locator, Page, expect } from '@playwright/test'
import { BasePage } from '../../../core/pages/base.page'
import { fillSelect, waitForNetworkIdle } from '../../../core/utils/test-helpers'
import { GuardrailProfileData, GuardrailRuleData, GuardrailProviderName } from '../guardrails.data'

function escapeRegExp(value: string): string {
	return value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
}

export class GuardrailsPage extends BasePage {
	readonly createRuleButton: Locator
	readonly createProfileButton: Locator

	constructor(page: Page) {
		super(page)
		this.createRuleButton = page.getByTestId('guardrails-rules-create-button')
		this.createProfileButton = page.getByTestId('guardrails-profiles-create-button')
	}

	private async dismissDevProfilerIfPresent(): Promise<void> {
		const profiler = this.page.getByText('Dev Profiler', { exact: true })
		const dismissButton = this.page.locator('button[title="Dismiss"]').first()

		if (await profiler.count()) {
			for (let i = 0; i < 3; i += 1) {
				if (!(await profiler.isVisible().catch(() => false))) {
					break
				}

				await dismissButton.click({ force: true }).catch(() => {})
				await this.page.waitForTimeout(300)
			}

			await profiler.waitFor({ state: 'hidden', timeout: 5000 }).catch(() => {})
		}
	}

	private async selectRadixOption(option: string | RegExp): Promise<void> {
		await this.page.waitForSelector('[role="listbox"]', { timeout: 5000 })
		await this.page.getByRole('option', { name: option }).click()
	}

	async gotoConfiguration(): Promise<void> {
		await this.page.goto('/workspace/guardrails/configuration', { waitUntil: 'domcontentloaded', timeout: 60000 })
		await this.dismissDevProfilerIfPresent()
		await this.createRuleButton.waitFor({ state: 'visible', timeout: 30000 })
		await waitForNetworkIdle(this.page, 15000).catch(() => {})
	}

	async gotoProviders(): Promise<void> {
		await this.page.goto('/workspace/guardrails/providers', { waitUntil: 'domcontentloaded', timeout: 60000 })
		await this.dismissDevProfilerIfPresent()
		await this.createProfileButton.waitFor({ state: 'visible', timeout: 30000 })
		await waitForNetworkIdle(this.page, 15000).catch(() => {})
	}

	getRuleRow(name: string): Locator {
		return this.page.locator('[data-testid^="guardrails-rule-row-"]').filter({ hasText: name }).first()
	}

	getProfileRow(name: string): Locator {
		return this.page.locator('[data-testid^="guardrails-profile-row-"]').filter({ hasText: name }).first()
	}

	async ruleExists(name: string): Promise<boolean> {
		return (await this.getRuleRow(name).count()) > 0
	}

	async profileExists(name: string): Promise<boolean> {
		return (await this.getProfileRow(name).count()) > 0
	}

	async openProvider(provider: GuardrailProviderName): Promise<void> {
		await this.page.getByTestId(`guardrails-provider-tab-${provider}`).click()
		await waitForNetworkIdle(this.page)
	}

	async openCreateProfile(provider: GuardrailProviderName): Promise<void> {
		await this.openProvider(provider)
		await this.createProfileButton.click()
		await expect(this.page.getByRole('dialog')).toBeVisible({ timeout: 5000 })
	}

	async fillProfile(profile: GuardrailProfileData): Promise<void> {
		await this.page.getByTestId('guardrails-profile-name-input').fill(profile.name)

		if (!profile.enabled) {
			await this.page.getByTestId('guardrails-profile-enabled-switch').click()
		}

		switch (profile.provider_name) {
			case 'bedrock':
				await this.page.getByTestId(`guardrails-profile-${profile.provider_name}-endpoint`).fill(String(profile.config.endpoint ?? ''))
				await this.page.getByTestId(`guardrails-profile-${profile.provider_name}-guardrail_id`).fill(String(profile.config.guardrail_id ?? ''))
				await this.page.getByTestId(`guardrails-profile-${profile.provider_name}-version`).fill(String(profile.config.version ?? ''))
				break
			case 'azure':
				await this.page.getByTestId(`guardrails-profile-${profile.provider_name}-endpoint`).fill(String(profile.config.endpoint ?? ''))
				await this.page.getByTestId(`guardrails-profile-${profile.provider_name}-api_key`).fill(String(profile.config.api_key ?? ''))
				await this.page.getByTestId(`guardrails-profile-${profile.provider_name}-severity_threshold`).fill(String(profile.config.severity_threshold ?? 4))
				break
			case 'grayswan':
				await this.page.getByTestId(`guardrails-profile-${profile.provider_name}-endpoint`).fill(String(profile.config.endpoint ?? ''))
				await this.page.getByTestId(`guardrails-profile-${profile.provider_name}-api_key`).fill(String(profile.config.api_key ?? ''))
				await this.page.getByTestId(`guardrails-profile-${profile.provider_name}-score_threshold`).fill(String(profile.config.score_threshold ?? 0.5))
				break
			case 'patronus_ai':
				await this.page.getByTestId(`guardrails-profile-${profile.provider_name}-endpoint`).fill(String(profile.config.endpoint ?? ''))
				await this.page.getByTestId(`guardrails-profile-${profile.provider_name}-api_key`).fill(String(profile.config.api_key ?? ''))
				await this.page.getByTestId(`guardrails-profile-${profile.provider_name}-evaluator`).fill(String(profile.config.evaluator ?? 'lynx'))
				break
			case 'model_armor':
				await this.page.getByTestId(`guardrails-profile-${profile.provider_name}-project_id`).fill(String(profile.config.project_id ?? ''))
				await this.page.getByTestId(`guardrails-profile-${profile.provider_name}-location`).fill(String(profile.config.location ?? ''))
				await this.page.getByTestId(`guardrails-profile-${profile.provider_name}-template_id`).fill(String(profile.config.template_id ?? ''))
				await this.page.getByTestId(`guardrails-profile-${profile.provider_name}-credentials_json`).fill(String(profile.config.credentials_json ?? ''))
				break
		}
	}

	async saveProfile(): Promise<void> {
		await this.page.getByTestId('guardrails-profile-save-button').click()
		await this.waitForSuccessToast()
		await waitForNetworkIdle(this.page)
		await expect(this.page.getByRole('dialog')).not.toBeVisible({ timeout: 10000 })
	}

	async createProfile(profile: GuardrailProfileData): Promise<void> {
		await this.dismissToasts()
		await this.openCreateProfile(profile.provider_name)
		await this.fillProfile(profile)
		await this.saveProfile()
		await expect(this.getProfileRow(profile.name)).toBeVisible({ timeout: 10000 })
	}

	async deleteProfileByName(name: string): Promise<void> {
		const row = this.getProfileRow(name)
		await row.waitFor({ state: 'visible', timeout: 10000 })
		await row.getByRole('button', { name: 'Delete guardrail profile' }).click()
		await this.page.getByRole('button', { name: 'Delete' }).click()
		await this.waitForSuccessToast('deleted')
		await waitForNetworkIdle(this.page)
	}

	async openCreateRule(): Promise<void> {
		await this.createRuleButton.click()
		await expect(this.page.getByRole('dialog')).toBeVisible({ timeout: 5000 })
	}

	async fillRuleMetadata(rule: GuardrailRuleData): Promise<void> {
		await this.page.getByLabel(/Rule Name/i).fill(rule.name)

		if (rule.description !== undefined) {
			await this.page.getByLabel(/Description/i).fill(rule.description)
		}

		if (rule.apply_to) {
			const label =
				rule.apply_to === 'input' ? 'Input Only' : rule.apply_to === 'output' ? 'Output Only' : 'Both'
			await this.page.getByRole('button', { name: new RegExp(label, 'i') }).click()
		}

		if (rule.action) {
			await fillSelect(this.page, '[data-testid="guardrails-rule-action-select"]', rule.action === 'block' ? 'Block' : 'Warn')
		}

		if (rule.priority !== undefined) {
			await this.page.getByTestId('guardrails-rule-priority-input').fill(String(rule.priority))
		}

		if (rule.sampling_rate !== undefined) {
			await this.page.getByLabel(/Sampling Rate/i).fill(String(rule.sampling_rate))
		}

		if (rule.timeout_ms !== undefined) {
			await this.page.getByLabel(/Timeout/i).fill(String(rule.timeout_ms))
		}

		if (rule.scope) {
			await fillSelect(this.page, '[data-testid="guardrails-rule-scope-select"]', rule.scope === 'global' ? 'Global' : rule.scope === 'virtual_key' ? 'Virtual key' : 'Team')
		}

		if (rule.scope !== 'global' && rule.scope_id) {
			await this.page.getByTestId('guardrails-rule-scope-id-input').fill(rule.scope_id)
		}

		if (rule.action === 'block' && rule.block_message !== undefined) {
			await this.page.getByTestId('guardrails-rule-block-message-input').fill(rule.block_message)
		}

		if (rule.profileNames && rule.profileNames.length > 0) {
			await this.selectRuleProfiles(rule.profileNames)
		}
	}

	async fillRule(rule: GuardrailRuleData): Promise<void> {
		await this.fillRuleMetadata(rule)

		// New rules may default into the visual builder mode when the current CEL is importable.
		// Raw-CEL E2E flows should always force the editor tab before writing into Monaco.
		await this.openRuleEditorTab()

		const editor = this.page.locator('#guardrails-rule-expression-editor textarea').first()
		await editor.waitFor({ state: 'visible', timeout: 10000 })
		// Monaco sometimes intercepts pointer events with view layers; force interactions through the inputarea.
		await editor.click({ force: true })
		await editor.fill(rule.cel_expression ?? '', { force: true })
	}

	async selectRuleProfiles(profileNames: string[]): Promise<void> {
		const select = this.page.getByTestId('guardrails-rule-profiles-select')
		await select.click()
		const listbox = this.page.getByRole('listbox', { name: /Available options/i })
		await expect(listbox).toBeVisible({ timeout: 5000 })

		// Prefer placeholder targeting since cmdk inputs can be finicky with label queries during animations.
		const search = listbox.getByPlaceholder(/Search options/i)
		await search.waitFor({ state: 'visible', timeout: 5000 })
		for (const profileName of profileNames) {
			await search.fill(profileName)
			const option = listbox.getByRole('option', { name: new RegExp(escapeRegExp(profileName)) }).first()
			await option.waitFor({ state: 'visible', timeout: 5000 })
			await option.click()
		}
		await this.page.keyboard.press('Escape').catch(() => {})
	}

	async validateRule(): Promise<void> {
		await this.page.getByTestId('guardrails-rule-validate-button').click()
		await this.waitForSuccessToast('Expression is valid')
	}

	async openRuleBuilderTab(): Promise<void> {
		await this.page.getByTestId('guardrails-rule-builder-tab').click()
		await this.page.getByTestId('guardrails-rule-builder-root').waitFor({ state: 'visible', timeout: 10000 })
	}

	async openRuleEditorTab(): Promise<void> {
		await this.page.getByTestId('guardrails-rule-editor-tab').click()
		await this.page.locator('#guardrails-rule-expression-editor').waitFor({ state: 'visible', timeout: 10000 })
	}

	async addFirstBuilderRuleRow(): Promise<void> {
		const builderRoot = this.page.getByTestId('guardrails-rule-builder-root')
		await builderRoot.getByRole('button', { name: 'Add rule' }).click()
		await this.page.getByTestId('guardrails-builder-field-select').first().waitFor({ state: 'visible', timeout: 10000 })
	}

	async setFirstBuilderRule(fieldLabel: string, operatorLabel: string, value: string): Promise<void> {
		const fieldTrigger = this.page.getByTestId('guardrails-builder-field-select').first()
		await fieldTrigger.click()
		await this.selectRadixOption(fieldLabel)

		const operatorTrigger = this.page.getByTestId('guardrails-builder-operator-select').first()
		await operatorTrigger.click()
		await this.selectRadixOption(operatorLabel)

		await this.page.getByTestId('guardrails-builder-value-input').first().fill(value)
	}

	async expectFirstBuilderFieldOptions(optionLabels: string[]): Promise<void> {
		const fieldTrigger = this.page.getByTestId('guardrails-builder-field-select').first()
		await fieldTrigger.click()

		const listbox = this.page.getByRole('listbox')
		await expect(listbox).toBeVisible({ timeout: 5000 })
		const optionTexts = await listbox.getByRole('option').allInnerTexts()
		expect(optionTexts).toEqual(optionLabels)

		await this.page.keyboard.press('Escape').catch(() => {})
	}

	async expectRuleExpressionPreview(expected: string | RegExp): Promise<void> {
		await expect(this.page.locator('#guardrails-rule-expression-preview')).toContainText(expected, { timeout: 10000 })
	}

	async openEditRuleByName(name: string): Promise<void> {
		const row = this.getRuleRow(name)
		await row.waitFor({ state: 'visible', timeout: 10000 })
		await row.getByRole('button', { name: 'Edit guardrail rule' }).click()
		await expect(this.page.getByRole('dialog')).toBeVisible({ timeout: 5000 })
	}

	async saveRule(): Promise<void> {
		await this.dismissToasts()
		await this.page.getByTestId('guardrails-rule-save-button').click()
		await this.waitForSuccessToast()
		await waitForNetworkIdle(this.page)
		await expect(this.page.getByRole('dialog')).not.toBeVisible({ timeout: 10000 })
	}

	async createRule(rule: GuardrailRuleData): Promise<void> {
		await this.dismissToasts()
		await this.openCreateRule()
		await this.fillRule(rule)
		await this.validateRule()
		await this.saveRule()
		await expect(this.getRuleRow(rule.name)).toBeVisible({ timeout: 10000 })
	}

	async deleteRuleByName(name: string): Promise<void> {
		const row = this.getRuleRow(name)
		await row.waitFor({ state: 'visible', timeout: 10000 })
		await row.getByRole('button', { name: 'Delete guardrail rule' }).click()
		await this.page.getByRole('button', { name: 'Delete' }).click()
		await this.waitForSuccessToast('deleted')
		await waitForNetworkIdle(this.page)
	}
}
