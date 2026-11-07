// Reproducible V14 command owner. Runs all official entries once, records actual
// cwd/argv/status/stdout/stderr and joins only its own Go process before cleanup.
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { mkdir, readdir, readFile, writeFile, rm, copyFile } from 'node:fs/promises';
import { join, resolve } from 'node:path';
import { parseArgs } from 'node:util';
import { initialize, startServer, stopServer, live, passwordFile } from '../../smoke/publication.mjs';
const { values } = parseArgs({ options: { go: { type: 'string' }, root: { type: 'string' }, 'task-number': { type: 'string' }, out: { type: 'string' } } });
assert.ok(values.go && values.root && values['task-number'] && values.out);
const root = resolve(values.root), out = resolve(values.out), number = Number(values['task-number']);
assert.equal(process.cwd(), root); assert.equal(process.env.DATA_DIR, join(root, '.work', process.env.RUN_ROLE, 'normal/data'));
await mkdir(out, { recursive: true, mode: 0o700 });
const env = { ...process.env };
for (const key of Object.keys(env)) if (/^MODEL_/i.test(key) || ['http_proxy', 'https_proxy', 'all_proxy', 'npm_config_proxy', 'npm_config_https_proxy', 'YUANFA_INTEGRATION_DIR'].includes(key.toLowerCase())) delete env[key];
delete env.YUANFA_INTEGRATION_DIR;
for (const key of ['TMPDIR', 'GOTMPDIR']) assert.ok(env[key]?.startsWith(root + '/.work/'));
const report = { success: false, cwd: root, commands: [], initialization: { commands: [] }, cleanup: {} };
async function run(name, argv, extra = {}) {
  const started = Date.now(); let stdout = '', stderr = '';
  const child = spawn(argv[0], argv.slice(1), { cwd: root, env: { ...env, ...extra }, stdio: ['ignore', 'pipe', 'pipe'] });
  child.stdout.on('data', bytes => { stdout += bytes; }); child.stderr.on('data', bytes => { stderr += bytes; });
  const status = await new Promise((res, rej) => { child.once('error', rej); child.once('close', (exit_code, signal) => res({ exit_code, signal })); });
  const record = { cwd: root, argv, started_at: new Date(started).toISOString(), duration_ms: Date.now() - started, ...status, stdout, stderr };
  const recordPath = join(out, name + '.json'); await writeFile(recordPath, JSON.stringify(record, null, 2), { mode: 0o600 });
  report.commands.push({ name, path: recordPath, argv, exit_code: status.exit_code, signal: status.signal });
  console.log(name + ': exit=' + status.exit_code + '\n' + stdout + stderr);
  assert.equal(status.exit_code, 0, name); return record;
}
let owned, ownsData = false, serverStdout = '', serverStderr = '';
const binary = resolve(env.SMOKE_YUANFA_BINARY), data = env.DATA_DIR;
try {
  await mkdir(data, { recursive: true, mode: 0o700 }); assert.deepEqual(await readdir(data), [], 'fresh assigned normal DATA_DIR required');
  ownsData = true;
  for (const file of ['tests/smoke/platform.mjs', 'tests/browser/platform.mjs', 'tests/integration/platform/browser.mjs', 'tests/integration/platform/capture.mjs', 'tests/integration/platform/run.mjs']) await run('syntax-' + file.split('/').at(-2) + '-' + file.split('/').at(-1), ['node', '--check', file]);
  await run('02-site-config', ['npm', 'run', 'site:config']);
  await run('03-build-admin', ['npm', 'run', 'build:admin']);
  await run('04-build-production', [values.go, 'build', '-o', binary, './backend/cmd/yuanfa']);
  await initialize(binary, env, report.initialization);
  report.initialization.cwd = root;
  await writeFile(join(out, '05-initialization.json'), JSON.stringify(report.initialization, null, 2), { mode: 0o600 });
  owned = startServer(binary, env, report); owned.stdout.on('data', b => { serverStdout += b; }); owned.stderr.on('data', b => { serverStderr += b; });
  report.server = { cwd: root, argv: [binary, 'serve'], pid: owned.pid, data_dir: data, base_url: env.SITE_URL, production_build: true };
  await live(env.SITE_URL, owned);
  await run('06-normal-smoke', ['node', 'tests/smoke/platform.mjs', '--base', env.SITE_URL, '--out', join(out, 'V14/normal')]);
  await run('07-browser-tagged', ['node', 'tests/browser/platform.mjs', '--go', values.go, '--root', root, '--task-number', String(number), '--out', join(out, 'V14/tagged')], { TMPDIR: '/proc/self/cwd/.work/browser-tmp' });
  const normal = JSON.parse(await readFile(join(out, 'V14/normal/result.json'), 'utf8'));
  assert.equal(normal.success, true); assert.ok(normal.snapshot_path && normal.release_id);
  await run('08-qa', ['npm', 'run', 'qa'], { TMPDIR: '/proc/self/cwd/.work/browser-tmp', QA_URL: env.SITE_URL, QA_SNAPSHOT: normal.snapshot_path, QA_OUTPUT: join(out, 'V14/qa') });
  report.release_id = normal.release_id; report.snapshot_path = normal.snapshot_path; report.success = true;
} catch (error) { report.error = error.stack; throw error; }
finally {
  try {
    if (ownsData) {
    const status = await stopServer(owned); report.server = { ...report.server, ...status, stdout: serverStdout, stderr: serverStderr }; report.cleanup.server_joined = true;
    const artifacts = join(data, 'artifacts');
    try { for (const id of await readdir(artifacts)) { const target = join(out, 'retained', id); await mkdir(target, { recursive: true }); for (const name of ['catalog.json', 'manifest.json']) await copyFile(join(artifacts, id, name), join(target, name)); } } catch (error) { if (error.code !== 'ENOENT') throw error; }
    try { report.active = JSON.parse(await readFile(join(data, 'state/active.json'), 'utf8')); } catch (error) { if (error.code !== 'ENOENT') throw error; }
    const password = await passwordFile();
    const scan = async directory => { for (const entry of await readdir(directory, { withFileTypes: true })) { const name = join(directory, entry.name); if (entry.isDirectory()) await scan(name); else { assert.ok(!(await readFile(name)).includes(Buffer.from(password)), 'credential leaked into ' + name); } } };
    await scan(out); report.cleanup.credential_found_in_evidence = false;
    await rm(data, { recursive: true, force: true }); report.cleanup.normal_data_removed = true;
    await rm(env.SMOKE_ADMIN_PASSWORD_FILE); report.cleanup.credential_removed = true;
    } else report.cleanup.unowned_data_preserved = true;
  } finally { await writeFile(join(out, 'run.json'), JSON.stringify(report, null, 2), { mode: 0o600 }); }
}
