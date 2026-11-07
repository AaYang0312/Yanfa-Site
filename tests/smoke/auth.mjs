// V03 identity smoke. Secrets remain in memory; evidence contains statuses,
// assertions and hashes only. Set SMOKE_MANAGE_SERVER=1 to own serve/restart/lock
// scenarios; otherwise exercise an already running instance without stopping it.
import { readFile, writeFile, mkdir, mkdtemp, readdir, lstat, rm, cp } from 'node:fs/promises';
import { resolve, join, dirname } from 'node:path';
import { createHash } from 'node:crypto';
import { spawn } from 'node:child_process';
import { setTimeout as delay } from 'node:timers/promises';

const args = process.argv.slice(2);
const option = name => { const i = args.indexOf(name); return i < 0 ? undefined : args[i + 1]; };
const base = option('--base');
const out = option('--out');
if (!base || !out || !process.env.SMOKE_ADMIN_PASSWORD_FILE) {
  throw new Error('Usage: SMOKE_ADMIN_PASSWORD_FILE=<0600 file> node tests/smoke/auth.mjs --base <origin> --out <evidence>');
}
const check = (value, label) => { if (!value) throw new Error(label); };
const managed = process.env.SMOKE_MANAGE_SERVER === '1';
const binary = resolve(process.env.SMOKE_YUANFA_BINARY || '.work/bin/yuanfa');
const dataDir = resolve(process.env.DATA_DIR || '.work/worker/normal/data');
const passwordFile = resolve(process.env.SMOKE_ADMIN_PASSWORD_FILE);
const passwordStat = await lstat(passwordFile);
check((passwordStat.mode & 0o077) === 0, 'password file must have private permissions');
const password = (await readFile(passwordFile, 'utf8')).replace(/\r?\n$/, '');
const report = { checks: [], managed_lifecycle: managed, commands: [], http: [] };
const passed = label => { report.checks.push(label); console.log(`PASS ${label}`); };
const env = { ...process.env, SITE_URL: base, HTTP_ADDR: new URL(base).host, DATA_DIR: dataDir };
let owned;

async function request(path, { method = 'GET', body, cookie, csrf, origin, authorization, raw } = {}) {
  const headers = {};
  if (body !== undefined || raw !== undefined) headers['Content-Type'] = 'application/json';
  if (cookie) headers.Cookie = cookie;
  if (csrf) headers['X-CSRF-Token'] = csrf;
  if (origin !== undefined) headers.Origin = origin;
  if (authorization) headers.Authorization = authorization;
  const response = await fetch(`${base}${path}`, { method, headers, body: raw ?? (body === undefined ? undefined : JSON.stringify(body)), redirect: 'manual' });
  const text = await response.text();
  let json;
  try { json = JSON.parse(text); } catch { json = null; }
  return { status: response.status, headers: response.headers, json, text };
}
async function expect(path, options, status, label) {
  const result = await request(path, options);
  report.http.push({ method: options.method || 'GET', path, label, status: result.status, expected_status: status });
  check(result.status === status, `${label}: expected HTTP ${status}, got ${result.status}`);
  check(result.headers.get('cache-control') === 'no-store', `${label}: missing no-store`);
  return result;
}
function start() {
  const child = spawn(binary, ['serve'], { env, stdio: ['ignore', 'pipe', 'pipe'] });
  child.output = '';
  child.stdout.on('data', b => { child.output += b; });
  child.stderr.on('data', b => { child.output += b; });
  child.completed = new Promise((resolve, reject) => { child.once('error', reject); child.once('exit', (code, signal) => resolve({ code, signal })); });
  return child;
}
async function waitLive(child) {
  for (let i = 0; i < 100; i++) {
    check(child.exitCode === null && child.signalCode === null, 'owned serve exited before health');
    try { if ((await request('/health/live')).status === 200) return; } catch {}
    await delay(50);
  }
  throw new Error('owned serve failed to become live');
}
async function stop() {
  if (!owned) return;
  const child = owned;
  child.kill('SIGTERM');
  const timeout = setTimeout(() => child.kill('SIGKILL'), 20000);
  try {
    const result = await child.completed;
    report.commands.push({ argv: ['serve'], exit_code: result.code, signal: result.signal, output: child.output.trim() });
    check(result.code === 0, 'owned serve did not stop cleanly');
  } finally { clearTimeout(timeout); owned = undefined; }
}
async function cli(argv, override = {}) {
  const child = spawn(binary, argv, { env: { ...env, ...override }, stdio: ['ignore', 'pipe', 'pipe'] });
  let text = '';
  child.stdout.on('data', b => { text += b; }); child.stderr.on('data', b => { text += b; });
  const timer = setTimeout(() => child.kill('SIGKILL'), 10000);
  try {
    const result = await new Promise((resolve, reject) => { child.once('error', reject); child.once('exit', (code, signal) => resolve({ code, signal })); });
    report.commands.push({ argv, exit_code: result.code, signal: result.signal, output: text.trim() });
    return { ...result, text };
  } finally { clearTimeout(timer); }
}
async function snapshot(dir) {
  const result = {};
  async function walk(relative) {
    for (const name of (await readdir(join(dir, relative))).sort()) {
      const path = join(relative, name); const stat = await lstat(join(dir, path));
      if (stat.isDirectory()) await walk(path);
      else {
        check(stat.isFile(), 'unexpected nonregular data file');
        result[path] = { sha256: createHash('sha256').update(await readFile(join(dir, path))).digest('hex'), bytes: stat.size, inode: stat.ino, mtime_ms: stat.mtimeMs };
      }
    }
  }
  await walk(''); return result;
}
const equalSnapshot = (a, b, label) => check(JSON.stringify(a) === JSON.stringify(b), label);

