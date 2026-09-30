import os from 'node:os';
import path from 'node:path';

import { defineConfig, devices } from '@playwright/test';

import releaseTools from '../security/tools/release-tools.lock.json';

const port = process.env.E2E_FRONTEND_PORT || '3101';
const baseURL = process.env.E2E_FRONTEND_URL || `http://127.0.0.1:${port}`;
const accountFlowMode = process.env.E2E_ACCOUNT_FULL_STACK === '1';
const accountBackendPort = process.env.E2E_ACCOUNT_BACKEND_PORT || '4319';
const accountBackendURL = process.env.E2E_ACCOUNT_BACKEND_URL || `http://127.0.0.1:${accountBackendPort}`;
const backendURL = accountFlowMode
  ? accountBackendURL
  : process.env.E2E_BACKEND_URL || 'http://127.0.0.1:8080';
const defaultWorkerCount = Math.max(1, Math.min(4, os.availableParallelism?.() ?? os.cpus().length));
const workerCount = Number(process.env.E2E_WORKERS || defaultWorkerCount);
const reuseExistingServer = process.env.E2E_REUSE_EXISTING_SERVER === '1';
const fullStackMode = process.env.E2E_FULL_STACK === '1';
const releaseProxyMode = process.env.E2E_RELEASE_PROXY === '1';
const frontendRoot = __dirname;
const backendRoot = path.resolve(frontendRoot, '..', 'backend');
const chromiumTool = releaseTools.tools.find((tool) => tool.name === 'chromium');
const chromiumProvisioning = chromiumTool?.provisioning;

if (accountFlowMode && (fullStackMode || releaseProxyMode)) {
  throw new Error('Account full-stack mode cannot be combined with another dedicated E2E mode');
}

if (accountFlowMode) {
  const backendOrigin = new URL(accountBackendURL);
  if (
    backendOrigin.protocol !== 'http:'
    || backendOrigin.hostname !== '127.0.0.1'
    || backendOrigin.port !== accountBackendPort
    || backendOrigin.pathname !== '/'
    || backendOrigin.search !== ''
    || backendOrigin.hash !== ''
  ) {
    throw new Error('Account full-stack mode requires a loopback HTTP backend URL');
  }
}

if (
  chromiumProvisioning?.kind !== 'nix_store'
  || typeof chromiumProvisioning.immutable_root !== 'string'
  || typeof chromiumProvisioning.relative_path !== 'string'
) {
  throw new Error('The verified Chromium runtime is missing from the release tool manifest');
}

const chromiumExecutable = path.join(
  chromiumProvisioning.immutable_root,
  chromiumProvisioning.relative_path,
);

export default defineConfig({
  testDir: './e2e',
  testMatch: accountFlowMode
    ? '**/account-full-stack.spec.ts'
    : fullStackMode
      ? '**/full-stack-local.spec.ts'
    : releaseProxyMode
      ? '**/release-proxy.spec.ts'
      : '**/*.spec.ts',
  testIgnore: accountFlowMode
    ? []
    : fullStackMode
      ? ['**/account-full-stack.spec.ts']
      : process.env.E2E_LIVE === '1'
        ? ['**/account-full-stack.spec.ts', '**/full-stack-local.spec.ts', ...(releaseProxyMode ? [] : ['**/release-proxy.spec.ts'])]
        : ['**/account-full-stack.spec.ts', '**/full-stack-local.spec.ts', '**/live-backend.spec.ts', ...(releaseProxyMode ? [] : ['**/release-proxy.spec.ts'])],
  timeout: 60_000,
  expect: {
    timeout: 10_000,
  },
  fullyParallel: false,
  workers: Number.isFinite(workerCount) && workerCount > 0 ? workerCount : 1,
  reporter: process.env.CI ? 'github' : 'list',
  use: {
    baseURL,
    trace: accountFlowMode ? 'off' : 'retain-on-failure',
  },
  webServer: process.env.E2E_SKIP_WEB_SERVER === '1'
    ? undefined
    : accountFlowMode
      ? [
          {
            command: 'go run -tags=integration,account_e2e ./integration_test/accountflow/harness',
            cwd: backendRoot,
            url: `${accountBackendURL}/__test/ready`,
            name: 'Account test backend',
            reuseExistingServer: false,
            timeout: 120_000,
            gracefulShutdown: {
              signal: 'SIGTERM',
              timeout: 120_000,
            },
            env: {
              ...process.env,
              E2E_ACCOUNT_BACKEND_PORT: accountBackendPort,
              E2E_ACCOUNT_FRONTEND_ORIGIN: baseURL,
              GOTOOLCHAIN: 'local',
              GOPROXY: 'off',
              GOSUMDB: 'off',
            },
          },
          {
            command: `npm run dev -- --hostname 127.0.0.1 --port ${port}`,
            cwd: frontendRoot,
            url: baseURL,
            name: 'Account test frontend',
            reuseExistingServer: false,
            timeout: 120_000,
            env: {
              ...process.env,
              BACKEND_URL: backendURL,
              NEXT_PUBLIC_API_URL: '',
              NEXT_PUBLIC_ADMIN_API_URL: '',
              EMAIL_PROVIDER: 'disabled',
              RESEND_ENABLED: 'false',
              RESEND_API_KEY: '',
              SMTP_HOST: '',
              SMTP_USERNAME: '',
              SMTP_PASSWORD: '',
            },
          },
        ]
    : {
        cwd: frontendRoot,
        command: `npm run dev -- --hostname 127.0.0.1 --port ${port}`,
        url: baseURL,
        reuseExistingServer,
        timeout: 120_000,
        env: {
          ...process.env,
          BACKEND_URL: backendURL,
          NEXT_PUBLIC_API_URL: process.env.NEXT_PUBLIC_API_URL || '',
          NEXT_PUBLIC_ADMIN_API_URL: process.env.NEXT_PUBLIC_ADMIN_API_URL || '',
        },
      },
  projects: [
    {
      name: 'chromium',
      use: {
        ...devices['Desktop Chrome'],
        launchOptions: {
          executablePath: chromiumExecutable,
        },
      },
    },
  ],
});
