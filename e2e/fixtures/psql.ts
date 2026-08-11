import { execSync } from 'node:child_process'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const PROJECT_ROOT = resolve(fileURLToPath(import.meta.url), '../../..')

// Two ways to reach Postgres, because the two environments differ.
//
// Locally it is the compose service and the host usually has no psql
// client, so the only route in is `docker compose exec` — the same path
// `make seed` uses, with the project root as cwd so the compose file
// resolves. In CI it is a service container with the port published and
// a psql client installed, and there is no compose stack at all.
//
// Trying compose first and calling it a day is what made SQL-seeded
// specs skip silently in CI: the probe failed, the reachability flag
// went false, and the specs reported success by not running (#216).
const DIRECT_PSQL_URL =
  process.env.TEST_DATABASE_URL ??
  process.env.DATABASE_URL ??
  'postgres://embookshelf:embookshelf@localhost:5432/embookshelf?sslmode=disable'

type PsqlRunner = (sql: string) => void

function composeRunner(): PsqlRunner {
  return (sql) =>
    execSync(
      'docker compose -f compose.dev.yml exec -T postgres psql -U embookshelf -d embookshelf -v ON_ERROR_STOP=1',
      { input: sql, stdio: ['pipe', 'pipe', 'inherit'], cwd: PROJECT_ROOT }
    )
}

function directRunner(): PsqlRunner {
  return (sql) =>
    execSync(`psql "${DIRECT_PSQL_URL}" -v ON_ERROR_STOP=1`, {
      input: sql,
      stdio: ['pipe', 'pipe', 'inherit'],
    })
}

// Picks whichever route actually answers, once per process.
const runner: PsqlRunner | null = (() => {
  for (const build of [directRunner, composeRunner]) {
    const candidate = build()
    try {
      candidate('SELECT 1;')
      return candidate
    } catch {
      // Try the other one.
    }
  }
  return null
})()

/** True when some psql route to the test database answered. */
export const postgresReachable = runner !== null

export function psql(sql: string) {
  if (!runner) throw new Error('no psql route to the test database')
  runner(sql)
}

export function loadSqlFixture(path: string) {
  psql(readFileSync(path, 'utf8'))
}
