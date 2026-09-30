import { expect, test, type Page } from '@playwright/test';

import type { startReleaseProxy } from './fixtures/release-proxy.mjs';

let proxy: Awaited<ReturnType<typeof startReleaseProxy>> | undefined;

function runningProxy() {
  if (!proxy) throw new Error('release proxy fixture did not start');
  return proxy;
}

test.beforeAll(async () => {
  if (process.env.E2E_RELEASE_PROXY !== '1' || process.env.E2E_SKIP_WEB_SERVER !== '1' || process.env.E2E_PRODUCTION !== '1') {
    throw new Error('release proxy smoke requires explicit E2E_RELEASE_PROXY=1, E2E_SKIP_WEB_SERVER=1 and E2E_PRODUCTION=1');
  }
  // The fixture validates CADDY_BIN here, not during --list. Missing tooling is
  // a failure, never a skip or a silently substituted HTTP proxy.
  const { startReleaseProxy } = await import('./fixtures/release-proxy.mjs');
  proxy = await startReleaseProxy({ caddyBin: process.env.CADDY_BIN, frontendUrl: process.env.E2E_RELEASE_FRONTEND_URL });
});

test.afterAll(async () => {
  if (proxy) {
    await proxy.stop();
    expect(proxy.configEvidence.original_unchanged_after_stop).toBe(true);
  }
  proxy = undefined;
});

test.beforeEach(async ({ page }) => {
  await page.route('**/*', async (route) => {
    const url = new URL(route.request().url());
    const allowed = url.protocol === 'http:' && url.hostname === '127.0.0.1'
      && Number(url.port) >= 4381 && Number(url.port) <= 4389;
    if (allowed) await route.continue();
    else await route.abort('blockedbyclient');
  });
});

async function browserRest(page: Page, target: string) {
  return page.evaluate(async (url) => {
    const response = await fetch(url, {
      method: 'POST', headers: { 'Content-Type': 'application/json' }, body: '{}',
      credentials: 'omit', signal: AbortSignal.timeout(5000),
    });
    const body: unknown = await response.json();
    return { status: response.status, body };
  }, target);
}

async function browserSocket(page: Page, target: string): Promise<string> {
  return page.evaluate((url) => new Promise<string>((resolve, reject) => {
    const socket = new WebSocket(url);
    let settled = false;
    const finish = (error?: string, value?: string) => {
      if (settled) return;
      settled = true;
      clearTimeout(timeout);
      socket.close();
      if (error) reject(new Error(error));
      else resolve(value ?? '');
    };
    const timeout = setTimeout(() => finish('routing probe websocket timed out'), 5000);
    socket.onerror = () => finish('routing probe websocket failed');
    socket.onclose = () => finish('routing probe websocket closed without text');
    socket.onmessage = (event) => {
      if (typeof event.data !== 'string' || event.data.length > 128) finish('routing probe websocket returned invalid text');
      else finish(undefined, event.data);
    };
  }), target);
}

test('release proxy serves the production frontend runtime and public health', async ({ page, request }) => {
  const { origins, configEvidence } = runningProxy();
  expect(configEvidence).toMatchObject({ default_bind: '127.0.0.1', validated: true, original_unchanged: true });
  expect(configEvidence.original_sha256).toMatch(/^[a-f0-9]{64}$/);
  expect(configEvidence.adapted_sha256).not.toBe(configEvidence.original_sha256);
  const response = await page.goto(origins.public);
  expect(response?.status()).toBe(200);
  await expect(page.getByRole('link', { name: 'Войти', exact: true })).toBeVisible();
  await expect(page.getByRole('link', { name: 'Создать аккаунт', exact: true })).toBeVisible();
  const health = await request.get(`${origins.public}/health`);
  expect(health.status()).toBe(200);
  expect(health.headers()['x-content-type-options']).toBe('nosniff');
  expect(health.headers()['strict-transport-security']).toBe('max-age=63072000; includeSubDomains; preload');
  expect(health.headers()['referrer-policy']).toBe('strict-origin-when-cross-origin');
});

test('release proxy redirects the admin root to the production login UI', async ({ page, request }) => {
  const { origins } = runningProxy();
  const redirect = await request.get(origins.admin, { maxRedirects: 0 });
  expect(redirect.status()).toBe(302);
  expect(redirect.headers().location).toBe('/admin');
  await page.goto(origins.admin);
  await expect(page).toHaveURL(`${origins.admin}/admin`);
  await expect(page.getByPlaceholder('Введите пароль...')).toBeVisible();
  await expect(page.getByRole('button', { name: 'Войти' })).toBeVisible();
  expect((await request.get(`${origins.admin}/health`)).status()).toBe(200);
});

test('public Caddy origin blocks admin UI and admin API before upstream', async ({ request }) => {
  const fixture = runningProxy();
  const sentinel = '/api/v1/admin/release-smoke';
  const upstream = await request.get(`${fixture.backendUrl}${sentinel}`);
  expect(upstream.status()).toBe(200);
  expect(await upstream.json()).toEqual({ synthetic: true, probe: 'upstream-boundary-sentinel' });
  const count = fixture.count(sentinel);
  for (const route of ['/admin', '/admin/release-smoke', '/api/v1/admin', sentinel]) {
    expect((await request.get(`${fixture.origins.public}${route}`, { maxRedirects: 0 })).status()).toBe(404);
  }
  expect(fixture.count(sentinel)).toBe(count);
});

test('all configured Caddy origins block internal paths despite a positive synthetic upstream', async ({ request }) => {
  const fixture = runningProxy();
  const sentinel = '/internal/release-smoke';
  expect((await request.get(`${fixture.backendUrl}${sentinel}`)).status()).toBe(200);
  const count = fixture.count(sentinel);
  for (const origin of fixture.allOrigins) {
    for (const route of ['/internal', sentinel]) {
      expect((await request.get(`${origin}${route}`, { maxRedirects: 0 })).status()).toBe(404);
    }
  }
  expect(fixture.count(sentinel)).toBe(count);
});

for (const context of ['public', 'admin'] as const) {
  test(`${context} browser sends real REST and WebSocket probes through same-origin and direct API routes`, async ({ page }) => {
    const { origins } = runningProxy();
    const origin = origins[context];
    await page.goto(context === 'admin' ? `${origin}/admin` : origin);
    for (const destination of [origin, origins.api]) {
      const response = await browserRest(page, `${destination}/api/release-smoke`);
      expect(response).toEqual({ status: 200, body: {
        synthetic: true, probe: 'release-routing', path: '/api/release-smoke', method: 'POST',
        request_origin: origin, request_host: new URL(destination).host,
      } });
      expect(await browserSocket(page, `${destination.replace('http:', 'ws:')}/api/release-smoke/ws`)).toBe(`release-routing:${origin}`);
    }
  });
}

test('synthetic routing backend rejects a foreign Origin over actual HTTP, not application auth proof', async ({ request }) => {
  const { origins } = runningProxy();
  const blocked = await request.post(`${origins.api}/api/release-smoke`, {
    headers: { Origin: 'http://127.0.0.1:4399' }, data: {},
  });
  expect(blocked.status()).toBe(403);
  expect(blocked.headers()['access-control-allow-origin']).toBeUndefined();
  expect(await blocked.json()).toEqual({ synthetic: true, error: 'origin denied by routing fixture' });
  const allowed = await request.get(`${origins.api}/health`, { headers: { Origin: origins.public } });
  expect(allowed.status()).toBe(200);
  expect(allowed.headers()['access-control-allow-origin']).toBe(origins.public);
  expect(await allowed.json()).toEqual({ status: 'ok', synthetic: true });
});
