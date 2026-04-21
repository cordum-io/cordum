// JUnit XML emitter for the TypeScript harness. Same schema as the
// Go + Python harnesses so step-7's aggregator consumes any of them.

import { promises as fs } from 'node:fs';
import path from 'node:path';

export class TestCase {
  constructor(name, className, timeSec) {
    this.name = name;
    this.className = className;
    this.timeSec = timeSec;
    this.failureMessage = null;
    this.failureBody = '';
  }
}

export class TestSuite {
  constructor(name) {
    this.name = name;
    this.cases = [];
  }

  get tests() { return this.cases.length; }

  get failures() {
    return this.cases.filter((c) => c.failureMessage).length;
  }

  toXML() {
    const esc = (s) =>
      String(s)
        .replaceAll('&', '&amp;')
        .replaceAll('<', '&lt;')
        .replaceAll('>', '&gt;')
        .replaceAll('"', '&quot;')
        .replaceAll("'", '&apos;');
    const lines = [
      '<?xml version="1.0" encoding="UTF-8"?>',
      `<testsuite name="${esc(this.name)}" tests="${this.tests}" failures="${this.failures}">`,
    ];
    for (const c of this.cases) {
      const head = `  <testcase name="${esc(c.name)}" classname="${esc(c.className)}" time="${c.timeSec.toFixed(7)}"`;
      if (c.failureMessage) {
        lines.push(`${head}>`);
        lines.push(`    <failure message="${esc(c.failureMessage)}" type="AssertionError">${esc(c.failureBody)}</failure>`);
        lines.push('  </testcase>');
      } else {
        lines.push(`${head}></testcase>`);
      }
    }
    lines.push('</testsuite>');
    return lines.join('\n') + '\n';
  }
}

export async function writeReport(outPath, suite) {
  const dir = path.dirname(outPath);
  await fs.mkdir(dir, { recursive: true });
  await fs.writeFile(outPath, suite.toXML(), 'utf8');
}
