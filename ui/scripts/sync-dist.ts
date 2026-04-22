// Copies the SPA shell (_shell.html + client assets) from ui/dist/client
// → internal/staticfs/dist so the Go binary can embed it. The SSR server
// bundle under dist/server is intentionally left behind — it exists only
// for the TanStack Start prerender pass.
import {
  copyFile,
  cp,
  mkdir,
  readFile,
  readdir,
  rm,
  stat,
  writeFile,
} from "node:fs/promises"
import { existsSync } from "node:fs"
import path from "node:path"
import { fileURLToPath } from "node:url"

const here = path.dirname(fileURLToPath(import.meta.url))
const uiDir = path.resolve(here, "..")
const shellRoot = path.join(uiDir, "dist", "client")
const outDist = path.resolve(uiDir, "..", "internal", "staticfs", "dist")

if (!existsSync(shellRoot)) {
  throw new Error(`expected ${shellRoot} after vite build`)
}

await rm(outDist, { recursive: true, force: true })
await mkdir(outDist, { recursive: true })
await writeFile(path.join(outDist, ".gitkeep"), "")

for (const entry of await readdir(shellRoot)) {
  const from = path.join(shellRoot, entry)
  const to = path.join(outDist, entry)
  const info = await stat(from)
  if (info.isDirectory()) {
    await cp(from, to, { recursive: true })
  } else {
    await copyFile(from, to)
  }
}

// Go's SPA fallback looks for index.html; TanStack Start names the shell
// _shell.html. Duplicate the content so both paths resolve.
const shellHtml = path.join(outDist, "_shell.html")
const indexHtml = path.join(outDist, "index.html")
if (existsSync(shellHtml) && !existsSync(indexHtml)) {
  await writeFile(indexHtml, await readFile(shellHtml, "utf8"))
}

console.log(`synced SPA shell → ${path.relative(uiDir, outDist)}`)
