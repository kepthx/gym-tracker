import { test } from '@playwright/test'

const PASSWORD = process.env.E2E_PASSWORD ?? 'очень-секретный-пароль'
const DIR = process.env.SHOTS_DIR ?? 'shots'

test('шапка с подтверждением выхода', async ({ page }) => {
  await page.setViewportSize({ width: 393, height: 852 })

  await page.goto('/')
  await page.getByPlaceholder('Пароль').fill(PASSWORD)
  await page.getByRole('button', { name: 'Войти' }).click()
  await page.getByRole('button', { name: /1\. Жим/ }).waitFor()

  await page.getByRole('button', { name: /1\. Жим/ }).click()
  await page.getByRole('button', { name: '← Выйти' }).click()
  await page.getByRole('button', { name: 'Отмена' }).waitFor()
  await page.screenshot({ path: `${DIR}/confirm-workout.png`, clip: { x: 0, y: 0, width: 393, height: 190 } })

  // The same component on the home screen: a card, not the header.
  await page.getByRole('button', { name: 'Отмена' }).click()
  await page.getByRole('button', { name: /^Подход 1$/ }).click()
  await page.keyboard.press('Enter')
  await page.getByRole('button', { name: '← Выйти' }).click()
  await page.getByRole('button', { name: 'Выйти', exact: true }).click()
  await page.getByRole('button', { name: 'Продолжить' }).waitFor()
  await page.getByRole('button', { name: 'Удалить' }).click()
  await page.screenshot({ path: `${DIR}/confirm-draft.png`, fullPage: false })
})
