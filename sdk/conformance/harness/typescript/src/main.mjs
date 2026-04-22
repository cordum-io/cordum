#!/usr/bin/env node
// Entrypoint for the TypeScript conformance harness.
//
//   node src/main.mjs \
//     --sim-bin ../../simulator/bin/cordum-gateway-sim \
//     --fixtures ../../fixtures \
//     --report ../../reports/typescript.xml

import { spawn } from 'node:child_process';
import { promises as fs } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

import { Driver } from './driver.mjs';
import { TestCase, TestSuite, writeReport } from './report.mjs';

const DEFAULT_API_KEY = 'conformance-api-key';
const DEFAULT_TENANT = 'default';

const ARGS = parseArgs(process.argv.slice(2));
const REPO_DIR = path.dirname(fileURLToPath(import.meta.url));

async function main() {
  let sim;
  try {
    sim = await startSimulator(ARGS['sim-bin']);
  } catch (err) {
    console.error(`harness-typescript: failed to start simulator: ${err.message}`);
    process.exit(2);
  }
  try {
    await waitForReady(sim.url, 5_000);
  } catch (err) {
    console.error(`harness-typescript: simulator never became ready: ${err.message}`);
    sim.cancel();
    process.exit(2);
  }

  const driver = new Driver({
    baseUrl: sim.url,
    apiKey: DEFAULT_API_KEY,
    tenant: DEFAULT_TENANT,
  });

  const suite = new TestSuite('conformance-typescript');
  let pass = 0;
  let fail = 0;
  const fixtures = await walkFixtures(ARGS.fixtures);
  for (const fxPath of fixtures) {
    const raw = await fs.readFile(fxPath, 'utf8');
    const fx = JSON.parse(raw);
    const start = performance.now();
    const tc = new TestCase(fx.name, fx.name, 0);
    try {
      await driver.runFixture(fx);
      pass += 1;
      console.error(`PASS ${fx.name.padEnd(50)} (${((performance.now() - start) / 1000).toFixed(3)}s)`);
    } catch (err) {
      tc.failureMessage = err.message;
      fail += 1;
      console.error(`FAIL ${fx.name.padEnd(50)} ${err.message}`);
    }
    tc.timeSec = (performance.now() - start) / 1000;
    suite.cases.push(tc);
  }

  await writeReport(ARGS.report, suite);
  console.error(`\nharness-typescript: ${pass} pass, ${fail} fail — report=${ARGS.report}`);
  sim.cancel();
  process.exit(fail === 0 ? 0 : 1);
}

function parseArgs(argv) {
  const out = {
    fixtures: '../../fixtures',
    'sim-bin': '../../simulator/bin/cordum-gateway-sim',
    report: '../../reports/typescript.xml',
  };
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i];
    if (a.startsWith('--')) {
      const key = a.slice(2);
      const value = argv[i + 1];
      if (value && !value.startsWith('--')) {
        out[key] = value;
        i += 1;
      } else {
        out[key] = 'true';
      }
    }
  }
  return out;
}

async function startSimulator(bin) {
  await fs.access(bin).catch(() => {
    throw new Error(`simulator binary not found at ${bin}`);
  });
  const child = spawn(bin, [], {
    stdio: ['ignore', 'pipe', 'inherit'],
  });
  const url = await new Promise((resolve, reject) => {
    let buffer = '';
    const onData = (chunk) => {
      buffer += chunk.toString();
      const nl = buffer.indexOf('\n');
      if (nl >= 0) {
        child.stdout.off('data', onData);
        const line = buffer.slice(0, nl).trim();
        // Drain remaining stdout so the child doesn't block on a
        // full pipe buffer.
        child.stdout.resume();
        child.stdout.on('data', () => {});
        if (!line.startsWith('http://') && !line.startsWith('https://')) {
          reject(new Error(`simulator did not print URL; got: ${line}`));
          return;
        }
        resolve(line);
      }
    };
    child.stdout.on('data', onData);
    child.on('error', reject);
    child.on('exit', (code) => {
      if (code !== 0 && code !== null) {
        reject(new Error(`simulator exited with code ${code}`));
      }
    });
  });
  const cancel = () => {
    try { child.kill(); } catch { /* ignore */ }
  };
  return { child, url, cancel };
}

async function waitForReady(url, timeoutMs) {
  const deadline = Date.now() + timeoutMs;
  let lastErr = '';
  while (Date.now() < deadline) {
    try {
      const resp = await fetch(`${url}/healthz`);
      if (resp.ok) return;
    } catch (err) {
      lastErr = err.message;
    }
    await new Promise((r) => setTimeout(r, 50));
  }
  throw new Error(lastErr || 'timeout');
}

async function walkFixtures(root) {
  const out = [];
  async function walk(dir) {
    const entries = await fs.readdir(dir, { withFileTypes: true });
    entries.sort((a, b) => a.name.localeCompare(b.name));
    for (const e of entries) {
      const p = path.join(dir, e.name);
      if (e.isDirectory()) {
        await walk(p);
      } else if (e.isFile() && e.name.endsWith('.json')) {
        out.push(p);
      }
    }
  }
  await walk(root);
  return out;
}

// keep fileURLToPath usage so the bundler doesn't tree-shake it
void REPO_DIR;

main().catch((err) => {
  console.error(`harness-typescript: unhandled error: ${err.stack || err.message}`);
  process.exit(2);
});
