import os from 'node:os';

import { defineConfig, devices } from '@playwright/test';

const port = process.env.E2E_FRONTEND_PORT || '3000';
const baseURL = process.env.E2E_FRONTEND_URL || `http://127.0.0.1:${port}`;
const backendURL = process.env.E2E_BACKEND_URL || 'http://127.0.0.1:8080';
const defaultWorkerCount = Math.max(1, Math.min(4, os.availableParallelism?.() ?? os.cpus().length));
const workerCount = Number(process.env.E2E_WORKERS || defaultWorkerCount);

export default defineConfig({
  testDir: './e2e',
  testIgnore: process.env.E2E_FULL_STACK === '1' ? [] : ['**/full-stack-local.spec.ts'],
  timeout: 60_000,
  expect: {
    timeout: 10_000,
  },
  fullyParallel: false,
  workers: Number.isFinite(workerCount) && workerCount > 0 ? workerCount : 1,
  reporter: process.env.CI ? 'github' : 'list',
  use: {
    baseURL,
    trace: 'retain-on-failure',
  },
  webServer: process.env.E2E_SKIP_WEB_SERVER === '1'
    ? undefined
    : {
        command: `npm run dev -- --hostname 127.0.0.1 --port ${port}`,
        url: baseURL,
        reuseExistingServer: !process.env.CI,
        timeout: 120_000,
        env: {
          ...process.env,
          BACKEND_URL: backendURL,
          NEXT_PUBLIC_API_URL: process.env.NEXT_PUBLIC_API_URL || '',
          NEXT_PUBLIC_ADMIN_API_URL: process.env.NEXT_PUBLIC_ADMIN_API_URL || '',
          NEXT_PUBLIC_WS_URL: process.env.NEXT_PUBLIC_WS_URL || '',
        },
      },
  projects: [
    {
      name: 'chromium',
      use: { ...devices['Desktop Chrome'] },
    },
  ],
});