await mkdir(out, { recursive: true });
try {
  if (managed) {
    let listening = false;
    try { await request('/health/live'); listening = true; } catch {}
    check(!listening, 'managed smoke requires an unused port; existing server was not stopped');
    owned = start(); await waitLive(owned);
  }
  const live = await expect('/health/live', {}, 200, 'live'); check(live.json?.status === 'ok', 'live body');
  const ready = await expect('/health/ready', {}, 503, 'ready before first release');
  check(ready.json?.database === 'ready' && ready.json?.artifacts === 'uninitialized' && ready.json?.ready === false, 'empty database readiness');
  await expect('/', {}, 503, 'unpublished website');
  passed('real live/ready; empty website 503');
  await expect('/api/admin/session', {}, 401, 'anonymous session');
  await expect('/api/admin/login', { method: 'POST', origin: `${base}.evil`, body: { username: 'admin', password } }, 403, 'foreign login origin');
  await expect('/api/admin/login', { method: 'POST', body: { username: 'admin', password } }, 403, 'missing login origin');
  await expect('/api/admin/login', { method: 'POST', origin: base, body: { username: 'admin', password: 'deliberately-wrong' } }, 401, 'bad password');
  await expect('/api/admin/login', { method: 'POST', origin: base, raw: '{' }, 400, 'malformed login JSON');
  await expect('/api/admin/login', { method: 'POST', origin: base, body: { username: 'admin', password, role: 'admin' } }, 422, 'unknown login field');
  await expect('/api/admin/login', { method: 'POST', origin: base, raw: JSON.stringify({ username: 'admin', password }) + ' '.repeat(256 * 1024) }, 413, 'JSON byte limit');
  const login = await expect('/api/admin/login', { method: 'POST', origin: base, body: { username: 'admin', password } }, 200, 'login');
  const setCookie = login.headers.get('set-cookie');
  check(setCookie?.includes('HttpOnly') && setCookie.includes('SameSite=Strict') && setCookie.includes('Max-Age=43200'), 'cookie protection/lifetime');
  check(new URL(base).protocol === 'https:' ? setCookie.includes('Secure') : !setCookie.includes('Secure'), 'cookie Secure policy');
  const cookie = setCookie.split(';')[0]; const csrf = login.json?.csrf_token;
  check(typeof csrf === 'string' && csrf.length === 64 && login.json?.admin_id === 'admin', 'session response identity/CSRF');
  const expires = Date.parse(login.json.expires_at) - Date.now();
  check(expires > 43100 * 1000 && expires <= 43200 * 1000, '12h server session expiry');
  passed('login; wrong password; exact Origin; strict JSON and 256KiB; protected 12h Cookie');
  const tokenInput = { name: 'V03 isolated token', expires_at: new Date(Date.now() + 3600000).toISOString(), scopes: ['products:read', 'products:write', 'media:read', 'candidates:write', 'publications:write'] };
  await expect('/api/admin/mcp-tokens', { method: 'POST', origin: base, cookie, body: tokenInput }, 403, 'missing CSRF');
  await expect('/api/admin/mcp-tokens', { method: 'POST', origin: base, cookie, csrf: 'wrong', body: tokenInput }, 403, 'wrong CSRF');
  await expect('/api/admin/mcp-tokens', { method: 'POST', origin: `${base}/`, cookie, csrf, body: tokenInput }, 403, 'nonexact Origin');
  await expect('/api/admin/mcp-tokens', { method: 'POST', cookie, csrf, body: tokenInput }, 403, 'missing write Origin');
  await expect('/api/admin/mcp-tokens', { method: 'POST', origin: base, cookie, csrf, body: { ...tokenInput, expires_at: '2020-01-01T00:00:00Z' } }, 422, 'past token expiry');
  const issued = await expect('/api/admin/mcp-tokens', { method: 'POST', origin: base, cookie, csrf, body: tokenInput }, 201, 'issue token');
  const tokenId = issued.json?.token?.id; const secret = issued.json?.secret;
  check(typeof tokenId === 'string' && typeof secret === 'string' && secret.startsWith('t_'), 'issued credential');
  await expect('/api/admin/mcp-tokens', { authorization: `Bearer ${secret}` }, 403, 'Bearer cannot manage credentials');
  let list = await expect('/api/admin/mcp-tokens', { cookie }, 200, 'list token');
  check(list.json?.items.some(x => x.id === tokenId && x.revoked_at === null), 'issued token visible');
  check(!list.text.includes(secret) && !list.text.includes('secret_hash') && !list.text.includes('"secret"'), 'list leaks no secret');
  passed('session+CSRF+exact Origin on writes; future token expiry; one-time secret; Bearer denied management');
  if (managed) {
    const before = await snapshot(dataDir);
    for (const argv of [['serve'], ['migrate'], ['init-admin', '--username', 'admin']]) {
      const conflict = await cli(argv);
      check(conflict.code === 73 && conflict.text.trim() === 'DATA_DIR_LOCKED', `lock protocol for ${argv[0]}`);
      equalSnapshot(before, await snapshot(dataDir), `data mutated by conflicting ${argv[0]}`);
    }
    report.lock_snapshot = before;
    passed('second serve/migrate/init-admin exit 73; all data SHA/inode/mtime unchanged');
    await stop();
    const reacquire = await cli(['migrate']); check(reacquire.code === 0, 'migrate could not reacquire stopped server lock');
    owned = start(); await waitLive(owned);
    const session = await expect('/api/admin/session', { cookie }, 200, 'persisted session');
    check(session.json?.csrf_token === csrf && session.json?.expires_at === login.json.expires_at, 'session changed on restart');
    list = await expect('/api/admin/mcp-tokens', { cookie }, 200, 'persisted token');
    check(list.json?.items.some(x => x.id === tokenId && x.revoked_at === null), 'token not persistent');
    passed('real SIGTERM/restart; lock reacquisition; session/CSRF/expiry/token persistence');
  }
  await expect(`/api/admin/mcp-tokens/${tokenId}`, { method: 'DELETE', origin: base, cookie, csrf }, 204, 'revoke token');
  list = await expect('/api/admin/mcp-tokens', { cookie }, 200, 'list revoked token');
  check(list.json?.items.some(x => x.id === tokenId && typeof x.revoked_at === 'string'), 'revocation not recorded');
  await expect(`/api/admin/mcp-tokens/${tokenId}`, { method: 'DELETE', origin: base, cookie, csrf }, 204, 'repeat revocation');
  const logout = await expect('/api/admin/logout', { method: 'POST', origin: base, cookie, csrf }, 204, 'logout');
  check(logout.headers.getSetCookie().some(x => x.startsWith('yuanfa_preview=') && x.includes('Max-Age=0')), 'logout did not clear preview');
  await expect('/api/admin/session', { cookie }, 401, 'revoked session');
  passed('HTTP Token revocation persisted; logout revokes session and clears preview');
  if (managed) {
    await stop(); owned = start(); await waitLive(owned);
    await expect('/api/admin/session', { cookie }, 401, 'logout persisted after restart');
    const fresh = await expect('/api/admin/login', { method: 'POST', origin: base, body: { username: 'admin', password } }, 200, 'login after restart');
    const freshCookie = fresh.headers.get('set-cookie').split(';')[0];
    list = await expect('/api/admin/mcp-tokens', { cookie: freshCookie }, 200, 'revocation after restart');
    check(list.json?.items.some(x => x.id === tokenId && x.revoked_at !== null), 'token revocation lost after restart');
    await stop();
    const marker = join(dataDir, '.restore-incomplete');
    await writeFile(marker, 'V03 incomplete restore\n', { mode: 0o600, flag: 'wx' });
    const marked = await snapshot(dataDir);
    const refused = await cli(['serve']);
    check(refused.code === 1 && refused.text.trim() === 'RESTORE_INCOMPLETE', 'incomplete restore started');
    equalSnapshot(marked, await snapshot(dataDir), 'restore rejection touched database/files');
    await rm(marker);
    const { DatabaseSync } = await import('node:sqlite');
    const badDir = await mkdtemp(join(dirname(dataDir), 'auth-bad-migration-'));
    try {
      await cp(dataDir, badDir, { recursive: true });
      const db = new DatabaseSync(join(badDir, 'db/catalog.sqlite'));
      db.prepare("UPDATE schema_migrations SET sha256=? WHERE version=1").run('0'.repeat(64)); db.close();
      const badBefore = await snapshot(badDir);
      const bad = await cli(['serve'], { DATA_DIR: badDir });
      check(bad.code === 1 && bad.text.includes('OPERATION_FAILED'), 'tampered migration accepted serve');
      equalSnapshot(badBefore, await snapshot(badDir), 'failed migration history wrote data');
    } finally { await rm(badDir, { recursive: true, force: true }); }
    passed('restart preserves logout/revocation; restore marker rejects before DB; migration mismatch rejects unchanged');
  }
  report.success = true;
} catch (error) {
  report.success = false; report.failure = error.message;
  throw error;
} finally {
  try { await stop(); } finally { await writeFile(join(out, 'auth-smoke.json'), JSON.stringify(report, null, 2) + '\n'); }
}
