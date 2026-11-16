// Vite must be running. Run: node scripts/verify-dashboard.mjs fixtures|live|down|auth
import assert from 'node:assert/strict';
import { chromium } from 'playwright';
import { mkdir } from 'node:fs/promises';
import { join } from 'node:path';
const mode = process.argv[2] ?? 'fixtures';
const browser = await chromium.launch();
const context = await browser.newContext({ viewport: { width: 390, height: 844 }, hasTouch: true });
const page = await context.newPage();
const runtimeErrors = [];
page.on('pageerror', e => runtimeErrors.push(e.message));
const now = new Date().toISOString();
const statuses = ['verified', 'unverified', 'unsigned', 'unchecked', 'unknown', 'future-status'];
const agents = statuses.map((verification, i) => ({ name: i ? `agent-${i}-long-responsive-name` : 'echo',
  aliases: ['greeter'], tags: [], upstreams: ['http://127.0.0.1:9010'], card_status: 'ok', verification,
  verification_mode: 'warn', skills: [], card_version: '1', spans: 12, errors: 1, error_rate: 0.08,
  p95_ms: 22, cost_usd: 0.12, last_seen: now }));
const tasks = Array.from({ length: 51 }, (_, i) => ({ task_id: `task-${i}`, trace_id: `trace-${i}`, status: 'failed',
  started_at: now, duration_ms: 1200, agents: ['echo'], root_caller: 'echo', cost_usd: 0.1,
  tokens_known: true, span_count: 1, input_tokens: 10, output_tokens: 20 }));
