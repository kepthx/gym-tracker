/*
 * Service worker: the app shell only.
 *
 * The prime rule is NEVER to touch /api/*. A cached API response is indistinguishable in
 * the UI from fresh data, and showing yesterday's workout as today's is the worst thing
 * that could happen here. All data lives in IndexedDB, and it gets there only through an
 * explicit sync.
 *
 * The outbox queue does not live here either: Background Sync does not exist on iOS, so a
 * worker would gain nothing while adding a second copy of the state.
 */

/*
 * The base the app is served under, derived from where this file itself sits: at /sw.js it
 * is "/", at /gym/sw.js it is "/gym/". This file is copied verbatim from public/, so Vite's
 * base rewriting never reaches it — reading the location is what keeps the worker in step
 * with the rest of the app instead of silently caching the wrong paths.
 *
 * It matches the worker's own scope, which the browser fixes to this directory.
 */
const BASE = new URL('./', self.location).pathname

const SHELL = 'shell-v1'

self.addEventListener('install', (event) => {
  event.waitUntil(
    caches.open(SHELL).then((cache) => cache.addAll([BASE])).then(() => self.skipWaiting()),
  )
})

self.addEventListener('activate', (event) => {
  event.waitUntil(
    caches
      .keys()
      .then((names) => Promise.all(names.filter((n) => n !== SHELL).map((n) => caches.delete(n))))
      .then(() => self.clients.claim()),
  )
})

self.addEventListener('fetch', (event) => {
  const request = event.request
  if (request.method !== 'GET') return

  const url = new URL(request.url)
  if (url.origin !== self.location.origin) return
  // Under no circumstances.
  if (url.pathname.startsWith(`${BASE}api/`)) return

  // Build output carries a content hash in the filename, so those files are immutable:
  // serving them from cache is safe, and safe forever.
  //
  // The exercise demonstrations under /media/ are immutable by the same convention —
  // replacing one means a new file name — and they are the reason the guide opens at the gym
  // with no signal at all. They are NOT under /api/, precisely so this line can exist.
  if (url.pathname.startsWith(`${BASE}assets/`) || url.pathname.startsWith(`${BASE}media/`)) {
    event.respondWith(
      caches.match(request).then(
        (hit) =>
          hit ??
          fetch(request).then((response) => {
            if (response.ok) {
              const copy = response.clone()
              void caches.open(SHELL).then((cache) => cache.put(request, copy))
            }
            return response
          }),
      ),
    )
    return
  }

  // The shell: network first, so an update arrives right away, cache on failure.
  // Without this the app would not open at the gym with no signal.
  event.respondWith(
    fetch(request)
      .then((response) => {
        if (response.ok) {
          const copy = response.clone()
          void caches.open(SHELL).then((cache) => cache.put(BASE, copy))
        }
        return response
      })
      .catch(() => caches.match(request).then((hit) => hit ?? caches.match(BASE))),
  )
})
