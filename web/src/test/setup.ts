// In tests IndexedDB is swapped for an in-memory implementation: the wrapper around
// storage is exactly where the "state + queue" atomicity lives, and checking that with
// stubs would be pointless.
import 'fake-indexeddb/auto'
