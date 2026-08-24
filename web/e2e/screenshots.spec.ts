import { expect, test } from '@playwright/test'

const PASSWORD = process.env.E2E_PASSWORD ?? 'очень-секретный-пароль'
const DIR = process.env.SHOTS_DIR ?? 'shots'

/**
 * Not a check but a way to look at the app with your own eyes: tests catch behaviour, but
 * they do not catch a broken layout or unreadable contrast.
 * Run with: SHOTS=1 npx playwright test e2e/screenshots.spec.ts
 */
test.skip(!process.env.SHOTS, 'снимки экрана делаются только по требованию')

test('снимки экранов', async ({ page }) => {
  await page.setViewportSize({ width: 393, height: 852 })

  const setButtons = page.getByRole('button', { name: /^Подход \d$/ })
  const repsEditor = page.locator('.reps-field')

  // The day is addressed by its number, not by its name: the programme is rewritten every
  // 6–8 weeks, and a hardcoded day name silently rots this file — which test.skip hides from
  // CI until someone runs it by hand.

  /** Marks a set and closes the reps editor before the card below it stops moving. */
  const markSet = async (nth: number) => {
    await setButtons.nth(nth).click()
    await expect(repsEditor).toBeFocused()
    await page.keyboard.press('Enter')
    await expect(repsEditor).toHaveCount(0)
  }

  await page.goto('/')
  await page.screenshot({ path: `${DIR}/1-login.png` })

  await page.getByPlaceholder('Пароль').fill(PASSWORD)
  await page.getByRole('button', { name: 'Войти' }).click()
  await page.getByRole('button', { name: /^1\./ }).waitFor()
  await page.screenshot({ path: `${DIR}/2-home.png`, fullPage: true })

  // The first workout: filled in so the second one has a "last time" and a record.
  await page.getByRole('button', { name: /^1\./ }).click()
  const weights = ['80', '80', '82,5']
  for (let i = 0; i < 3; i++) {
    await markSet(i)
    await page.getByPlaceholder('кг').nth(i).fill(weights[i]!)
    await page.getByPlaceholder('кг').nth(i).blur()
  }
  // An unweighted exercise: there must be no weight field under its buttons. The index is
  // into every set button on the screen, so it moves whenever the day's exercises do.
  const unweighted = page
    .locator('section.card.exercise', { hasText: 'Прыжки на тумбу' })
    .getByRole('button', { name: /^Подход \d$/ })
    .first()
  await unweighted.click()
  await expect(repsEditor).toBeFocused()
  await page.keyboard.press('Enter')
  await expect(repsEditor).toHaveCount(0)
  await page.screenshot({ path: `${DIR}/3-workout.png`, fullPage: true })

  await page.getByRole('button', { name: 'Завершить тренировку' }).click()
  await page.getByRole('button', { name: /^1\./ }).waitFor()

  // A second workout on the same day: now the last result and the record are visible.
  await page.getByRole('button', { name: /^1\./ }).click()
  for (let i = 0; i < 3; i++) {
    await markSet(i)
    await page.getByPlaceholder('кг').nth(i).fill(i === 2 ? '85' : '82,5')
    await page.getByPlaceholder('кг').nth(i).blur()
  }
  await page.screenshot({ path: `${DIR}/4-workout-record.png`, fullPage: true })

  await page.getByRole('button', { name: 'Завершить тренировку' }).click()
  await page.getByRole('button', { name: /^1\./ }).waitFor()

  await page.getByRole('button', { name: 'Прогресс' }).click()
  await page.getByRole('heading', { name: 'Прогресс' }).waitFor()
  await page.screenshot({ path: `${DIR}/5-progress.png`, fullPage: true })
})
