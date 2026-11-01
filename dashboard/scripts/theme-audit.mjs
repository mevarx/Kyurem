/**
 * Theme audit: loads every page in both themes at desktop + mobile widths,
 * runs axe-core (WCAG 2.1 AA), and screenshots each combination.
 *
 * Usage: node scripts/theme-audit.mjs [baseUrl]
 */
import { chromium } from "playwright";
import { createRequire } from "node:module";
import { mkdir } from "node:fs/promises";

const require = createRequire(import.meta.url);
const axePath = require.resolve("axe-core/axe.min.js");

const BASE = process.argv[2] ?? "http://localhost:5173";
const OUT = "theme-audit";

const PAGES = [
  ["overview", "/"],
  ["tasks", "/tasks"],
  ["task-detail", "/tasks/task-9f31c4"],
  ["graph", "/graph"],
  ["costs", "/costs"],
  ["agents", "/agents"],
  ["policy", "/policy"],
  ["settings", "/settings"],
];

const VIEWPORTS = [
  ["desktop", { width: 1440, height: 900 }],
  ["mobile", { width: 390, height: 844 }],
];

async function setTheme(page, theme) {
  await page.evaluate((t) => {
    localStorage.setItem("kyurem.theme", t);
  }, theme);
  await page.reload({ waitUntil: "networkidle" });
  await page.waitForTimeout(400);
}

async function runAxe(page) {
  await page.addScriptTag({ path: axePath });
  return page.evaluate(async () => {
    const result = await window.axe.run(document, {
      runOnly: { type: "tag", values: ["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"] },
    });
    return result.violations.map((v) => ({
      id: v.id,
      impact: v.impact,
      help: v.help,
      nodes: v.nodes.slice(0, 4).map((n) => n.target.join(" ")),
    }));
  });
}

const browser = await chromium.launch();
await mkdir(OUT, { recursive: true });

let totalViolations = 0;
const report = [];

for (const [vpName, viewport] of VIEWPORTS) {
  const context = await browser.newContext({ viewport, deviceScaleFactor: 1 });
  const page = await context.newPage();
  const consoleErrors = [];
  page.on("console", (m) => {
    if (m.type() === "error") consoleErrors.push(m.text());
  });
  page.on("pageerror", (e) => consoleErrors.push(`pageerror: ${e.message}`));

  for (const theme of ["dark", "light"]) {
    for (const [name, path] of PAGES) {
      await page.goto(`${BASE}${path}`, { waitUntil: "networkidle" });
      await setTheme(page, theme);
      await page.waitForTimeout(300);

      const violations = await runAxe(page);
      totalViolations += violations.length;
      if (violations.length > 0) {
        report.push({ viewport: vpName, theme, page: name, violations });
      }

      if (vpName === "desktop" || name === "overview" || name === "tasks") {
        await page.screenshot({ path: `${OUT}/${theme}-${vpName}-${name}.png`, fullPage: false });
      }
    }
  }

  if (consoleErrors.length) {
    console.log(`\n[console errors @ ${vpName}]`);
    for (const e of consoleErrors.slice(0, 8)) console.log("  ", e);
  }
  await context.close();
}

await browser.close();

console.log(`\naxe violations: ${totalViolations}`);
for (const entry of report) {
  console.log(`\n${entry.viewport} / ${entry.theme} / ${entry.page}: ${entry.violations.length}`);
  for (const v of entry.violations) {
    console.log(`  - ${v.id} (${v.impact}): ${v.help}`);
    for (const n of v.nodes) console.log(`      ${n}`);
  }
}
console.log(`\nscreenshots in ${OUT}/`);
