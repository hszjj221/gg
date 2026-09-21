const { mkdirSync } = require('node:fs');
const { join, resolve } = require('node:path');
const { spawnSync } = require('node:child_process');

const uiRoot = resolve(__dirname, '..');
const outputDir = join(uiRoot, 'bin');
mkdirSync(outputDir, { recursive: true });
const binary = join(outputDir, process.platform === 'win32' ? 'ggd.exe' : 'ggd');
const result = spawnSync('go', ['build', '-o', binary, '../cmd/ggd'], {
  cwd: uiRoot,
  stdio: 'inherit',
});
if (result.error) throw result.error;
process.exit(result.status ?? 1);
