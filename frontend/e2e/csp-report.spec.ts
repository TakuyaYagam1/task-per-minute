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

test('csp report-only header keeps same-origin api and ws reports enabled', async ({ request }) => {
  const response = await request.get('/');
  const csp = response.headers()['content-security-policy-report-only'];

  expect(response.ok()).toBe(true);
  expect(csp).toContain("default-src 'self'");
  expect(csp).toContain("connect-src 'self'");
  expect(csp).toContain('report-uri /csp-report');
});
