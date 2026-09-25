import { expect, test } from '@playwright/test';

test('csp report endpoint accepts report-only browser reports', async ({ request }) => {
  const response = await request.post('/csp-report', {
    data: {
      'csp-report': {
        'document-uri': 'http://127.0.0.1:3000/',
        'violated-directive': 'connect-src',
      },
    },
  });

  expect(response.status()).toBe(204);
  expect(await response.text()).toBe('');
});

test('csp report endpoint accepts reports up to 64 KiB', async ({ request }) => {
  const payload = 'a'.repeat(64 * 1024);

  const response = await request.post('/csp-report', {
    data: payload,
    headers: {
      'content-type': 'application/csp-report',
    },
  });

  expect(response.status()).toBe(204);
});

test('csp report endpoint rejects oversized reports', async ({ request }) => {
  const payload = 'a'.repeat(64 * 1024 + 1);

  const response = await request.post('/csp-report', {
    data: payload,
    headers: {
      'content-type': 'application/csp-report',
    },
  });

  expect(response.status()).toBe(413);
});

test('csp header keeps same-origin API and realtime enabled', async ({ request }) => {
  const response = await request.get('/');
  const headers = response.headers();
  const production = process.env.E2E_PRODUCTION === '1';
  const csp = headers[production ? 'content-security-policy' : 'content-security-policy-report-only'];

  expect(response.ok()).toBe(true);
  if (production) {
    expect(headers['content-security-policy-report-only']).toBeUndefined();
    expect(csp).toContain('upgrade-insecure-requests');
  }
  expect(csp).toContain("default-src 'self'");
  expect(csp).toContain("connect-src 'self'");
  expect(csp).toContain('report-uri /csp-report');
});

test('public page navigation returns the configured security headers', async ({ page }) => {
  const response = await page.goto('/');
  expect(response).not.toBeNull();
  if (!response) throw new Error('public page navigation did not return a response');
  expect(response.status()).toBe(200);
  const headers = response.headers();
  expect(headers['content-type']).toContain('text/html');
  expect(headers['x-frame-options']).toBe('DENY');
  expect(headers['x-content-type-options']).toBe('nosniff');
  expect(headers['strict-transport-security']).toBe('max-age=63072000; includeSubDomains; preload');
  expect(headers['referrer-policy']).toBe('strict-origin-when-cross-origin');
  expect(headers['permissions-policy']).toBe('camera=(), microphone=(), geolocation=(), interest-cohort=()');
  expect(headers['cross-origin-opener-policy']).toBe('same-origin');
  if (process.env.E2E_PRODUCTION === '1') {
    expect(headers['content-security-policy']).toContain("default-src 'self'");
    expect(headers['content-security-policy-report-only']).toBeUndefined();
  } else {
    expect(headers['content-security-policy-report-only']).toContain("default-src 'self'");
  }
});
