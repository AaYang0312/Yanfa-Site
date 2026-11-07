import assert from 'node:assert/strict';
import { cp, lstat, mkdir, readdir, readFile, readlink, realpath, unlink, writeFile } from 'node:fs/promises';
import { resolve, join, relative, sep } from 'node:path';
import { parseArgs } from 'node:util';
import { copyTemplate } from './build-candidate.mjs';

const { values } = parseArgs({ options: { root: { type: 'string' }, out: { type: 'string' } } });
assert.ok(values.root && values.out, '--root and --out required');
const root = await realpath(values.root), out = resolve(values.out);
assert.equal(process.versions.node, '22.23.4', 'Runtime packaging requires locked Node');
assert.ok(out !== root && !root.startsWith(out + sep), 'Output cannot contain source');
try { await lstat(out); throw new Error('Output must not exist: ' + out); } catch (error) { if (error.code !== 'ENOENT') throw error; }
const inside = (parent, path) => path.startsWith(parent + sep);
async function checkedCopy(source, target, allowLinks = false) {
 await cp(source, target, { recursive: true, verbatimSymlinks: true, filter: async path => {
  if ((await lstat(path)).isSymbolicLink()) {
   assert.ok(allowLinks, 'Unexpected symlink: ' + path);
   const destination = await realpath(path);
   assert.ok(inside(source, destination), 'Dependency symlink escapes installed tree: ' + path);
   assert.ok(!(await readlink(path)).startsWith('/'), 'Dependency link must be relocatable: ' + path);
  }
  return true;
 } });
}
await mkdir(out, { recursive: true });
const template = join(out, 'template');
await copyTemplate(root, template);
// The builder normally links source dependencies. Deployment owns a relocatable
// physical tree so no worktree or build-stage paths survive in the image.
await unlink(join(template, 'node_modules'));
await checkedCopy(join(root, 'node_modules'), join(template, 'node_modules'), true);
await checkedCopy(join(root, 'scripts/build-candidate.mjs'), join(template, 'scripts/build-candidate.mjs'));
await checkedCopy(join(root, 'admin/dist'), join(out, 'admin'));
assert.ok((await readdir(join(out, 'admin'))).includes('index.html'), 'Build independent admin first');
const site = await readFile(join(root, 'src/generated/site-config.json'));
JSON.parse(site);
await writeFile(join(out, 'site-config.json'), site, { flag: 'wx' });
await checkedCopy(join(root, 'licenses'), join(out, 'licenses'));
const metadata = { schema_version: 1, node: '22.23.4', npm: '10.9.9', template: 'template', admin: 'admin', site_config: 'site-config.json', dependencies: 'template/node_modules', build_entry: 'template/scripts/build-candidate.mjs' };
await writeFile(join(out, 'runtime.json'), JSON.stringify(metadata, null, 2) + '\n', { flag: 'wx' });
console.log(JSON.stringify({ ...metadata, out, template_paths: relative(out, template) }, null, 2));
