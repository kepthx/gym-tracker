import { expect, test } from '@playwright/test'
import type { Locator, Page } from '@playwright/test'

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
    await expect(page.getByRole('button', { name: /1\. Ноги/ })).toBeVisible()
  })

  await test.step('начать тренировку', async () => {
    await page.getByRole('button', { name: /1\. Ноги/ }).click()
    await expect(page.getByText('Присед со штангой')).toBeVisible()
  })

  await test.step('уйти в офлайн', async () => {
    await context.setOffline(true)
  })

  const setButtons = page.getByRole('button', { name: /^Подход \d$/ })
  const repsEditor = page.locator('.reps-field')

  await test.step('отметить шесть подходов без связи', async () => {
    for (let i = 0; i < 6; i++) {
      await setButtons.nth(i).click()
      // The tap marks the set and hands the keyboard straight to the weight field of that
      // same column — the whole point of the layout. Nothing opens over the square, so
      // nothing below it moves either.
      await expect(page.locator('.weight-field').nth(i)).toBeFocused()
      await expect(repsEditor).toHaveCount(0)
    }
    await expect(page.locator('.set-btn-done')).toHaveCount(6)
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
    await expect(page.getByText('Присед со штангой')).toBeVisible()
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
    await expect(page.getByRole('button', { name: /1\. Ноги/ })).toBeVisible()
    await expect(page.getByText('Незавершённая тренировка')).toHaveCount(0)
  })

  // The number today's weight is decided from, in the place it is typed. A hint, never a
  // value: it must not be recorded by itself, or the log would fill with weights nobody
  // lifted.
  await test.step('в следующий раз прошлый вес стоит подсказкой в поле', async () => {
    await page.getByRole('button', { name: /1\. Ноги/ }).click()
    const weight = page.getByPlaceholder('82,5')
    await expect(weight).toBeVisible()
    await expect(weight).toHaveValue('')

    // Sets that had no weight last time keep the plain unit hint.
    await expect(page.getByPlaceholder('кг').first()).toBeVisible()
  })
})

test('выход из тренировки требует подтверждения и не теряет данные', async ({ page }) => {
  await page.goto('/')
  await page.getByPlaceholder('Пароль').fill(PASSWORD)
  await page.getByRole('button', { name: 'Войти' }).click()

  await page.getByRole('button', { name: /2\. Спина/ }).click()
  await page.getByRole('button', { name: /^Подход 1$/ }).first().click()
  await expect(page.locator('.weight-field').first()).toBeFocused()

  // There are no system dialogs: they do not work in standalone mode on iOS.
  // There are no modals either — the confirmation expands in place.
  await page.getByRole('button', { name: '← Выйти' }).click()
  await expect(page.getByText('Выйти без завершения?')).toBeVisible()
  await page.getByRole('button', { name: 'Отмена' }).click()
  await expect(page.getByText('Жим штанги стоя')).toBeVisible()

  await page.getByRole('button', { name: '← Выйти' }).click()
  await page.getByRole('button', { name: 'Выйти' }).click()

  // The workout is still unfinished and waiting to be resumed — leaving does not delete it.
  await expect(page.getByText('Незавершённая тренировка')).toBeVisible()
  await page.getByRole('button', { name: 'Продолжить' }).click()
  await expect(page.getByText('Жим штанги стоя')).toBeVisible()
  await expect(page.locator('.workout-counter')).toHaveText(/^1\//)
  await expect(page.locator('.set-btn-done')).toHaveCount(1)
})

/** A hold on a marked square: that is where the reps editor lives now. */
async function holdSet(page: Page, button: Locator): Promise<void> {
  const box = await button.boundingBox()
  if (!box) throw new Error('кнопка подхода не видна')
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2)
  await page.mouse.down()
  await page.waitForTimeout(600)
  await page.mouse.up()
}

/**
 * The whole set column in one exercise: the tap gives the keyboard to the weight, a hold
 * gives it to the reps, and the reps take digits with the exercise's unit printed beside them.
 *
 * The kilograms matter as much as the order here. A weight of 28,5 that cannot be typed is
 * what sent a week of them into the reps box instead.
 */
test('тап отдаёт клавиатуру весу, удержание — повторениям', async ({ page }) => {
  await page.goto('/')
  await page.getByPlaceholder('Пароль').fill(PASSWORD)
  await page.getByRole('button', { name: 'Войти' }).click()
  await page.getByRole('button', { name: /2\. Спина/ }).click()

  const carry = page.locator('section.exercise').filter({ hasText: 'Прогулка с гирей' })
  const button = carry.getByRole('button', { name: 'Подход 1' })
  const weight = carry.getByPlaceholder('кг').first()

  await button.click()
  // Marked, and the keyboard is on the weight — nothing opened over the square.
  await expect(carry.locator('.set-btn-done')).toHaveCount(1)
  await expect(weight).toBeFocused()
  await expect(carry.locator('.reps-field')).toHaveCount(0)

  await weight.fill('28,5')
  await weight.blur()
  await expect(weight).toHaveValue('28,5')

  await holdSet(page, button)

  const reps = carry.locator('.reps-field')
  await expect(reps).toBeFocused()
  await expect(carry.locator('.reps-unit')).toHaveText('м')
  await expect(reps).toHaveAttribute('placeholder', '30')
  await expect(reps).toHaveAttribute('inputmode', 'decimal')

  // The separator key is on this keypad too. It must not reach a count.
  await reps.pressSequentially('4,5')
  await expect(reps).toHaveValue('45')
  await page.keyboard.press('Enter')

  // Stored the way history has always held it — the unit went back on.
  await expect(button).toHaveText('45м')

  await page.reload()
  await expect(carry.getByRole('button', { name: 'Подход 1' })).toHaveText('45м')
  await expect(carry.getByPlaceholder('кг').first()).toHaveValue('28,5')
})

/**
 * The visible way into the reps, beside the hold that reaches one set.
 *
 * It exists because the hold cannot be seen and cannot be reached from a keyboard at all —
 * a correction nobody can find is a correction the app does not have.
 */
test('кнопка «Повторения» открывает отмеченные подходы разом', async ({ page }) => {
  await page.goto('/')
  await page.getByPlaceholder('Пароль').fill(PASSWORD)
  await page.getByRole('button', { name: 'Войти' }).click()
  await page.getByRole('button', { name: /1\. Ноги/ }).click()

  const squat = page.locator('section.exercise').filter({ hasText: 'Присед со штангой' })
  const reps = squat.getByRole('button', { name: 'Повторения' })

  // Nothing marked yet, so there is nothing to correct and no control offering to.
  await expect(reps).toHaveCount(0)

  await squat.getByRole('button', { name: 'Подход 1' }).click()
  await squat.getByRole('button', { name: 'Подход 2' }).click()
  await expect(squat.locator('.set-btn-done')).toHaveCount(2)

  await reps.click()

  // Both marked sets become fields at once; the first one holds the keyboard.
  const fields = squat.locator('.reps-field')
  await expect(fields).toHaveCount(2)
  await expect(fields.first()).toBeFocused()

  await fields.nth(1).fill('6')
  await squat.getByRole('button', { name: 'Готово' }).click()

  await expect(squat.locator('.reps-field')).toHaveCount(0)
  await expect(squat.getByRole('button', { name: 'Подход 1' })).toHaveText('8')
  await expect(squat.getByRole('button', { name: 'Подход 2' })).toHaveText('6')

  await page.reload()
  await expect(squat.getByRole('button', { name: 'Подход 2' })).toHaveText('6')
})
