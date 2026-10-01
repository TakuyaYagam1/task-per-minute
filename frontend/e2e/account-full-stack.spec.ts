import { randomUUID } from 'node:crypto';
import { readFile } from 'node:fs/promises';
import { resolve } from 'node:path';

import { expect, test, type APIRequestContext, type Page } from '@playwright/test';
import { openAccountMenu } from './support/common';

const frontendURL = (process.env.E2E_FRONTEND_URL || `http://127.0.0.1:${process.env.E2E_FRONTEND_PORT || '3101'}`).replace(/\/+$/, '');
const backendURL = (process.env.E2E_ACCOUNT_BACKEND_URL || `http://127.0.0.1:${process.env.E2E_ACCOUNT_BACKEND_PORT || '4319'}`).replace(/\/+$/, '');
const accountPassword = 'Account-test-password-9284';
const accountFlowAdminPassword = 'Account-flow-admin-password-384';
const registrationMessage = 'Письмо было отправлено на указанную почту.';
const avatarVideoPlaybackError = 'Не удалось воспроизвести видео профиля.';

const mediaPolicyViolations = async (page: Page): Promise<string[]> => page.evaluate(() => (
  (window as Window & { __mediaPolicyViolations?: string[] }).__mediaPolicyViolations ?? []
));

const expectNoPlayerAuthState = async (
  page: Page,
  email: string,
  password: string,
  username: string,
): Promise<void> => {
  const cookies = await page.context().cookies(backendURL);
  expect(cookies.some((cookie) => cookie.name === 'tpm_player_session')).toBe(false);
  expect(cookies.some((cookie) => cookie.name === 'tpm_player_csrf')).toBe(false);

  const storage = await page.evaluate(() => ({
    playerId: window.sessionStorage.getItem('player_id'),
    username: window.sessionStorage.getItem('username'),
    local: Object.entries(window.localStorage),
    session: Object.entries(window.sessionStorage),
  }));
  expect(storage.playerId).toBeNull();
  expect(storage.username).toBeNull();
  expect(storage.local.some(([key]) => key === 'player_id' || key === 'username')).toBe(false);
  expect(storage.session.some(([key]) => key === 'player_id' || key === 'username')).toBe(false);
  const saved = JSON.stringify(storage);
  expect(saved).not.toContain(email);
  expect(saved).not.toContain(password);
  expect(saved).not.toContain(username);
};

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

const waitForEmailChangeCode = async (
  request: APIRequestContext,
  email: string,
): Promise<string> => {
  const deadline = Date.now() + 20_000;
  const captureURL = `${backendURL}/__test/email-change-code?email=${encodeURIComponent(email)}`;

  while (Date.now() < deadline) {
    const response = await request.get(captureURL, { timeout: 2_000 });
    if (response.status() === 200) {
      const captured = await response.json() as { code?: unknown };
      if (typeof captured.code !== 'string' || !/^[0-9]{6}$/.test(captured.code)) {
        throw new Error('Captured email-change message did not contain a valid code');
      }
      return captured.code;
    }
    if (response.status() !== 204) {
      throw new Error('The email-change test capture returned an unexpected response');
    }
    await new Promise((resolve) => setTimeout(resolve, 100));
  }

  throw new Error('No email-change message reached the account test capture');
};

