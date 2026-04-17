import { expect, test } from '../../core/fixtures/base.fixture'
import { createGuardrailRuleData } from './guardrails.data'

const createdRules: string[] = []

test.describe('Guardrails Rule Builder', () => {
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
	})

	test('should build a simple rule visually and keep editor in sync', async ({ guardrailsPage }) => {
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
		await guardrailsPage.expectFirstBuilderFieldOptions(['Request message', 'Model'])
		await guardrailsPage.setFirstBuilderRule('Request message', 'contains', 'secret')

		const expectedCEL = 'request.messages.exists(m, m.content.contains("secret"))'
		await guardrailsPage.expectRuleExpressionPreview(expectedCEL)

		await guardrailsPage.openRuleEditorTab()
		await expect(guardrailsPage.page.locator('#guardrails-rule-expression-editor')).toContainText(expectedCEL)

		await guardrailsPage.validateRule()
		await guardrailsPage.saveRule()

		await expect(guardrailsPage.getRuleRow(rule.name)).toBeVisible({ timeout: 10000 })
	})
})
