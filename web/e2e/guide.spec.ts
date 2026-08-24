import { expect, Page, test } from '@playwright/test'

const PASSWORD = process.env.E2E_PASSWORD ?? 'очень-секретный-пароль'

/** Every host the video could pull something from if the player were not held back. */
const THIRD_PARTY = /youtube|ytimg|googlevideo|google\.com|gstatic|doubleclick/

async function openFirstGuide(page: Page): Promise<void> {
  await page.goto('/')
  await page.getByPlaceholder('Пароль').fill(PASSWORD)
  await page.getByRole('button', { name: 'Войти' }).click()
  await page.getByRole('button', { name: /1\. Ноги/ }).click()
  await expect(page.getByText('Присед со штангой')).toBeVisible()
  await page.getByRole('button', { name: 'Как выполнять' }).first().click()
}

/**
 * The guide opens inside the card, and until play is tapped the application is still
 * strictly first-party.
 *
 * The second half of this is the point: CONTEXT.md §9 forbids anything that sends data to
 * a third party, and the video is the single deliberate exception to that. The exception is
 * only acceptable while it costs nothing to a screen that is merely open, so that is what
 * gets asserted — not the player's behaviour, but the silence before it.
 */
test('справка раскрывается в карточке и до нажатия «плей» ничего не грузит со стороны', async ({
  page,
}) => {
  const external: string[] = []
  page.on('request', (request) => {
    if (THIRD_PARTY.test(request.url())) external.push(request.url())
  })
  // The frame is answered locally: what is being checked is that the application asks for
  // it at the right moment, not that YouTube is reachable from the build machine.
  await page.route(/youtube-nocookie\.com/, (route) =>
    route.fulfill({ status: 200, contentType: 'text/html', body: '<!doctype html>' }),
  )

  await openFirstGuide(page)

  await test.step('текст на месте', async () => {
    await expect(page.getByText('Гриф лежит на задних дельтах, не на шее')).toBeVisible()
    await expect(page.getByText('Пятки отрываются от пола')).toBeVisible()
  })

  await test.step('кадра ещё нет и в сеть никто не ходил', async () => {
    await expect(page.locator('iframe')).toHaveCount(0)
    await expect(page.locator('.guide-video-facade')).toBeVisible()
    expect(external, 'до тапа по «плей» ни одного запроса на сторону').toEqual([])
  })

  await test.step('кадр появляется только по явному тапу', async () => {
    await page.locator('.guide-video-facade').click()
    const frame = page.locator('iframe')
    await expect(frame).toHaveCount(1)
    await expect(frame).toHaveAttribute(
      'src',
      /^https:\/\/www\.youtube-nocookie\.com\/embed\/7Yg2YVNdd8c\?/,
    )
    // playsinline: without it iOS takes the video full screen and throws the user out of
    // the workout.
    await expect(frame).toHaveAttribute('src', /playsinline=1/)

    // The referrer is not optional. The embedded player identifies the embedding site by
    // the Referer header and refuses with "Ошибка 153. Ошибка настройки видеопроигрывателя"
    // when it gets none — which is exactly what no-referrer did in production. The document
    // policy is same-origin, so this attribute is the only thing that lets the origin
    // through, and it must send no more than the origin. Nothing in this file talks to
    // YouTube, so this assertion is what stands in for the player refusing to load.
    await expect(frame).toHaveAttribute('referrerpolicy', 'strict-origin-when-cross-origin')
  })

  await test.step('свернуть — кадр исчезает', async () => {
    await page.getByRole('button', { name: 'Скрыть технику' }).first().click()
    await expect(page.locator('iframe')).toHaveCount(0)
  })
})

/**
 * The reference is read at the gym, where there may be no signal at all. The text has to be
 * there after a cold start with no network — it lives in IndexedDB like everything else —
 * and the video has to say so instead of showing a frame that will never load.
 */
test('без сети справка читается, а видео честно говорит, что не откроется', async ({
  page,
  context,
}) => {
  // One online visit is what puts the reference on the device.
  await openFirstGuide(page)
  await expect(page.getByText('Гриф лежит на задних дельтах, не на шее')).toBeVisible()

  await context.setOffline(true)
  await page.reload()

  // The shell comes from the service worker and the data from IndexedDB — not one request
  // reaches the server.
  await expect(page.getByText('Присед со штангой')).toBeVisible()

  // Chromium's offline emulation does not survive the navigation: after a reload
  // navigator.onLine reads true again while the context is still offline. Toggling puts the
  // flag back where the emulation says it should be.
  await context.setOffline(false)
  await context.setOffline(true)
  await page.getByRole('button', { name: 'Как выполнять' }).first().click()

  await expect(page.getByText('Гриф лежит на задних дельтах, не на шее')).toBeVisible()
  await expect(page.getByText('Без сети видео не откроется')).toBeVisible()
  await expect(page.locator('.guide-video-facade')).toHaveCount(0)
})
