import { realpathSync } from 'node:fs';
import { resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

function real(path) {
  try {
    return realpathSync(path);
  } catch {
    return resolve(path);
  }
}

export function isMain(url) {
  const entry = process.argv[1];
  return Boolean(entry) && real(entry) === real(fileURLToPath(url));
}
