// Brings up the real binary on a clean database and creates a user in it.
// An end-to-end test should check what actually ships to the server, not a stand-in API.
import { execFileSync, spawn } from 'node:child_process'
import { mkdtempSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const port = process.argv[2] ?? '8099'
const root = resolve(dirname(fileURLToPath(import.meta.url)), '..', '..')
const binary = join(root, 'gymtracker')
const password = process.env.E2E_PASSWORD ?? 'очень-секретный-пароль'

const dataDir = mkdtempSync(join(tmpdir(), 'gymtracker-e2e-'))
const env = {
  ...process.env,
  GYM_DB: join(dataDir, 'gymtracker.db'),
  GYM_BACKUP_DIR: join(dataDir, 'backups'),
  GYM_PROGRAMS: join(root, 'programs'),
  GYM_GUIDES: join(root, 'guides', 'exercises.json'),
  GYM_ADDR: `127.0.0.1:${port}`,
}

execFileSync(binary, ['adduser', 'igor', '--admin', '--name=Игорь'], {
  env,
  input: `${password}\n`,
  stdio: ['pipe', 'inherit', 'inherit'],
})

const server = spawn(binary, [], { env, stdio: 'inherit' })

const stop = () => {
  server.kill()
  rmSync(dataDir, { recursive: true, force: true })
}
process.on('SIGTERM', () => { stop(); process.exit(0) })
process.on('SIGINT', () => { stop(); process.exit(0) })
process.on('exit', stop)
