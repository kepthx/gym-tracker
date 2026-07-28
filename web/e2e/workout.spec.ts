import { expect, test } from '@playwright/test'

const PASSWORD = process.env.E2E_PASSWORD ?? 'очень-секретный-пароль'

/**
 * The end-to-end scenario all of this was built for.
 *
 * It covers three mandatory properties at once: instant saving, resuming an interrupted
 * workout, and working with no connection. The page reload stands in for killing the app on
 * iOS — the effect is the same: in-memory state vanishes entirely, and whatever is left has
 * to be sitting in local storage.
 */
test('тренировка переживает офлайн и перезапуск, а потом доезжает до сервера', async ({
  page,
  context,
}) => {
  await page.goto('/')

  await test.step('вход', async () => {
    await page.getByPlaceholder('Пароль').fill(PASSWORD)
    await page.getByRole('button', { name: 'Войти' }).click()
    await expect(page.getByRole('button', { name: /1\. Жим/ })).toBeVisible()
  })

  await test.step('начать тренировку', async () => {
    await page.getByRole('button', { name: /1\. Жим/ }).click()
    await expect(page.getByText('Жим лёжа со штангой')).toBeVisible()
  })

  await test.step('уйти в офлайн', async () => {
    await context.setOffline(true)
  })

  const setButtons = page.getByRole('button', { name: /^Подход \d$/ })

  await test.step('отметить шесть подходов без связи', async () => {
    for (let i = 0; i < 6; i++) {
      await setButtons.nth(i).click()
      // The reps editor opens after the mark; dismiss it with a tap elsewhere.
      await page.keyboard.press('Enter')
    }
    // With no connection this is NOT an error: the data is on the device, and the indicator
    // has to read as success.
    await expect(page.getByText(/Сохранено на устройстве · \d+/)).toBeVisible()
  })

  await test.step('ввести вес', async () => {
    const weight = page.getByPlaceholder('кг').first()
    await weight.fill('82,5') // a comma, as on a Russian layout
    await weight.blur()
  })

  await test.step('перезапуск приложения посреди тренировки', async () => {
    await page.reload()
    // The screen restores from local storage, without a single request to the server.
    await expect(page.getByText('Жим лёжа со штангой')).toBeVisible()
  })

  await test.step('всё на месте', async () => {
    // What is checked is the saving itself, not the specific number of sets in the program:
    // the program changes every 6–8 weeks, while the requirement stays the same.
    await expect(page.locator('.workout-counter')).toHaveText(/^6\//)
    await expect(page.locator('.set-btn-done')).toHaveCount(6)
    await expect(page.getByPlaceholder('кг').first()).toHaveValue('82,5')
  })

  await test.step('связь вернулась — очередь уехала', async () => {
    await context.setOffline(false)
    await expect(page.getByText('Сохранено', { exact: true })).toBeVisible({ timeout: 30_000 })
  })

  await test.step('состояние сервера совпадает', async () => {
    const response = await page.request.get('/api/sync?since=0')
    expect(response.ok()).toBeTruthy()
    const body = await response.json()

    const open = body.changes.sessions.filter((s: { finished_at: number | null }) => s.finished_at === null)
    expect(open).toHaveLength(1)

    const done = body.changes.sets.filter((s: { done: boolean }) => s.done)
    expect(done.length).toBeGreaterThanOrEqual(6)

    const withWeight = body.changes.sets.find((s: { weight: number | null }) => s.weight === 82.5)
    expect(withWeight, 'вес, введённый через запятую, должен доехать как 82.5').toBeTruthy()
  })

  await test.step('завершить тренировку', async () => {
    await page.getByRole('button', { name: 'Завершить тренировку' }).click()
    await expect(page.getByRole('button', { name: /1\. Жим/ })).toBeVisible()
    await expect(page.getByText('Незавершённая тренировка')).toHaveCount(0)
  })
})

test('выход из тренировки требует подтверждения и не теряет данные', async ({ page }) => {
  await page.goto('/')
  await page.getByPlaceholder('Пароль').fill(PASSWORD)
  await page.getByRole('button', { name: 'Войти' }).click()

  await page.getByRole('button', { name: /2\. Тяга/ }).click()
  await page.getByRole('button', { name: /^Подход 1$/ }).first().click()
  await page.keyboard.press('Enter')

  // There are no system dialogs: they do not work in standalone mode on iOS.
  // There are no modals either — the confirmation expands in place.
  await page.getByRole('button', { name: '← Выйти' }).click()
  await expect(page.getByText('Выйти без завершения?')).toBeVisible()
  await page.getByRole('button', { name: 'Отмена' }).click()
  await expect(page.getByText('Тяга штанги в наклоне')).toBeVisible()

  await page.getByRole('button', { name: '← Выйти' }).click()
  await page.getByRole('button', { name: 'Выйти' }).click()

  // The workout is still unfinished and waiting to be resumed — leaving does not delete it.
  await expect(page.getByText('Незавершённая тренировка')).toBeVisible()
  await page.getByRole('button', { name: 'Продолжить' }).click()
  await expect(page.getByText('Тяга штанги в наклоне')).toBeVisible()
  await expect(page.locator('.workout-counter')).toHaveText(/^1\//)
  await expect(page.locator('.set-btn-done')).toHaveCount(1)
})
