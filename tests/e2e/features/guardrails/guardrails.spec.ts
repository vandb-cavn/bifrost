import { expect, test } from '../../core/fixtures/base.fixture'
import { createGuardrailProfileData, createGuardrailRuleData } from './guardrails.data'

const createdRules: string[] = []
const createdProfiles: string[] = []

test.describe('Guardrails', () => {
	test.describe.configure({ mode: 'serial' })

	test.afterEach(async ({ guardrailsPage }) => {
		await guardrailsPage.gotoConfiguration()
		for (const ruleName of [...createdRules]) {
			try {
				if (await guardrailsPage.ruleExists(ruleName)) {
					await guardrailsPage.deleteRuleByName(ruleName)
				}
			} catch (error) {
				console.error(`[CLEANUP] Failed to delete guardrail rule ${ruleName}:`, error)
			}
		}
		createdRules.length = 0

		await guardrailsPage.gotoProviders()
		for (const profileName of [...createdProfiles]) {
			try {
				if (await guardrailsPage.profileExists(profileName)) {
					await guardrailsPage.deleteProfileByName(profileName)
				}
			} catch (error) {
				console.error(`[CLEANUP] Failed to delete guardrail profile ${profileName}:`, error)
			}
		}
		createdProfiles.length = 0
	})

	test('should render the guardrails admin surfaces', async ({ guardrailsPage }) => {
		await guardrailsPage.gotoConfiguration()
		await expect(guardrailsPage.createRuleButton).toBeVisible()

		await guardrailsPage.gotoProviders()
		await expect(guardrailsPage.createProfileButton).toBeVisible()
	})

	test('should create and validate a rule using the visual builder', async ({ guardrailsPage }) => {
		await guardrailsPage.gotoConfiguration()
		await guardrailsPage.openCreateRule()

		const rule = createGuardrailRuleData({
			name: `e2e-builder-rule-${Date.now()}`,
			apply_to: 'input',
			cel_expression: '',
		})
		createdRules.push(rule.name)

		await guardrailsPage.fillRuleMetadata(rule)
		await guardrailsPage.openRuleBuilderTab()
		await guardrailsPage.addFirstBuilderRuleRow()
		await guardrailsPage.setFirstBuilderRule('Request message', 'contains', 'secret')

		const expectedCEL = 'request.messages.exists(m, m.content.contains("secret"))'
		await guardrailsPage.expectRuleExpressionPreview(expectedCEL)

		await guardrailsPage.validateRule()
		await guardrailsPage.saveRule()

		await expect(guardrailsPage.getRuleRow(rule.name)).toBeVisible({ timeout: 10000 })
	})

	test('should create a profile, validate a raw CEL rule, and link the profile', async ({ guardrailsPage }) => {
		const profile = createGuardrailProfileData({
			name: `e2e-profile-${Date.now()}`,
			provider_name: 'azure',
		})
		createdProfiles.push(profile.name)

		await guardrailsPage.gotoProviders()
		await guardrailsPage.createProfile(profile)
		await expect(guardrailsPage.getProfileRow(profile.name)).toContainText('Azure Content Safety')

		await guardrailsPage.gotoConfiguration()
		await guardrailsPage.openCreateRule()

		const rule = createGuardrailRuleData({
			name: `e2e-rule-${Date.now()}`,
			apply_to: 'input',
			action: 'block',
			profileNames: [profile.name],
			cel_expression: 'request.messages.exists(m, m.content.contains("secret"))',
		})
		createdRules.push(rule.name)

		await guardrailsPage.fillRule(rule)
		await guardrailsPage.validateRule()
		await guardrailsPage.saveRule()

		const ruleRow = guardrailsPage.getRuleRow(rule.name)
		await expect(ruleRow).toBeVisible({ timeout: 10000 })
		await expect(ruleRow).toContainText(profile.name)
	})
})
