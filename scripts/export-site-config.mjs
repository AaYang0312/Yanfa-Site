import { readFile, mkdir, writeFile } from 'node:fs/promises';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { stripTypeScriptTypes } from 'node:module';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
// Node22 reads the existing TypeScript source directly; no second phone constant.
const source = await readFile(resolve(root, 'src/data/site.ts'), 'utf8');
const { site, telHref } = await import(`data:text/javascript;base64,${Buffer.from(stripTypeScriptTypes(source)).toString('base64')}`);
const config = { schema_version: 1, name: site.name, phone: site.phone, contact_href: telHref() };
if (!/^tel:[+0-9]+$/.test(config.contact_href)) throw new Error('Invalid site phone configuration');
await mkdir(resolve(root, 'src/generated'), { recursive: true });
await writeFile(resolve(root, 'src/generated/site-config.json'), JSON.stringify(config, null, 2) + '\n');
console.log('Exported site configuration from src/data/site.ts.');
