import { expect, Page, test } from '@playwright/test'

const PASSWORD = process.env.E2E_PASSWORD ?? 'очень-секретный-пароль'

/** Any host that is not this application. There should no longer be a single request to one. */
const THIRD_PARTY = /^https?:\/\/(?!127\.0\.0\.1|localhost)/

/**
 * The guide toggle of one exercise card.
 *
 * Scoped to the card rather than picked by index: an opened guide renames its own button, so
 * an index into "Как выполнять" quietly points at a different exercise afterwards.
 */
function guideToggle(page: Page, exercise: string) {
  return page
    .locator('section.exercise')
    .filter({ hasText: exercise })
    .getByRole('button', { name: /Как выполнять|Скрыть технику/ })
}

async function openWorkout(page: Page): Promise<void> {
  await page.goto('/')
  await page.getByPlaceholder('Пароль').fill(PASSWORD)
  await page.getByRole('button', { name: 'Войти' }).click()
  await page.getByRole('button', { name: /1\. Ноги/ }).click()
  await expect(page.getByText('Присед со штангой')).toBeVisible()
}

/**
 * The guide expands inside the card and shows the movement, and the whole of it comes from
 * this origin.
 *
 * The second half is the point. CONTEXT.md §9 forbids anything that sends data to a third
 * party; an earlier version embedded a YouTube player as a deliberate exception, and this
 * asserts the exception is gone rather than merely unused.
 */
test('справка показывает движение и не ходит ни к кому на сторону', async ({ page }) => {
  const external: string[] = []
  page.on('request', (request) => {
    if (THIRD_PARTY.test(request.url())) external.push(request.url())
  })

  await openWorkout(page)

  await test.step('текст на месте', async () => {
    await guideToggle(page, 'Присед со штангой').click()
    await expect(page.getByText('Гриф лежит на задних дельтах, не на шее')).toBeVisible()
    await expect(page.getByText('Пятки отрываются от пола')).toBeVisible()
  })

  await test.step('у приседа — свой ролик, играет сам и без звука', async () => {
    const clip = page.locator('video.guide-media-box')
    await expect(clip).toHaveCount(1)
    await expect(clip).toHaveAttribute('src', /\/media\/squat_bb\.mp4$/)
    // Muted and inline are not preferences: iOS refuses to autoplay anything with sound, and
    // without playsinline the video takes the screen and throws the user out of the workout.
    expect(await clip.evaluate((v: HTMLVideoElement) => v.muted)).toBe(true)
    expect(await clip.evaluate((v: HTMLVideoElement) => v.playsInline)).toBe(true)
    expect(await clip.evaluate((v: HTMLVideoElement) => v.loop)).toBe(true)
    // It has to actually decode: a file the phone cannot play is worse than none.
    await expect
      .poll(() => clip.evaluate((v: HTMLVideoElement) => v.readyState), { timeout: 15_000 })
      .toBeGreaterThan(0)
  })

  await test.step('у румынской тяги — два кадра', async () => {
    await guideToggle(page, 'Румынская тяга').click()
    const frames = page.locator('.guide-frames img')
    await expect(frames).toHaveCount(2)
    await expect(frames.nth(0)).toHaveAttribute('src', /\/media\/rdl-0\.jpg$/)
    await expect(frames.nth(1)).toHaveAttribute('src', /\/media\/rdl-1\.jpg$/)
  })

  await test.step('подпись называет автора и лицензию', async () => {
    await expect(page.getByText('CC BY 3.0').first()).toBeVisible()
  })

  await test.step('ни одного запроса на сторону', async () => {
    expect(external, 'приложение целиком первой стороны').toEqual([])
  })
})

/**
 * The reference is read at the gym, where there may be no signal at all. Once a guide has
 * been opened online its demonstration is in the service worker's cache, and everything —
 * text from IndexedDB, media from the cache — has to survive a cold start with no network.
 */
test('после первого открытия справка и показ работают без сети', async ({ page, context }) => {
  await openWorkout(page)
  await guideToggle(page, 'Присед со штангой').click()
  const clip = page.locator('video.guide-media-box')
  await expect
    .poll(() => clip.evaluate((v: HTMLVideoElement) => v.readyState), { timeout: 15_000 })
    .toBeGreaterThan(0)

  await context.setOffline(true)
  await page.reload()

  await expect(page.getByText('Присед со штангой')).toBeVisible()
  await guideToggle(page, 'Присед со штангой').click()
  await expect(page.getByText('Гриф лежит на задних дельтах, не на шее')).toBeVisible()

  // Served from the cache: it decodes with no network at all.
  const offlineClip = page.locator('video.guide-media-box')
  await expect
    .poll(() => offlineClip.evaluate((v: HTMLVideoElement) => v.readyState), { timeout: 15_000 })
    .toBeGreaterThan(0)
  await expect(page.getByText('Показ не загрузился — нет сети')).toHaveCount(0)
})
