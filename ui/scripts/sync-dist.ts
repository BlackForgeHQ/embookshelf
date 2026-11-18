// Copies the static export (ui/out) → internal/staticfs/dist so the Go
// binary can embed it.
import { cp, mkdir, rm, writeFile } from "node:fs/promises"
import { existsSync } from "node:fs"
import path from "node:path"
import { fileURLToPath } from "node:url"

const here = path.dirname(fileURLToPath(import.meta.url))
const uiDir = path.resolve(here, "..")
const exportRoot = path.join(uiDir, "out")
const outDist = path.resolve(uiDir, "..", "internal", "staticfs", "dist")

if (!existsSync(path.join(exportRoot, "index.html"))) {
  throw new Error(`expected ${exportRoot}/index.html after next build`)
}

await rm(outDist, { recursive: true, force: true })
await mkdir(outDist, { recursive: true })
await writeFile(path.join(outDist, ".gitkeep"), "")
await cp(exportRoot, outDist, { recursive: true })

console.log(`synced static export → ${path.relative(uiDir, outDist)}`)
