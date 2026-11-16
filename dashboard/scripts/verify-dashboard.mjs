// Vite must be running. Run: node scripts/verify-dashboard.mjs fixtures|clicks|live|down|auth
import assert from 'node:assert/strict';
import { chromium } from 'playwright';
import { mkdir } from 'node:fs/promises';
import { join } from 'node:path';
const mode = process.argv[2] ?? 'fixtures';
const browser = await chromium.launch();
const context = await browser.newContext({ viewport: { width: 390, height: 844 }, hasTouch: true });
if (process.env.VERIFY_THEME) await context.addInitScript(theme => localStorage.setItem('kyurem.theme', theme), process.env.VERIFY_THEME);
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
const taskDetail = {
  task: { ...tasks[0], span_count: 2, error_type: 'fixture_failure', error_message: 'fixture task failed' },
  spans: [{ trace_id: 'trace-0', span_id: 'span-0', name: 'echo.call', kind: 'agent', service_name: 'echo', agent: 'echo',
    caller: 'root', started_at: now, duration_ms: 1200, status_code: 'ERROR', status_message: 'fixture failure',
    attributes: { 'fixture.key': 'fixture value' } },
  { trace_id: 'trace-0', span_id: 'span-0-child', parent_span_id: 'span-0', name: 'upper.call', kind: 'agent',
    service_name: 'upper', agent: 'upper', caller: 'echo', started_at: now, duration_ms: 500, status_code: 'OK', attributes: {} }],
  root_cause: { span_id: 'span-0', span_name: 'echo.call', agent: 'echo', error_type: 'fixture_failure',
    message: 'The fixture agent returned a controlled error.', affected_span_ids: ['span-0'], confidence: 'heuristic' },
  cost: { group_by: 'agent', total_usd: 0.1, input_tokens: 10, output_tokens: 20, spans: 1, unknown_spans: 0,
    buckets: [{ key: 'echo', cost_usd: 0.1, input_tokens: 10, output_tokens: 20, spans: 1, unknown_spans: 0 }] },
  events: [{ span_id: 'span-0', time: now, name: 'fixture.error', attributes: { level: 'error', source: 'fixture' } }],
};
let errorsFail = false, metadataFail = false, errorsDelay = false, shrink = false, releaseErrors;
if (['fixtures', 'clicks'].includes(mode)) await page.route('**/api/v1/**', async route => {
  const u = new URL(route.request().url()), endpoint = u.pathname.split('/').pop();
  const json = body => route.fulfill({ json: body });
  const fail = message => route.fulfill({ status: 500, json: { error: { code: 'fixture_failure', message } } });
  if (endpoint === 'agents') return metadataFail ? fail('fixture registry unavailable') : json({ items: agents });
  if (u.pathname.startsWith('/api/v1/tasks/') && endpoint !== 'tasks') return json(taskDetail);
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
  if (endpoint === 'decisions') return json({ items: [{ timestamp: now, kind: 'auth', caller: 'echo',
    callee: agents[1].name, decision: 'deny', reason: 'fixture denied' }], total: 1 });
  if (endpoint === 'costs') return json({ group_by: 'agent', total_usd: 0.72, spans: 72, unknown_spans: 0,
    input_tokens: 120, output_tokens: 60, buckets: agents.map(a => ({ key: a.name, cost_usd: 0.12,
      input_tokens: 20, output_tokens: 10, spans: 12, unknown_spans: 0 })) });
  if (endpoint === 'settings') return json({ version: 'fixture', spec_version: '1', service_name: 'fixture-gateway', agents: 6, storage: { driver: 'memory' }, telemetry: { exporting: false, queue_depth: 0, queue_capacity: 2048, spans_ended: 72, spans_exported: 0, spans_dropped: {} } });
  if (endpoint === 'overview') {
    const buckets = Array.from({ length: 24 }, (_, i) => ({ bucket: new Date(Date.now() - (23-i)*3600000).toISOString(), succeeded: [30,24,20,18,26,45,65,48,56,70,68,82,61,73,89,94,82,68,77,90,86,72,80,96][i], failed: i % 3 + 1, unknown: 0, spans: 100, cost_usd: (i % 5 + 2)*0.16 }));
    return json({ tasks_total: 1584, tasks_succeeded: 1536, tasks_failed: 48, error_rate: 0.0303,
      p50_task_duration_ms: 180, p95_task_duration_ms: 1240, active_agents: 6, spans_total: 4620,
      total_cost_usd: 24.72, tasks_over_time: buckets, cost_over_time: buckets,
      top_agents_by_cost: agents.slice(0,5).map((a,i) => ({ key: a.name, cost_usd: [8.24,6.1,4.8,3.2,2.38][i], spans: 800-i*100, unknown_spans: 0 })),
      top_failing_agents: agents.slice(0,3).map((a,i) => ({ name: a.name, errors: 12-i*3, error_rate: 0.08-i*0.02 })), recent_failed_tasks: tasks.slice(0,3) });
  }
  return route.fulfill({ status: 404, json: { error: { message: 'fixture endpoint unavailable' } } });
});
const visible = text => page.getByText(text, { exact: true }).first().waitFor();
async function goto(path) { await page.goto('http://127.0.0.1:5173' + path, { waitUntil: 'domcontentloaded' }); await page.locator('h1').waitFor(); }
async function fits(label) {
  const sizes = await page.evaluate(() => ({ viewport: innerWidth, document: document.documentElement.scrollWidth,
    regions: [...document.querySelectorAll('.app-main,.page')].map(e => ({ client: e.clientWidth, scroll: e.scrollWidth })) }));
  if (sizes.regions.some(r => r.scroll > r.client + 1)) console.log(await page.evaluate(() => {
    const boundary = document.querySelector('.page').getBoundingClientRect().right;
    return [...document.querySelectorAll('.page *')].filter(e => !e.closest('.table-wrapper') && e.getBoundingClientRect().right > boundary + 1).map(e => ({ tag: e.tagName, class: e.className, text: e.textContent.slice(0, 100) }));
  }));
  assert(sizes.regions.length >= 2, `${label}: routed page did not render`);
  assert(sizes.document <= sizes.viewport, `${label}: ${JSON.stringify(sizes)}`);
  for (const r of sizes.regions) assert(r.scroll <= r.client + 1, `${label}: ${JSON.stringify(sizes)}`);
  console.log('PASS', label, JSON.stringify(sizes));
}
const selectEcho = () => page.locator('.react-flow__node[data-id="echo"]').click();
try {
  if (['fixtures', 'live'].includes(mode)) {
    for (const width of [320, 390, 1440]) {
      await page.setViewportSize({ width, height: 900 });
      for (const path of ['/', '/tasks', '/agents', '/policy', '/costs', '/graph']) {
        await goto(path);
        if (path === '/') await page.locator('.overview-kpi').first().waitFor();
        else if (path === '/graph') await page.locator('.react-flow__node').first().waitFor();
        else if (path === '/policy') await page.locator('.detail-grid').first().waitFor();
        else await page.locator('.data-table').first().waitFor();
        await page.waitForTimeout(300);
        await fits(`${mode} ${path} ${width}px`);
        if (process.env.VERIFY_SCREENSHOTS) {
          await mkdir(process.env.VERIFY_SCREENSHOTS, { recursive: true });
          await page.waitForTimeout(1200); // let numeric/chart entrances settle before visual QA
          await page.screenshot({ path: join(process.env.VERIFY_SCREENSHOTS, `${mode}-${path.slice(1) || 'overview'}-${width}-${process.env.VERIFY_THEME || 'light'}.png`), fullPage: false });
        }
        if (mode === 'fixtures' && path === '/agents') {
          for (const [i, tone] of ['good', 'bad', 'warn', 'neutral', 'neutral', 'neutral'].entries())
            assert(await page.getByText(statuses[i], { exact: true }).evaluate((e, t) => e.classList.contains(`badge-${t}`), tone));
        }
        if (width === 390 && path !== '/graph' && path !== '/') {
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
  if (['fixtures', 'clicks'].includes(mode)) {
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

    // Exercise the actual interactive paths in each page, not just direct URL rendering.
    const destinations = [
      ['Overview', '/'], ['Tasks', '/tasks'], ['Agent graph', '/graph'], ['Costs', '/costs'],
      ['Agents', '/agents'], ['Policy & audit', '/policy'], ['Settings', '/settings'],
    ];
    await page.setViewportSize({ width: 1440, height: 900 });
    for (const [label, path] of destinations) {
      await goto('/');
      await page.getByRole('link', { name: label, exact: true }).click();
      await page.waitForURL(`**${path}`);
      await page.locator('h1').waitFor();
    }
    console.log('PASS fixtures: all sidebar destinations navigate to their pages');

    await goto('/');
    await page.keyboard.press('Control+k');
    await page.getByRole('dialog', { name: 'Command menu' }).waitFor();
    await page.getByPlaceholder('Go to a page or paste a task id…').fill('Go to Costs');
    await page.keyboard.press('Enter');
    await page.waitForURL('**/costs');
    console.log('PASS fixtures: command menu search navigates with the keyboard');

    await goto('/');
    await page.getByRole('radio', { name: '1h', exact: true }).check();
    await page.getByRole('link', { name: 'task-0', exact: true }).click();
    await page.getByRole('heading', { name: 'task-0' }).waitFor();
    await goto('/agents');
    await page.getByRole('radio', { name: '1h', exact: true }).check();
    await goto('/graph');
    await page.getByRole('radio', { name: '1h', exact: true }).check();
    await page.getByRole('button', { name: 'Zoom in' }).click();
    await page.getByRole('button', { name: 'Zoom out' }).click();
    console.log('PASS fixtures: Overview, Agents and Graph time controls plus graph zoom');

    await goto('/tasks');
    await page.getByLabel('Task id').fill('task-0');
    await visible('task-0');
    await page.getByLabel('Clear task filter').click();
    await page.locator('tbody tr').first().click();
    await page.getByRole('dialog', { name: /Task task-0/ }).waitFor();
    await page.getByRole('button', { name: 'Close task details' }).click();
    await page.getByRole('link', { name: 'task-0', exact: true }).click();
    await page.getByRole('heading', { name: 'task-0' }).waitFor();
    await page.locator('summary').filter({ hasText: 'Task payload' }).click();
    await page.getByRole('tree', { name: 'task' }).waitFor();
    if (process.env.VERIFY_SCREENSHOTS) {
      await mkdir(process.env.VERIFY_SCREENSHOTS, { recursive: true });
      await page.screenshot({ path: join(process.env.VERIFY_SCREENSHOTS, 'task-detail-payload.png'), fullPage: false });
    }
    await page.getByRole('button', { name: 'Expand all' }).click();
    await page.getByRole('button', { name: 'Collapse all' }).waitFor();
    await page.getByLabel('Search keys and values').fill('task_id');
    await page.getByRole('button', { name: 'Next match' }).click();
    await page.getByRole('button', { name: 'Clear search' }).click();
    await page.getByRole('button', { name: 'Copy path data.task_id' }).click();
    await page.getByRole('button', { name: 'Collapse span echo.call' }).click();
    await page.getByRole('button', { name: 'Select span echo.call, affected by root cause' }).click();
    await page.getByRole('button', { name: /Select span echo\.call, echo/ }).click();
    await page.getByRole('button', { name: 'Pause stream' }).click();
    await page.getByRole('button', { name: 'Resume stream' }).click();
    await page.getByLabel('Search logs').fill('fixture');
    await page.getByRole('button', { name: 'Clear search' }).last().click();
    console.log('PASS fixtures: task row drawer, payload tree, search, disclosure and event stream controls');

    await goto('/costs');
    await page.getByLabel('Group by').selectOption('task');
    await visible('Cost per task');
    await page.getByRole('radio', { name: '1h', exact: true }).check();
    if (process.env.VERIFY_SCREENSHOTS) {
      await mkdir(process.env.VERIFY_SCREENSHOTS, { recursive: true });
      await page.screenshot({ path: join(process.env.VERIFY_SCREENSHOTS, 'costs-desktop.png'), fullPage: false });
    }
    const csv = page.waitForEvent('download');
    await page.getByRole('button', { name: 'Export CSV' }).click();
    assert.equal((await csv).suggestedFilename(), 'kyurem-costs-task.csv');
    await page.locator('[aria-label="Share of spend breakdown"] button').first().click();
    console.log('PASS fixtures: costs grouping, time range, CSV export and spend filter');

    await goto('/policy');
    await page.getByLabel('Kind').selectOption('auth');
    const reviewQueue = page.getByRole('region', { name: /Review queue/ });
    await reviewQueue.waitFor({ state: 'visible' });
    await reviewQueue.getByRole('button', { name: 'Edit' }).click();
    await page.getByLabel('Edit before approving').waitFor();
    await reviewQueue.getByRole('button', { name: 'Cancel' }).click();
    await reviewQueue.getByRole('button', { name: 'Reject' }).click();
    await visible('Queue clear');
    await page.getByRole('button', { name: 'Reload queue' }).click();
    console.log('PASS fixtures: policy decision filter and local review actions');

    await goto('/settings');
    const themeButtons = page.getByRole('button', { name: /theme.*switch to/i });
    assert.equal(await themeButtons.count(), 1, 'a single visible theme switch');
    const currentTheme = await page.locator('html').getAttribute('data-theme');
    await themeButtons.click();
    await page.waitForFunction(before => document.documentElement.dataset.theme !== before, currentTheme);
    await page.getByRole('button', { name: 'Show token' }).click();
    await page.getByRole('button', { name: 'Hide token' }).click();
    await page.locator('#api-token').fill('fixture-token');
    await page.getByRole('button', { name: 'Save token' }).click();
    await visible('Token saved.');
    await page.getByRole('button', { name: 'Clear token' }).click();
    await page.getByRole('region', { name: 'Workspace. Press question mark for keyboard shortcuts.' }).focus();
    await page.keyboard.press('?');
    await page.getByRole('dialog', { name: 'Keyboard shortcuts' }).waitFor();
    await page.keyboard.press('Escape');
    await page.getByRole('dialog', { name: 'Keyboard shortcuts' }).waitFor({ state: 'detached' });
    await page.getByRole('button', { name: 'Shortcuts' }).click();
    await page.getByRole('dialog', { name: 'Keyboard shortcuts' }).waitFor();
    await page.getByRole('button', { name: 'Close shortcuts' }).click();
    console.log('PASS fixtures: theme, token and shortcut controls on Settings');

    await page.setViewportSize({ width: 390, height: 844 });
    await goto('/');
    await page.getByRole('button', { name: 'Open navigation' }).click();
    await page.getByRole('link', { name: 'Costs', exact: true }).click();
    await page.getByRole('heading', { name: 'Costs' }).waitFor();
    if (process.env.VERIFY_SCREENSHOTS) {
      await mkdir(process.env.VERIFY_SCREENSHOTS, { recursive: true });
      await page.screenshot({ path: join(process.env.VERIFY_SCREENSHOTS, 'costs-mobile.png'), fullPage: false });
    }
    assert.equal(await page.getByRole('button', { name: 'Open navigation' }).getAttribute('aria-expanded'), 'false');
    await fits('mobile navigation click');
    console.log('PASS fixtures: mobile menu opens, navigates and closes');
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
