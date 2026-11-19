// Keep the existing entry point; VM modules allow controlled theme CSS failures.
import {execFileSync} from 'node:child_process'
import {fileURLToPath} from 'node:url'
execFileSync(process.execPath,['--experimental-vm-modules',fileURLToPath(new URL('./test-theme-preset-sync.mjs',import.meta.url))],{stdio:'inherit'})