let errorsFail = false, metadataFail = false, errorsDelay = false, shrink = false, releaseErrors;
if (mode === 'fixtures') await page.route('**/api/v1/**', async route => {
  const u = new URL(route.request().url()), endpoint = u.pathname.split('/').pop();
  const json = body => route.fulfill({ json: body });
  const fail = message => route.fulfill({ status: 500, json: { error: { code: 'fixture_failure', message } } });
  if (endpoint === 'agents') return metadataFail ? fail('fixture registry unavailable') : json({ items: agents });
  if (endpoint === 'graph') return json({ nodes: agents.map(a => ({ ...a, calls: 12, p50_ms: 10 })),
    edges: [{ from: 'echo', to: agents[1].name, calls: 12, errors: 1, error_rate: 0.08, p95_ms: 22, p50_ms: 10, cost_usd: 0.1 }] });
  if (endpoint === 'tasks') {
    if (u.searchParams.has('agent')) {
      if (errorsDelay) await new Promise(resolve => { releaseErrors = resolve; });
      return errorsFail ? fail('fixture failed-task lookup unavailable') : json({ items: [], total: 0, limit: 10, offset: 0 });
    }
    const filtered = shrink || u.searchParams.get('from') === '15m' ? tasks.slice(0, 1) : tasks;
    const offset = Number(u.searchParams.get('offset') ?? 0);
    return json({ items: filtered.slice(offset, offset + 50), total: filtered.length, limit: 50, offset });
  }
  if (endpoint === 'policy') return json({ mode: 'warn', default: 'allow', callers: agents.map(a => a.name),
    callees: agents.map(a => a.name), rules: [{ name: 'example-rule', caller: 'echo', callee: '*', skills: ['echo'], effect: 'deny' }],
    matrix: agents.flatMap(a => agents.map(b => ({ caller: a.name, callee: b.name, decision: 'allow' }))) });
  if (endpoint === 'decisions') return json({ items: [{ timestamp: now, kind: 'policy', caller: 'echo',
    callee: agents[1].name, decision: 'deny', reason: 'fixture denied' }], total: 1 });
  if (endpoint === 'costs') return json({ group_by: 'agent', total_usd: 0.72, spans: 72, unknown_spans: 0,
    input_tokens: 120, output_tokens: 60, buckets: agents.map(a => ({ key: a.name, cost_usd: 0.12,
      input_tokens: 20, output_tokens: 10, spans: 12, unknown_spans: 0 })) });
  if (endpoint === 'settings') return json({ version: 'fixture', spec_version: '1', service_name: 'fixture-gateway', agents: 6, storage: { driver: 'memory' }, telemetry: { exporting: false, queue_depth: 0, queue_capacity: 2048, spans_ended: 72, spans_exported: 0, spans_dropped: {} } });
  if (endpoint === 'overview') return json({ tasks_total: 51, tasks_succeeded: 0, tasks_failed: 51, error_rate: 1,
    active_agents: 6, spans_total: 51, total_cost_usd: 5.1, tasks_over_time: [], cost_over_time: [],
    top_agents_by_cost: [], top_failing_agents: [], recent_failed_tasks: tasks.slice(0, 3) });
  return route.fulfill({ status: 404, json: { error: { message: 'fixture endpoint unavailable' } } });
});
const visible = text => page.getByText(text, { exact: true }).first().waitFor();
async function goto(path) { await page.goto('http://127.0.0.1:5173' + path); await page.locator('h1').waitFor(); }
async function fits(label) {
  const sizes = await page.evaluate(() => ({ viewport: innerWidth, document: document.documentElement.scrollWidth,
    regions: [...document.querySelectorAll('.app-main,.page')].map(e => ({ client: e.clientWidth, scroll: e.scrollWidth })) }));
  if (sizes.regions.some(r => r.scroll > r.client + 1)) console.log(await page.evaluate(() => {
    const boundary = document.querySelector('.page').getBoundingClientRect().right;
    return [...document.querySelectorAll('.page *')].filter(e => !e.closest('.table-wrapper') && e.getBoundingClientRect().right > boundary + 1).map(e => ({ tag: e.tagName, class: e.className, text: e.textContent.slice(0, 100) }));
  }));
  assert(sizes.document <= sizes.viewport, `${label}: ${JSON.stringify(sizes)}`);
  for (const r of sizes.regions) assert(r.scroll <= r.client + 1, `${label}: ${JSON.stringify(sizes)}`);
  console.log('PASS', label, JSON.stringify(sizes));
}
const selectEcho = () => page.locator('.react-flow__node[data-id="echo"]').click();
try {
  if (['fixtures', 'live'].includes(mode)) {
    for (const width of [320, 390, 1440]) {
      await page.setViewportSize({ width, height: 900 });
      for (const path of ['/tasks', '/agents', '/policy', '/costs', '/graph']) {
        await goto(path);
        if (path === '/graph') await page.locator('.react-flow__node').first().waitFor();
        else if (path === '/policy') await page.locator('.detail-grid').first().waitFor();
        else await page.locator('.data-table').first().waitFor();
        await page.waitForTimeout(300);
        await fits(`${mode} ${path} ${width}px`);
        if (process.env.VERIFY_SCREENSHOTS && path === '/agents') {
          await mkdir(process.env.VERIFY_SCREENSHOTS, { recursive: true });
          await page.screenshot({ path: join(process.env.VERIFY_SCREENSHOTS, `${mode}-agents-${width}.png`), fullPage: true });
        }
        if (mode === 'fixtures' && path === '/agents') {
          for (const [i, tone] of ['good', 'bad', 'warn', 'neutral', 'neutral', 'neutral'].entries())
            assert(await page.getByText(statuses[i], { exact: true }).evaluate((e, t) => e.classList.contains(`badge-${t}`), tone));
        }
        if (width === 390 && path !== '/graph') {
          const table = page.locator('.table-wrapper').filter({ has: page.locator('table') }).last();
          const d = await table.evaluate(e => ({ client: e.clientWidth, scroll: e.scrollWidth, tabindex: e.tabIndex, overflow: getComputedStyle(e).overflowX }));
          assert.equal(d.tabindex, 0); assert.equal(d.overflow, 'auto');
          if (d.scroll > d.client) {
            await table.focus(); await page.keyboard.press('ArrowRight'); await page.waitForTimeout(250);
            assert(await table.evaluate(e => e.scrollLeft > 0), `${path}: keyboard scrolling failed`);
            await table.evaluate(e => { e.scrollLeft = 0; });
            await table.scrollIntoViewIfNeeded();
            const box = await table.boundingBox();
            const cdp = await context.newCDPSession(page);
            const x = box.x + box.width - 15, y = box.y + 25;
            await cdp.send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: [{ x, y }] });
            for (let step = 1; step <= 6; step++) {
              await cdp.send('Input.dispatchTouchEvent', { type: 'touchMove', touchPoints: [{ x: x - step * 20, y }] });
              await page.waitForTimeout(25);
            }
            await cdp.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] });
            await page.waitForTimeout(200);
            assert(await table.evaluate(e => e.scrollLeft > 0), `${path}: touch scrolling failed`);
            await cdp.detach();
          }
        }
      }
    }
  }
  if (mode === 'fixtures') {
    await page.setViewportSize({ width: 390, height: 844 });
    await goto('/tasks'); await visible('Page 1 of 2');
    await page.getByRole('button', { name: 'Next', exact: true }).click(); await visible('Page 2 of 2');
    await page.getByRole('radio', { name: '15m', exact: true }).check(); await visible('1 task · page 1 of 1');
    assert(await page.getByRole('link', { name: 'task-0', exact: true }).isVisible());
    await page.getByRole('radio', { name: '24h', exact: true }).check(); await visible('Page 1 of 2');
    await page.getByRole('button', { name: 'Next', exact: true }).click(); await visible('Page 2 of 2');
    shrink = true; await visible('1 task · page 1 of 1');
    console.log('PASS fixtures: narrower range and live shrinking total recover page 1');
    errorsDelay = true; await goto('/graph'); await selectEcho(); await visible('Loading errors…');
    errorsDelay = false; releaseErrors(); await visible('No recent errors');
    errorsFail = true; await page.reload(); await selectEcho(); await visible('Could not load recent errors');
    assert.equal(await page.getByText('No recent errors', { exact: true }).count(), 0);
    await fits('failed recent errors 390px');
    errorsFail = false; await page.getByRole('button', { name: 'Try again', exact: true }).click(); await visible('No recent errors');
    metadataFail = true; await page.reload(); await selectEcho(); await visible('Could not load agent metadata');
    assert(await page.locator('.react-flow__node').count() > 0);
    metadataFail = false; await page.getByRole('button', { name: 'Try again', exact: true }).click(); await visible('verification: verified');
    console.log('PASS fixtures: graph loading, empty, HTTP 500, metadata failure and retry recovery');
  }
  if (['down', 'auth'].includes(mode)) {
    for (const path of ['/', '/tasks', '/agents', '/policy', '/costs', '/graph']) {
      await goto(path); await page.getByRole('alert').first().waitFor();
      const alerts = await page.getByRole('alert').allTextContents();
      if (mode === 'down') assert(alerts.some(t => t.includes('KYUREM_API_ORIGIN')), path);
      else { assert(alerts.some(t => t.includes('HTTP 401') && t.includes('bearer token')), path);
        assert(!alerts.some(t => t.includes("Can't reach the gateway")), path); }
      await fits(`${mode} ${path} 390px`);
    }
    await page.getByRole('link', { name: /Settings · API/ }).first().click();
    await page.locator('#api-token').waitFor();
    console.log(`PASS ${mode}: Settings token form remains accessible`);
    if (mode === 'auth' && process.env.VERIFY_API_TOKEN) {
      await page.locator('#api-token').fill(process.env.VERIFY_API_TOKEN);
      await page.getByRole('button', { name: 'Save token', exact: true }).click();
      await visible('gateway reachable');
      await goto('/agents'); await page.locator('.data-table').waitFor();
      console.log('PASS auth: saving a valid token recovers authenticated data');
    }
  }
  assert.deepEqual(runtimeErrors, [], 'browser runtime errors');
} finally { await browser.close(); }
