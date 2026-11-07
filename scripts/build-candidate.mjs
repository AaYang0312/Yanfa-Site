import assert from 'node:assert/strict';
import { readFile, mkdir, cp, lstat, realpath, symlink, writeFile, access } from 'node:fs/promises';
import { resolve, relative, sep } from 'node:path';
import { pathToFileURL } from 'node:url';
import { parseArgs } from 'node:util';
import { createRequire } from 'node:module';
import { spawn } from 'node:child_process';
import { createHash } from 'node:crypto';
import { readSnapshot, publicImages, mediaFilename, sha256, auditSite, walkFiles } from './audit-build.mjs';
import { validateContract } from '../src/generated/validate-contract.mjs';

export const TEMPLATE_PATHS = Object.freeze([
  'src', 'public', 'schemas', 'package.json', 'package-lock.json', 'tsconfig.json', 'astro.config.mjs',
  'scripts/generate-contracts.mjs', 'scripts/export-site-config.mjs', 'scripts/audit-build.mjs',
]);
const within = (parent, child) => child === parent || child.startsWith(`${parent}${sep}`);
async function absent(path) {
  try { await lstat(path); } catch (error) { if (error.code === 'ENOENT') return; throw error; }
  throw new Error(`Output must not exist: ${path}`);
}
export async function copyTemplate(templatePath, destination) {
  const source = await realpath(templatePath);
  const target = resolve(destination);
  await absent(target);
  assert.ok(!within(source, target) || within(resolve(source, '.work'), target), 'Template copy must be isolated from source files');
  await mkdir(target, { recursive: true });
  for (const entry of TEMPLATE_PATHS) {
    await cp(resolve(source, entry), resolve(target, entry), {
      recursive: true,
      filter: async path => {
        const name = relative(source, path).split(sep).join('/');
        if (name === 'src/assets/products' || name === 'src/assets/catalog-media' || name === 'src/data/product-source.json') return false;
        assert.ok(!(await lstat(path)).isSymbolicLink(), `Template symlink is forbidden: ${name}`);
        return true;
      },
    });
  }
  await access(resolve(source, 'node_modules/astro/astro.js'));
  await symlink(resolve(source, 'node_modules'), resolve(target, 'node_modules'), 'dir');
  return target;
}
async function run(command, args, cwd, env) {
  await new Promise((resolveRun, reject) => {
    const child = spawn(command, args, { cwd, env, stdio: 'inherit', detached: true });
    const timer = setTimeout(() => {
      try { process.kill(-child.pid, 'SIGKILL'); } catch { /* exited */ }
    }, 300_000);
    const terminate = () => { try { process.kill(-child.pid, 'SIGTERM'); } catch { /* exited */ } };
    process.once('SIGTERM', terminate); process.once('SIGINT', terminate);
    const cleanup = () => { clearTimeout(timer); process.off('SIGTERM', terminate); process.off('SIGINT', terminate); };
    child.once('error', error => { cleanup(); reject(error); });
    child.once('exit', (code, signal) => { cleanup(); code === 0 ? resolveRun() : reject(new Error(`Build exited ${code ?? signal}`)); });
  });
}
export async function buildCandidate(options) {
  for (const name of ['template', 'work', 'snapshot', 'media-root', 'out', 'site-url']) assert.ok(options[name], `--${name} is required`);
  const source = await realpath(options.template);
  const work = resolve(options.work);
  const out = resolve(options.out);
  const snapshotPath = await realpath(options.snapshot);
  const mediaRoot = await realpath(options['media-root']);
  const url = new URL(options['site-url']);
  assert.ok(['http:', 'https:'].includes(url.protocol) && !url.username && !url.password && url.pathname === '/' && !url.search && !url.hash, 'site-url must be an HTTP(S) origin');
  assert.ok(!within(work, source) && !within(out, source), 'Output cannot contain the template source');
  assert.ok(!within(source, work) || within(resolve(source, '.work'), work), 'Work must be isolated from template source');
  assert.ok(!within(source, out) || within(resolve(source, '.work'), out), 'Output must be isolated from template source');
  assert.ok(!within(out, work) && !within(resolve(work, 'template'), out), 'Work and site output overlap');
  assert.ok(!within(work, snapshotPath) && !within(out, snapshotPath) && !within(work, mediaRoot) && !within(out, mediaRoot), 'Inputs must be outside output directories');
  await absent(work); await absent(out);
  const input = await readFile(snapshotPath);
  const snapshot = readSnapshot(snapshotPath);
  const sharp = createRequire(resolve(source, 'package.json'))('sharp');
  // Validate the complete public input before producing any template or output.
  const media = [];
  for (const image of publicImages(snapshot)) {
    const path = await realpath(resolve(mediaRoot, image.path));
    assert.ok(within(mediaRoot, path), `Media escapes root: ${image.media_id}`);
    const bytes = await readFile(path);
    assert.equal(bytes.length, image.bytes, `Media size mismatch: ${image.media_id}`);
    assert.equal(sha256(bytes), image.sha256, `Media SHA mismatch: ${image.media_id}`);
    const metadata = await sharp(bytes, { animated: true }).metadata();
    assert.deepEqual([metadata.format, metadata.width, metadata.height, metadata.pages ?? 1], [image.format, image.width, image.height, 1], `Invalid media metadata: ${image.media_id}`);
    await sharp(bytes).raw().toBuffer();
    media.push({ image, bytes });
  }
  const template = await copyTemplate(source, resolve(work, 'template'));
  const codeHash = createHash('sha256');
  for (const entry of TEMPLATE_PATHS) {
    const path = resolve(template, entry);
    const files = (await lstat(path)).isDirectory() ? await walkFiles(path) : [path];
    for (const file of files) codeHash.update(relative(template, file)).update('\0').update(await readFile(file)).update('\0');
  }
  const codeVersion = process.env.CODE_VERSION || `template_sha256_${codeHash.digest('hex')}`;
  const stagingSite = resolve(template, 'dist');
  const frozen = resolve(work, 'catalog.json');
  await writeFile(frozen, input, { flag: 'wx', mode: 0o600 });
  await mkdir(resolve(template, 'src/assets/catalog-media'), { recursive: true });
  await mkdir(resolve(template, 'public/media'), { recursive: true });
  for (const { image, bytes } of media) {
    await writeFile(resolve(template, 'src/assets/catalog-media', mediaFilename(image)), bytes);
    await writeFile(resolve(template, 'public/media', mediaFilename(image)), bytes);
  }
  const env = {
    ...process.env, CATALOG_SNAPSHOT: frozen, CATALOG_MEDIA_DIR: resolve(template, 'src/assets/catalog-media'),
    BUILD_SITE_DIR: stagingSite, SITE_URL: url.origin, ASTRO_TELEMETRY_DISABLED: '1',
  };
  await run('npm', ['run', 'build'], template, env);
  await cp(stagingSite, out, { recursive: true, errorOnExist: true, force: false });
  const audit = await auditSite(frozen, out);
  const manifest = validateContract('catalog-snapshot.schema.json#/$defs/Manifest', {
    schema_version: 1, candidate_id: snapshot.release_id, workspace_revision: snapshot.workspace_revision,
    code_version: codeVersion, snapshot_sha256: sha256(input), routes: audit.routes, resources: audit.resources,
  });
  await writeFile(resolve(work, 'manifest.json'), `${JSON.stringify(manifest, null, 2)}\n`, { flag: 'wx', mode: 0o600 });
  return { candidate_id: snapshot.release_id, snapshot_sha256: manifest.snapshot_sha256, site: out, manifest: resolve(work, 'manifest.json'), pages: audit.pages, resources: manifest.resources.length };
}
if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  const { values } = parseArgs({ options: Object.fromEntries(['template', 'work', 'snapshot', 'media-root', 'out', 'site-url'].map(name => [name, { type: 'string' }])) });
  console.log(JSON.stringify(await buildCandidate(values), null, 2));
}
