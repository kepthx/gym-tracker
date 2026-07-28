import { render } from 'preact'
import { App } from './app'

const root = document.getElementById('app')
if (root) render(<App />, root)

// The service worker caches ONLY the app shell and never /api/*: a cached API response is
// indistinguishable in the UI from fresh data, and showing yesterday's workout as today's
// is the worst thing that could happen here.
if ('serviceWorker' in navigator && import.meta.env.PROD) {
  addEventListener('load', () => {
    void navigator.serviceWorker.register('/sw.js')
  })
}