test('a player can change account details and manage a private avatar', async ({ page, request }) => {
  test.setTimeout(180_000);
  await page.addInitScript(() => {
    const target = window as Window & { __mediaPolicyViolations?: string[] };
    target.__mediaPolicyViolations = [];
    window.addEventListener('securitypolicyviolation', (event) => {
      if (event.effectiveDirective === 'media-src') target.__mediaPolicyViolations?.push(event.blockedURI);
    });
  });
  await page.emulateMedia({ reducedMotion: 'no-preference' });
  const identity = randomUUID().replaceAll('-', '').slice(0, 16);
  const username = `settings_${identity}`;
  const email = `settings-${identity}@example.invalid`;
  const renamedUsername = `updated_${identity}`;
  const newEmail = `changed-${identity}@example.invalid`;
  const newPassword = 'Changed-account-password-4728';
  const wrongPassword = 'Wrong-current-password-7391';
  const avatar = await readFile(resolve(
    process.cwd(),
    '../backend/internal/adapter/outbound/media/ffmpeg/testdata/avatar.mp4',
  ));

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
  expect((await registrationResponsePromise).status()).toBe(202);
  const activationURL = await waitForVerificationLink(request);

  await page.goto(activationURL, { waitUntil: 'domcontentloaded' });
  const verificationResponsePromise = page.waitForResponse((response) => (
    new URL(response.url()).pathname === '/api/v1/players/verify-email'
      && response.request().method() === 'POST'
  ));
  await page.getByRole('button', { name: 'Подтвердить email', exact: true }).click();
  expect((await verificationResponsePromise).status()).toBe(204);

  await page.goto('/login');
  await page.getByLabel('Логин или email').fill(email);
  await page.getByLabel('Пароль', { exact: true }).fill(accountPassword);
  const loginResponsePromise = page.waitForResponse((response) => (
    new URL(response.url()).pathname === '/api/v1/players/login'
      && response.request().method() === 'POST'
  ));
  await page.getByRole('button', { name: 'Войти', exact: true }).click();
  expect((await loginResponsePromise).status()).toBe(200);

  await page.goto('/settings');
  await expect(page.getByRole('heading', { name: 'Настройки', exact: true })).toBeVisible();
  await expect(page.getByText(email, { exact: true })).toBeVisible();

  await page.getByRole('button', { name: 'Изменить логин', exact: true }).click();
  await page.getByLabel('Новый логин', { exact: true }).fill(renamedUsername);
  await page.getByLabel('Текущий пароль', { exact: true }).fill(wrongPassword);
  const rejectedUsernameResponsePromise = page.waitForResponse((response) => (
    new URL(response.url()).pathname === '/api/v1/players/account/username'
      && response.request().method() === 'POST'
  ));
  await page.getByRole('button', { name: 'Изменить логин', exact: true }).click();
  const rejectedUsernameResponse = await rejectedUsernameResponsePromise;
  expect(rejectedUsernameResponse.status()).toBe(422);
  expect((await rejectedUsernameResponse.json() as { code?: string }).code)
    .toBe('player.current_password_invalid');
  const sessionAfterRejectedChange = (await page.context().cookies(backendURL))
    .find((cookie) => cookie.name === 'tpm_player_session')?.value;
  expect(sessionAfterRejectedChange).toBeTruthy();
  await expect(page.getByRole('alert').filter({ hasText: 'Текущий пароль указан неверно.' })).toBeVisible();
  await page.getByLabel('Текущий пароль', { exact: true }).fill(accountPassword);

  const usernameResponsePromise = page.waitForResponse((response) => (
    new URL(response.url()).pathname === '/api/v1/players/account/username'
      && response.request().method() === 'POST'
  ));
  await page.getByRole('button', { name: 'Изменить логин', exact: true }).click();
  const usernameResponse = await usernameResponsePromise;
  expect(usernameResponse.status()).toBe(200);
  await expect(page.getByText(renamedUsername, { exact: true })).toBeVisible();

  await page.getByRole('button', { name: 'Безопасность', exact: true }).click();
  await page.getByRole('button', { name: 'Изменить пароль', exact: true }).click();
  await page.getByLabel('Текущий пароль', { exact: true }).fill(accountPassword);
  await page.getByLabel('Новый пароль', { exact: true }).fill(newPassword);
  await page.getByLabel('Повторите новый пароль', { exact: true }).fill(newPassword);
  const sessionBeforePasswordChange = (await page.context().cookies(backendURL))
    .find((cookie) => cookie.name === 'tpm_player_session')?.value;
  const passwordResponsePromise = page.waitForResponse((response) => (
    new URL(response.url()).pathname === '/api/v1/players/account/password'
      && response.request().method() === 'POST'
  ));
  await page.getByRole('button', { name: 'Изменить пароль', exact: true }).click();
  const passwordResponse = await passwordResponsePromise;
  expect(passwordResponse.status()).toBe(204);
  const passwordSessionCookies = await page.context().cookies(backendURL);
  const sessionAfterPasswordChange = passwordSessionCookies.find((cookie) => cookie.name === 'tpm_player_session')?.value;
  const csrfAfterPasswordChange = passwordSessionCookies.find((cookie) => cookie.name === 'tpm_player_csrf')?.value;
  expect(sessionAfterPasswordChange).toBeTruthy();
  expect(sessionAfterPasswordChange).not.toBe(sessionBeforePasswordChange);
  expect(csrfAfterPasswordChange).toBeTruthy();
  expect(passwordResponse.headers()['x-csrf-token']).toBe(csrfAfterPasswordChange);
  await expect(page.getByRole('status').getByText('Пароль изменен.', { exact: true })).toBeVisible();

  await page.getByRole('button', { name: 'Профиль', exact: true }).click();
  await page.getByRole('button', { name: 'Изменить email', exact: true }).click();
  await page.getByLabel('Новый email', { exact: true }).fill(newEmail);
  await page.getByLabel('Текущий пароль', { exact: true }).fill(newPassword);
  const beginEmailResponsePromise = page.waitForResponse((response) => (
    new URL(response.url()).pathname === '/api/v1/players/account/email'
      && response.request().method() === 'POST'
  ));
  await page.getByRole('button', { name: 'Продолжить', exact: true }).click();
  const beginEmailResponse = await beginEmailResponsePromise;
  expect(beginEmailResponse.status()).toBe(202);
  await expect(page.getByText(email, { exact: true })).toBeVisible();
  await expect(page.getByText(newEmail, { exact: true })).toBeVisible();

  const emailCode = await waitForEmailChangeCode(request, newEmail);
  await page.getByLabel('Код подтверждения', { exact: true }).fill(emailCode);
  const sessionBeforeEmailConfirm = (await page.context().cookies(backendURL))
    .find((cookie) => cookie.name === 'tpm_player_session')?.value;
  const confirmEmailResponsePromise = page.waitForResponse((response) => (
    new URL(response.url()).pathname === '/api/v1/players/account/email/confirm'
      && response.request().method() === 'POST'
  ));
  await page.getByRole('button', { name: 'Подтвердить почту', exact: true }).click();
  const confirmEmailResponse = await confirmEmailResponsePromise;
  expect(confirmEmailResponse.status()).toBe(200);
  const confirmation = await confirmEmailResponse.json() as { email?: string; previous_email_notified?: boolean };
  expect(confirmation.email).toBe(newEmail);
  expect(typeof confirmation.previous_email_notified).toBe('boolean');
  const emailSessionCookies = await page.context().cookies(backendURL);
  const sessionAfterEmailConfirm = emailSessionCookies.find((cookie) => cookie.name === 'tpm_player_session')?.value;
  const csrfAfterEmailConfirm = emailSessionCookies.find((cookie) => cookie.name === 'tpm_player_csrf')?.value;
  expect(sessionAfterEmailConfirm).toBeTruthy();
  expect(sessionAfterEmailConfirm).not.toBe(sessionBeforeEmailConfirm);
  expect(csrfAfterEmailConfirm).toBeTruthy();
  expect(confirmEmailResponse.headers()['x-csrf-token']).toBe(csrfAfterEmailConfirm);
  await expect(page.getByText(newEmail, { exact: true })).toBeVisible();

  await page.reload();
  await expect(page.getByText(renamedUsername, { exact: true })).toBeVisible();
  await expect(page.getByText(newEmail, { exact: true })).toBeVisible();

  const avatarInput = page.getByLabel('Выбрать аватар', { exact: true });
  const supportsH264 = await page.evaluate(() => {
    const probe = document.createElement('video');
    return probe.canPlayType('video/mp4; codecs="avc1.42E01E"') !== '';
  });
  await avatarInput.setInputFiles({ name: 'profile.mp4', mimeType: 'video/mp4', buffer: avatar });
  const avatarVideo = page.getByLabel('Видео аватара', { exact: true });
  const playbackError = page.getByRole('alert').filter({ hasText: avatarVideoPlaybackError });
  if (supportsH264) {
    await expect(avatarVideo).toBeVisible();
    await expect(avatarVideo).toHaveAttribute('src', /^blob:/);
    expect(await avatarVideo.evaluate((element) => {
      const media = element as HTMLVideoElement;
      return { muted: media.muted, playsInline: media.playsInline, loop: media.loop, autoplay: media.autoplay };
    })).toEqual({ muted: true, playsInline: true, loop: true, autoplay: true });
    await expect.poll(() => avatarVideo.evaluate((element) => (element as HTMLVideoElement).readyState >= 2)).toBe(true);
    await expect.poll(() => avatarVideo.evaluate((element) => (element as HTMLVideoElement).currentTime)).toBeGreaterThan(0);
    await expect.poll(() => avatarVideo.evaluate((element) => !(element as HTMLVideoElement).paused)).toBe(true);
    await expect(page.getByRole('button', { name: /видео аватара/i })).toHaveCount(0);
  } else {
    await expect(playbackError).toBeVisible();
    await expect(avatarVideo).toHaveCount(0);
  }
  expect(await mediaPolicyViolations(page)).toEqual([]);
  const avatarResponse = await page.context().request.get(`${backendURL}/api/v1/players/account/avatar`, {
    headers: { Origin: frontendURL },
  });
  expect(avatarResponse.status()).toBe(200);
  expect(avatarResponse.headers()['content-type']).toContain('video/mp4');

  await page.reload();
  await expect(page.getByText(renamedUsername, { exact: true })).toBeVisible();
  await expect(page.getByText(newEmail, { exact: true })).toBeVisible();
  if (supportsH264) {
    const reloadedVideo = page.getByLabel('Видео аватара', { exact: true });
    await expect(reloadedVideo).toBeVisible();
    await expect(reloadedVideo).toHaveAttribute('src', /^blob:/);
    expect(await reloadedVideo.evaluate((element) => {
      const media = element as HTMLVideoElement;
      return { muted: media.muted, playsInline: media.playsInline, loop: media.loop, autoplay: media.autoplay };
    })).toEqual({ muted: true, playsInline: true, loop: true, autoplay: true });
    await expect.poll(() => reloadedVideo.evaluate((element) => (element as HTMLVideoElement).readyState >= 2)).toBe(true);
    await expect.poll(() => reloadedVideo.evaluate((element) => (element as HTMLVideoElement).currentTime)).toBeGreaterThan(0);
    await expect.poll(() => reloadedVideo.evaluate((element) => !(element as HTMLVideoElement).paused)).toBe(true);
    await expect(page.getByRole('button', { name: /видео аватара/i })).toHaveCount(0);
  } else {
    await expect(page.getByRole('alert').filter({ hasText: avatarVideoPlaybackError })).toBeVisible();
  }
  expect(await mediaPolicyViolations(page)).toEqual([]);

  await page.getByRole('button', { name: 'Удалить', exact: true }).click();
  await expect(page.getByLabel('Видео аватара', { exact: true })).toHaveCount(0);
  await expect(page.getByRole('img', { name: 'Инициалы аватара', exact: true })).toBeVisible();
  const missingAvatarResponse = await page.context().request.get(`${backendURL}/api/v1/players/account/avatar`, {
    headers: { Origin: frontendURL },
  });
  expect(missingAvatarResponse.status()).toBe(404);

  const storage = await page.evaluate(() => JSON.stringify({
    local: Object.entries(window.localStorage),
    session: Object.entries(window.sessionStorage),
  }));
  expect(storage).not.toContain(accountPassword);
  expect(storage).not.toContain(newPassword);
  expect(storage).not.toContain(emailCode);
  expect(storage).not.toContain(newEmail);

  await page.getByRole('button', { name: 'Меню аккаунта', exact: true }).click();
  const logoutResponsePromise = page.waitForResponse((response) => (
    new URL(response.url()).pathname === '/api/v1/players/logout'
      && response.request().method() === 'POST'
  ));
  await page.getByRole('button', { name: 'Выйти', exact: true }).click();
  expect((await logoutResponsePromise).status()).toBe(204);

  await page.goto('/login');
  await page.getByLabel('Логин или email').fill(newEmail);
  await page.getByLabel('Пароль', { exact: true }).fill(newPassword);
  const updatedLoginResponsePromise = page.waitForResponse((response) => (
    new URL(response.url()).pathname === '/api/v1/players/login'
      && response.request().method() === 'POST'
  ));
  await page.getByRole('button', { name: 'Войти', exact: true }).click();
  expect((await updatedLoginResponsePromise).status()).toBe(200);
  await expect(page.getByRole('dialog', { name: 'Аккаунт удален' })).toHaveCount(0);
  await expect(page.getByRole('alert').filter({ hasText: /\S/ })).toHaveCount(0);
});

