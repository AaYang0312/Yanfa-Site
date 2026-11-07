import test from 'node:test';
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { existsSync } from 'node:fs';
import { chmod, mkdir, mkdtemp, readFile, readdir, rm, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';

const script = fileURLToPath(new URL('../deploy/backup-offline.sh', import.meta.url));

async function fixture(t) {
  const directory = await mkdtemp(join(tmpdir(), 'backup offline test-'));
  t.after(() => rm(directory, { recursive: true, force: true }));
  const data = join(directory, 'data directory');
  const output = join(directory, 'backup output');
  const bin = join(directory, 'probe bin');
  const control = join(directory, 'control logs');
  const dockerCalls = join(directory, 'docker calls');
  const compose = join(directory, 'compose.yml');
  await Promise.all([data, output, bin].map(path => mkdir(path)));
  await writeFile(compose, `services:
  yuanfa:
    image: backup-preflight-test:never-run
    volumes:
      - type: bind
        source: ${JSON.stringify(data)}
        target: /data
`);
  const existing = join(output, '.sealed backup');
  const contents = Buffer.from('existing backup bytes\0\xff\n');
  await writeFile(existing, contents);
  const docker = join(bin, 'docker');
  await writeFile(docker, '#!/bin/sh\nprintf "unexpected Docker call\\n" >> "$DOCKER_PROBE_LOG"\nexit 97\n');
  await chmod(docker, 0o700);
  return { data, output, bin, control, dockerCalls, compose, existing, contents };
}

function runBackup(context) {
  return spawnSync('bash', [script,
    '--compose', context.compose,
    '--data', context.data,
    '--output', context.output,
    '--health-url', 'http://127.0.0.1:1',
  ], {
    encoding: 'utf8',
    timeout: 10000,
    env: {
      ...process.env,
      PATH: `${context.bin}:${process.env.PATH}`,
      BACKUP_CONTROL_DIR: context.control,
      DOCKER_PROBE_LOG: context.dockerCalls,
    },
  });
}

async function assertSafeRefusal(result, context) {
  assert.equal(result.error, undefined);
  assert.equal(result.signal, null);
  assert.notEqual(result.status, 0);
  assert.deepEqual(await readFile(context.existing), context.contents);
  assert.deepEqual(await readdir(context.output), ['.sealed backup']);
  assert.deepEqual({
    dockerInvoked: existsSync(context.dockerCalls),
    controlCreated: existsSync(context.control),
  }, { dockerInvoked: false, controlCreated: false });
}

test('failed output enumeration with empty stdout refuses before Docker or control logs', async t => {
  const context = await fixture(t);
  const find = join(context.bin, 'find');
  await writeFile(find, '#!/bin/sh\nprintf "injected find enumeration failure\\n" >&2\nexit 41\n');
  await chmod(find, 0o700);
  await assertSafeRefusal(runBackup(context), context);
});

test('real find refuses a nonempty output directory and preserves its contents', async t => {
  const context = await fixture(t);
  await assertSafeRefusal(runBackup(context), context);
});
