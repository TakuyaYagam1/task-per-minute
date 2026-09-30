import { randomUUID } from 'node:crypto';

import { expect, test, type APIRequestContext } from '@playwright/test';

const frontendURL = (process.env.E2E_FRONTEND_URL || `http://127.0.0.1:${process.env.E2E_FRONTEND_PORT || '3101'}`).replace(/\/+$/, '');
const backendURL = (process.env.E2E_ACCOUNT_BACKEND_URL || `http://127.0.0.1:${process.env.E2E_ACCOUNT_BACKEND_PORT || '4319'}`).replace(/\/+$/, '');
const accountPassword = 'Account-test-password-9284';
const registrationMessage = 'Если этот адрес можно зарегистрировать, мы отправили письмо со ссылкой для подтверждения.';

type CapturedMail = {
  verification_url: string;
};

const waitForVerificationLink = async (request: APIRequestContext): Promise<string> => {
  const deadline = Date.now() + 20_000;

  while (Date.now() < deadline) {
    const response = await request.get(`${backendURL}/__test/verification-link`, { timeout: 2_000 });
    if (response.status() === 200) {
      const message = (await response.json()) as CapturedMail;
      const link = message.verification_url;
      const linkPrefix = `${frontendURL}/verify-email#token=`;
      if (!link.startsWith(linkPrefix) || link.length <= linkPrefix.length) {
        throw new Error('Captured verification message did not contain a local activation link');
      }
      return link;
    }
    if (response.status() !== 204) {
      throw new Error('The account test mail capture returned an unexpected response');
    }
    await new Promise((resolve) => setTimeout(resolve, 100));
  }

  throw new Error('No verification message reached the account test mail capture');
};

test('a player verifies email, restores a cookie session and logs out', async ({ page, request }) => {
  const identity = randomUUID().replaceAll('-', '').slice(0, 16);
  const username = `e2e_${identity}`;
  const email = `account-${identity}@example.invalid`;

  await page.goto('/register');
  await page.getByLabel('Логин, видимый в рейтинге').fill(username);
  await page.getByLabel('Email', { exact: true }).fill(email);
  await page.getByLabel('Пароль', { exact: true }).fill(accountPassword);
  await page.getByLabel('Повторите пароль').fill(accountPassword);

  const registrationResponsePromise = page.waitForResponse((response) => (
    new URL(response.url()).pathname === '/api/v1/players/register'
      && response.request().method() === 'POST'
  ));
  await page.getByRole('button', { name: 'Создать аккаунт', exact: true }).click();
  const registrationResponse = await registrationResponsePromise;
  expect(registrationResponse.status()).toBe(202);
  await expect(page.getByRole('status').getByText(registrationMessage)).toBeVisible();

  const cookiesBeforeActivation = await page.context().cookies(backendURL);
  expect(cookiesBeforeActivation.some((cookie) => cookie.name === 'tpm_player_session')).toBe(false);

  const pendingLogin = await request.post(`${backendURL}/api/v1/players/login`, {
    headers: { Origin: frontendURL },
    data: { login: email, password: accountPassword },
  });
  expect(pendingLogin.status()).toBe(401);

  const activationURL = await waitForVerificationLink(request);
  await page.goto(activationURL, { waitUntil: 'domcontentloaded' });
  await expect(page.getByRole('button', { name: 'Подтвердить email', exact: true })).toBeVisible();
  const verificationResponsePromise = page.waitForResponse((response) => (
    new URL(response.url()).pathname === '/api/v1/players/verify-email'
      && response.request().method() === 'POST'
  ));
  await page.getByRole('button', { name: 'Подтвердить email', exact: true }).click();
  const verificationResponse = await verificationResponsePromise;
  expect(verificationResponse.status()).toBe(204);
  await expect(page.getByRole('status').getByText('Email подтверждён. Теперь можно войти.')).toBeVisible();

  await page.getByRole('link', { name: 'Перейти ко входу' }).click();
  await expect(page).toHaveURL(`${frontendURL}/login`);
  await page.getByLabel('Логин или email').fill(email);
  await page.getByLabel('Пароль', { exact: true }).fill(accountPassword);

  const loginResponsePromise = page.waitForResponse((response) => (
    new URL(response.url()).pathname === '/api/v1/players/login'
      && response.request().method() === 'POST'
  ));
  await page.getByRole('button', { name: 'Войти', exact: true }).click();
  const loginResponse = await loginResponsePromise;
  expect(loginResponse.status()).toBe(200);

  await expect(page).toHaveURL(`${frontendURL}/`);
  const currentPlayerResponse = await page.context().request.get(`${backendURL}/api/v1/players/me`, {
    headers: { Origin: frontendURL },
  });
  expect(currentPlayerResponse.status()).toBe(200);
  const currentPlayer = (await currentPlayerResponse.json()) as { player: { username: string } };
  expect(currentPlayer.player.username).toBe(username);

  const cookies = await page.context().cookies(backendURL);
  const playerSession = cookies.find((cookie) => cookie.name === 'tpm_player_session');
  const playerCSRF = cookies.find((cookie) => cookie.name === 'tpm_player_csrf');
  expect(playerSession?.httpOnly).toBe(true);
  expect(playerCSRF?.httpOnly).toBe(false);
  expect(loginResponse.headers()['x-csrf-token']).toBe(playerCSRF?.value);

  await page.reload();
  await expect(page.getByText(username, { exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Выйти', exact: true })).toBeVisible();

  await page.getByRole('button', { name: 'Выйти', exact: true }).click();
  await expect(page.getByRole('link', { name: 'Войти', exact: true })).toBeVisible();
  await expect(page.getByRole('link', { name: 'Создать аккаунт', exact: true })).toBeVisible();

  const staleSessionResponse = await request.get(`${backendURL}/api/v1/players/me`, {
    headers: {
      Origin: frontendURL,
      Cookie: `tpm_player_session=${playerSession?.value}`,
    },
  });
  expect(staleSessionResponse.status()).toBe(401);
});