test('a deleted player can re-register after admin deletion', async ({ page, request }) => {
  test.setTimeout(240_000);
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
  await expectNoPlayerAuthState(page, email, accountPassword, username);

  const originalActivationURL = await waitForVerificationLink(request);
  await expect(page.getByRole('link', { name: 'Перейти ко входу', exact: true })).toHaveCount(0);
  const registrationResend = page.getByRole('button', { name: 'Не пришло письмо?', exact: true });
  await expect(registrationResend).toBeDisabled();
  await expect(registrationResend).toBeEnabled({ timeout: 65_000 });
  const registrationResendPromise = page.waitForResponse((response) => (
    new URL(response.url()).pathname === '/api/v1/players/resend-verification'
      && response.request().method() === 'POST'
  ));
  await registrationResend.click();
  const registrationResendResponse = await registrationResendPromise;
  expect(registrationResendResponse.status()).toBe(202);
  expect(registrationResendResponse.request().postDataJSON()).toEqual({ email });
  expect(await registrationResendResponse.headerValue('set-cookie')).toBeNull();
  await expect(page).toHaveURL(`${frontendURL}/register`);
  await expect(registrationResend).toBeDisabled();
  const activationURL = await waitForVerificationLink(request);
  expect(activationURL).not.toBe(originalActivationURL);
  await expectNoPlayerAuthState(page, email, accountPassword, username);

  await page.getByRole('link', { name: 'Войти', exact: true }).click();
  await page.getByLabel('Логин или email').fill(email);
  await page.getByLabel('Пароль', { exact: true }).fill('Wrong-test-password-2874');
  const wrongPendingLoginPromise = page.waitForResponse((response) => (
    new URL(response.url()).pathname === '/api/v1/players/login'
      && response.request().method() === 'POST'
  ));
  await page.getByRole('button', { name: 'Войти', exact: true }).click();
  const wrongPendingLogin = await wrongPendingLoginPromise;
  expect(wrongPendingLogin.status()).toBe(401);
  expect(await wrongPendingLogin.headerValue('set-cookie')).toBeNull();
  expect(await wrongPendingLogin.headerValue('x-csrf-token')).toBeNull();
  const wrongPendingProblem = await wrongPendingLogin.json() as { code?: string; detail?: string };
  expect(wrongPendingProblem.code).not.toBe('player.email_unverified');
  expect(wrongPendingProblem.detail).toBe('invalid credentials');
  await expect(page.getByText('Неверный логин или пароль.', { exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Отправить письмо подтверждения', exact: true })).toHaveCount(0);
  await expectNoPlayerAuthState(page, email, 'Wrong-test-password-2874', username);

  await page.getByLabel('Пароль', { exact: true }).fill(accountPassword);
  const pendingLoginPromise = page.waitForResponse((response) => (
    new URL(response.url()).pathname === '/api/v1/players/login'
      && response.request().method() === 'POST'
  ));
  await page.getByRole('button', { name: 'Войти', exact: true }).click();
  const pendingLogin = await pendingLoginPromise;
  expect(pendingLogin.status()).toBe(401);
  expect(await pendingLogin.headerValue('set-cookie')).toBeNull();
  expect(await pendingLogin.headerValue('x-csrf-token')).toBeNull();
  const pendingProblem = await pendingLogin.json() as { code?: string; detail?: string };
  expect(pendingProblem.code).toBe('player.email_unverified');
  expect(pendingProblem.detail).toBe('activate your account by verifying your email before signing in');
  await expect(page.getByText('Аккаунт не активирован. Подтвердите email по ссылке из письма.', { exact: true })).toBeVisible();
  const resendButton = page.getByRole('button', { name: 'Отправить письмо подтверждения', exact: true });
  await expect(resendButton).toBeEnabled();
  await expectNoPlayerAuthState(page, email, accountPassword, username);

  const resendRequestPromise = page.waitForRequest((request) => (
    new URL(request.url()).pathname === '/api/v1/players/login/resend-verification'
      && request.method() === 'POST'
  ));
  const resendResponsePromise = page.waitForResponse((response) => (
    new URL(response.url()).pathname === '/api/v1/players/login/resend-verification'
      && response.request().method() === 'POST'
  ));
  await resendButton.click();
  const resendRequest = await resendRequestPromise;
  expect(resendRequest.postDataJSON()).toEqual({ login: email, password: accountPassword });
  const resendResponse = await resendResponsePromise;
  expect(resendResponse.status()).toBe(429);
  expect(await resendResponse.headerValue('retry-after')).toBe('60');
  expect(await resendResponse.headerValue('set-cookie')).toBeNull();
  expect(await resendResponse.headerValue('x-csrf-token')).toBeNull();
  const resendProblem = await resendResponse.json() as { code?: string };
  expect(resendProblem.code).toBe('rate_limited');
  await expect(page.getByRole('alert').filter({ hasText: 'Слишком много попыток. Подождите перед повторным запросом.' })).toBeVisible();
  await expect(page.getByText(/Повторная отправка доступна через \d+ с\./)).toBeVisible();
  await expect(resendButton).toBeDisabled();
  await expectNoPlayerAuthState(page, email, accountPassword, username);

  const noExtraMail = await request.get(`${backendURL}/__test/verification-link`);
  expect(noExtraMail.status()).toBe(204);
  await page.goto(activationURL, { waitUntil: 'domcontentloaded' });
  await expect(page.getByRole('heading', { name: 'Подтверждение email', exact: true })).toBeVisible();
  await expect(page.getByText('Подтвердите адрес из письма, чтобы войти в аккаунт.', { exact: true })).toHaveCount(0);
  await expect(page.getByLabel('Email для нового письма')).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Отправить новое письмо', exact: true })).toHaveCount(0);
  await expect(page.getByText(/Не пришло письмо/)).toHaveCount(0);
  await expect(page.getByRole('link', { name: 'Перейти ко входу', exact: true })).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Подтвердить email', exact: true })).toBeVisible();
  const verificationResponsePromise = page.waitForResponse((response) => (
    new URL(response.url()).pathname === '/api/v1/players/verify-email'
      && response.request().method() === 'POST'
  ));
  await page.getByRole('button', { name: 'Подтвердить email', exact: true }).click();
  const verificationResponse = await verificationResponsePromise;
  expect(verificationResponse.status()).toBe(204);
  await expect(page.getByRole('status').getByText('Email подтверждён. Переход ко входу через 5 секунд.')).toBeVisible();

  await page.getByRole('link', { name: 'Войти', exact: true }).click();
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
  const initialPlayer = await loginResponse.json() as { player_id: string };

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
  expect(playerSession?.value).toBeTruthy();
  expect(playerCSRF?.httpOnly).toBe(false);
  expect(loginResponse.headers()['x-csrf-token']).toBe(playerCSRF?.value);

  await page.reload();
  await expect(page.getByText(username, { exact: true })).toBeVisible();
  const initialAccountMenu = await openAccountMenu(page);
  await expect(initialAccountMenu.getByRole('button', { name: 'Выйти', exact: true })).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(initialAccountMenu).toBeHidden();

  const adminPage = await page.context().newPage();
  await adminPage.goto('/admin');
  await adminPage.getByPlaceholder('Введите пароль...').fill(accountFlowAdminPassword);
  await adminPage.getByRole('button', { name: 'Войти' }).click();
  await adminPage.getByRole('button', { name: 'Игроки' }).click();
  await expect(adminPage.getByText(username, { exact: true })).toBeVisible();
  adminPage.once('dialog', (dialog) => dialog.accept());
  const deleteResponsePromise = adminPage.waitForResponse((response) => (
    new URL(response.url()).pathname === `/api/v1/admin/players/${initialPlayer.player_id}`
      && response.request().method() === 'DELETE'
  ));
  await adminPage.getByRole('button', { name: `Удалить игрока ${username}`, exact: true }).click();
  const deleteResponse = await deleteResponsePromise;
  expect(deleteResponse.status()).toBe(204);
  await expect(adminPage.getByText('Игрок удалён', { exact: true })).toBeVisible();
  await adminPage.close();

  await page.bringToFront();
  const deletedNotice = page.getByRole('dialog', { name: 'Аккаунт удален' });
  await expect(deletedNotice).toBeVisible({ timeout: 20_000 });
  await expect(deletedNotice.getByText('Администратор удалил ваш аккаунт.', { exact: true })).toBeVisible();
  await expectNoPlayerAuthState(page, email, accountPassword, username);
  await deletedNotice.getByRole('button', { name: 'Понятно', exact: true }).click();
  await expect(page).toHaveURL(`${frontendURL}/login`);

  await page.getByRole('link', { name: 'Создать аккаунт', exact: true }).click();
  await expect(page).toHaveURL(`${frontendURL}/register`);
  await page.getByLabel('Логин, видимый в рейтинге').fill(username);
  await page.getByLabel('Email', { exact: true }).fill(email);
  await page.getByLabel('Пароль', { exact: true }).fill(accountPassword);
  await page.getByLabel('Повторите пароль').fill(accountPassword);
  const replacementRegistrationPromise = page.waitForResponse((response) => (
    new URL(response.url()).pathname === '/api/v1/players/register'
      && response.request().method() === 'POST'
  ));
  await page.getByRole('button', { name: 'Создать аккаунт', exact: true }).click();
  const replacementRegistration = await replacementRegistrationPromise;
  expect(replacementRegistration.status()).toBe(202);
  await expect(page.getByRole('status').getByText(registrationMessage)).toBeVisible();
  const replacementActivationURL = await waitForVerificationLink(request);
  await expectNoPlayerAuthState(page, email, accountPassword, username);

  await page.goto(replacementActivationURL, { waitUntil: 'domcontentloaded' });
  await expect(page.getByRole('button', { name: 'Подтвердить email', exact: true })).toBeVisible();
  const replacementVerificationPromise = page.waitForResponse((response) => (
    new URL(response.url()).pathname === '/api/v1/players/verify-email'
      && response.request().method() === 'POST'
  ));
  await page.getByRole('button', { name: 'Подтвердить email', exact: true }).click();
  const replacementVerification = await replacementVerificationPromise;
  expect(replacementVerification.status()).toBe(204);
  await expect(page.getByRole('status').getByText('Email подтверждён. Переход ко входу через 5 секунд.')).toBeVisible();
  await page.getByRole('link', { name: 'Войти', exact: true }).click();
  await expect(page).toHaveURL(`${frontendURL}/login`);
  await page.getByLabel('Логин или email').fill(email);
  await page.getByLabel('Пароль', { exact: true }).fill(accountPassword);
  const replacementLoginPromise = page.waitForResponse((response) => (
    new URL(response.url()).pathname === '/api/v1/players/login'
      && response.request().method() === 'POST'
  ));
  await page.getByRole('button', { name: 'Войти', exact: true }).click();
  const replacementLogin = await replacementLoginPromise;
  expect(replacementLogin.status()).toBe(200);
  const replacementPlayer = await replacementLogin.json() as { player_id: string };
  expect(replacementPlayer.player_id).not.toBe(initialPlayer.player_id);
  const replacementCookies = await page.context().cookies(backendURL);
  const replacementSession = replacementCookies.find((cookie) => cookie.name === 'tpm_player_session');
  expect(replacementSession?.value).toBeTruthy();
  await expect(page).toHaveURL(`${frontendURL}/`);
  await expect(page.getByText(username, { exact: true })).toBeVisible();

  const deletedSessionResponse = await request.get(`${backendURL}/api/v1/players/me`, {
    headers: {
      Origin: frontendURL,
      Cookie: `tpm_player_session=${playerSession?.value}`,
    },
  });
  expect(deletedSessionResponse.status()).toBe(401);
  const deletedSessionProblem = await deletedSessionResponse.json() as { code?: string };
  expect(deletedSessionProblem.code).toBe('player.account_deleted');

  const replacementAccountMenu = await openAccountMenu(page);
  const replacementLogout = replacementAccountMenu.getByRole('button', { name: 'Выйти', exact: true });
  await expect(replacementLogout).toBeVisible();
  await replacementLogout.click();
  await expect(page.getByRole('link', { name: 'Войти', exact: true })).toBeVisible();
  await expect(page.getByRole('link', { name: 'Создать аккаунт', exact: true })).toBeVisible();

  const staleSessionResponse = await request.get(`${backendURL}/api/v1/players/me`, {
    headers: {
      Origin: frontendURL,
      Cookie: `tpm_player_session=${replacementSession?.value}`,
    },
  });
  expect(staleSessionResponse.status()).toBe(401);
});
