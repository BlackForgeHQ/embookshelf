import { copyFile, rm } from 'node:fs/promises';
import { dirname, extname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

// The watcher polls ./bookdrop/ relative to the binary's working directory.
// The server is booted from the repo root (make build && ./tmp/embookshelf),
// so BookDrop lives one level above the e2e/ directory.
const __dirname = dirname(fileURLToPath(import.meta.url));
const BOOKDROP_DIR = resolve(__dirname, '..', '..', 'bookdrop');

const FIXTURES: Record<'epub' | 'pdf', string> = {
  epub: resolve(__dirname, 'files', 'sample.epub'),
  pdf: resolve(__dirname, 'files', 'sample.pdf'),
};

export type DropFormat = keyof typeof FIXTURES;

// dropFixture copies the sample file into ./bookdrop/ under a fresh
// filename (so multiple test runs don't collide on the path-uniqueness
// constraint) and returns both the filename and a cleanup thunk.
export async function dropFixture(
  format: DropFormat,
  label: string,
): Promise<{ filename: string; cleanup: () => Promise<void> }> {
  const src = FIXTURES[format];
  const filename = `e2e-${label}-${Date.now()}${extname(src)}`;
  const dest = resolve(BOOKDROP_DIR, filename);

  await copyFile(src, dest);

  return {
    filename,
    cleanup: async () => {
      await rm(dest, { force: true });
    },
  };
}

// Back-compat shim: existing specs call dropEpub directly.
export const dropEpub = (label: string) => dropFixture('epub', label);
