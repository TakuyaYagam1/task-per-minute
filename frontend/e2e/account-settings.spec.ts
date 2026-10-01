import { readFile } from 'node:fs/promises';
import { resolve } from 'node:path';

import { expect, test, type Locator, type Page } from '@playwright/test';

import { jsonHeaders } from './support/common';

test.use({ trace: 'off' });

const usernameError = 'Логин должен содержать 2-50 латинских букв, цифр, _ или -.';
const emailError = 'Введите полный email, например name@example.com.';
const emailCodeLengthError = 'Введите 6 цифр.';
const passwordLengthError = 'Пароль должен содержать от 10 до 128 символов.';
const passwordMismatchError = 'Пароли не совпадают.';
const avatarTypeError = 'Выберите JPG, PNG, GIF или видео MP4.';
const avatarSizeError = 'Размер файла должен быть не более 5 МБ.';
const avatarDecodeError = 'Не удалось открыть изображение или видео. Выберите другой файл.';
const avatarPlaybackError = 'Не удалось воспроизвести видео профиля.';
const avatarVideoUnavailableError = 'Обработка видео сейчас недоступна. Попробуйте позже.';

const player = {
  created_at: '2026-10-01T00:00:00Z',
  id: '77777777-7777-7777-7777-777777777777',
  username: 'demo',
} as const;

const tinyPNG = Buffer.from(
  'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+j9pAAAAAASUVORK5CYII=',
  'base64',
);
const tinyGIF = Buffer.from(
  'R0lGODlhAQABAIAAAAAAAP///yH5BAEAAAAALAAAAAABAAEAAAIBRAA7',
  'base64',
);

type CurrentPlayerReply = {
  status: number;
  player?: typeof player;
};

type MockAPIRequest = {
  method: string;
  path: string;
  body?: unknown;
  csrfToken?: string | null;
};

type MockAPIOptions = {
  initialAvatar?: boolean;
  avatarVideo?: Buffer;
  avatarUploadStatus?: number;
  avatarDeleteStatus?: number;
  delayStalePollResponse?: boolean;
  delayPasswordResponse?: boolean;
};

const mockAccountAPI = async (
  page: Page,
  replies: CurrentPlayerReply[],
  cacheIdentity: boolean,
  options: MockAPIOptions = {},
) => {
  let meCalls = 0;
  let avatarReads = 0;
  const mutations: string[] = [];
  const requests: MockAPIRequest[] = [];
  let currentUsername: string = player.username;
  let currentEmail = 'demo@example.test';
  let pendingEmail: string | null = null;
  let resendAvailableAt: string | null = null;
  let avatarExists = options.initialAvatar ?? false;
  let avatarIsVideo = false;
  let delaySessionPoll = false;
  let releaseStalePoll: (() => void) | null = null;
  let notifyStalePollStarted: (() => void) | null = null;
  let notifyStalePollFinished: (() => void) | null = null;
  let notifyPasswordRequestStarted: (() => void) | null = null;
  let releasePasswordResponse: (() => void) | null = null;
  const stalePollStarted = new Promise<void>((resolve) => {
    notifyStalePollStarted = resolve;
  });
  const stalePollFinished = new Promise<void>((resolve) => {
    notifyStalePollFinished = resolve;
  });
  const passwordRequestStarted = new Promise<void>((resolve) => {
    notifyPasswordRequestStarted = resolve;
  });
  const passwordResponseGate = new Promise<void>((resolve) => {
    releasePasswordResponse = resolve;
  });

  const accountSettings = () => ({
    username: currentUsername,
    email: currentEmail,
    pending_email: pendingEmail,
    pending_email_expires_at: pendingEmail ? new Date(Date.now() + 600_000).toISOString() : null,
    email_resend_available_at: resendAvailableAt,
  });

  let mockCSRFToken = 'mock-csrf-token';
  const csrfResponseHeaders = () => ({
    ...jsonHeaders,
    'set-cookie': `tpm_player_csrf=${mockCSRFToken}; Path=/; SameSite=Lax`,
    'x-csrf-token': mockCSRFToken,
  });

  if (cacheIdentity) {
    await page.addInitScript((identity) => {
      window.sessionStorage.setItem('player_id', identity.id);
      window.sessionStorage.setItem('username', identity.username);
    }, player);
  }

  await page.route('**/api/**', async (route) => {
    const request = route.request();
    const method = request.method().toUpperCase();
    const pathname = new URL(request.url()).pathname;

    if (method === 'GET' && pathname === '/api/v1/players/me') {
      const callIndex = meCalls;
      meCalls += 1;
      expect(request.headers().authorization).toBeUndefined();

      if (options.delayStalePollResponse && delaySessionPoll) {
        notifyStalePollStarted?.();
        await new Promise<void>((resolve) => {
          releaseStalePoll = resolve;
        });
        try {
          await route.fulfill({
            status: 401,
            headers: { 'content-type': 'application/problem+json' },
            body: JSON.stringify({
              code: 'player.invalid_session',
              title: 'Unauthorized',
              status: 401,
            }),
          });
        } catch {
          // The rotation may already have aborted this stale request.
        } finally {
          notifyStalePollFinished?.();
        }
        return;
      }

      const reply = replies[Math.min(callIndex, replies.length - 1)];

      if (reply.status === 200 && reply.player) {
        await route.fulfill({
          status: 200,
          headers: jsonHeaders,
          body: JSON.stringify({ player: reply.player }),
        });
        return;
      }

      await route.fulfill({
        status: reply.status,
        headers: { 'content-type': 'application/problem+json' },
        body: JSON.stringify({
          type: 'about:blank',
          title: reply.status === 401 ? 'Unauthorized' : 'Service Unavailable',
          status: reply.status,
        }),
      });
      return;
    }

    if (method === 'GET' && pathname === '/api/v1/players/account') {
      await route.fulfill({
        status: 200,
        headers: csrfResponseHeaders(),
        body: JSON.stringify(accountSettings()),
      });
      return;
    }

    if (method === 'GET' && pathname === '/api/v1/players/account/avatar') {
      avatarReads += 1;
      if (!avatarExists) {
        await route.fulfill({ status: 404, headers: { 'content-type': 'application/problem+json' } });
        return;
      }
      if (avatarIsVideo && options.avatarVideo) {
        await route.fulfill({ status: 200, headers: { 'content-type': 'video/mp4' }, body: options.avatarVideo });
        return;
      }
      await route.fulfill({ status: 200, headers: { 'content-type': 'image/gif' }, body: tinyGIF });
      return;
    }

    let body: unknown;
    if (method !== 'GET' && method !== 'HEAD' && method !== 'OPTIONS' && method !== 'TRACE') {
      try {
        body = request.postDataJSON();
      } catch {
        body = undefined;
      }
      mutations.push(`${method} ${pathname}`);
      requests.push({ method, path: pathname, body, csrfToken: request.headers()['x-csrf-token'] ?? null });
    }

    if (method === 'POST' && pathname === '/api/v1/players/account/username') {
      const requestBody = body as { username?: unknown } | undefined;
      if (typeof requestBody?.username === 'string') currentUsername = requestBody.username;
      await route.fulfill({
        status: 200,
        headers: jsonHeaders,
        body: JSON.stringify({ ...player, username: currentUsername }),
      });
      return;
    }

    if (method === 'POST' && pathname === '/api/v1/players/account/password') {
      notifyPasswordRequestStarted?.();
      if (options.delayPasswordResponse) await passwordResponseGate;
      mockCSRFToken = 'rotated-mock-csrf-token';
      await route.fulfill({ status: 204, headers: csrfResponseHeaders() });
      return;
    }

    if (method === 'POST' && pathname === '/api/v1/players/account/email') {
      const requestBody = body as { new_email?: unknown } | undefined;
      if (typeof requestBody?.new_email === 'string') pendingEmail = requestBody.new_email;
      resendAvailableAt = new Date(Date.now() + 60_000).toISOString();
      await route.fulfill({ status: 202, headers: csrfResponseHeaders(), body: JSON.stringify(accountSettings()) });
      return;
    }

    if (method === 'POST' && pathname === '/api/v1/players/account/email/resend') {
      resendAvailableAt = new Date(Date.now() + 60_000).toISOString();
      await route.fulfill({ status: 202, headers: csrfResponseHeaders(), body: JSON.stringify(accountSettings()) });
      return;
    }

    if (method === 'DELETE' && pathname === '/api/v1/players/account/email') {
      pendingEmail = null;
      resendAvailableAt = null;
      await route.fulfill({ status: 200, headers: csrfResponseHeaders(), body: JSON.stringify(accountSettings()) });
      return;
    }

    if (method === 'POST' && pathname === '/api/v1/players/account/email/confirm') {
      if (pendingEmail) currentEmail = pendingEmail;
      pendingEmail = null;
      resendAvailableAt = null;
      mockCSRFToken = 'rotated-mock-csrf-token';
      await route.fulfill({
        status: 200,
        headers: csrfResponseHeaders(),
        body: JSON.stringify({ email: currentEmail, previous_email_notified: true }),
      });
      return;
    }

    if (method === 'PUT' && pathname === '/api/v1/players/account/avatar') {
      const postData = request.postDataBuffer()?.toString('latin1') ?? '';
      const filename = /filename="([^"]+)"/i.exec(postData)?.[1] ?? '';
      const isVideo = filename.toLowerCase().endsWith('.mp4');
      const isCorruptImage = postData.includes('filename="corrupt.png"');
      const status = isCorruptImage ? 400 : options.avatarUploadStatus ?? 204;
      if (status === 204) {
        avatarExists = true;
        avatarIsVideo = isVideo;
      }
      await route.fulfill({
        status,
        headers: status === 204 ? {} : { 'content-type': 'application/problem+json' },
        body: status === 204 ? undefined : JSON.stringify({
          code: isCorruptImage
            ? 'player.avatar_invalid'
            : isVideo && status === 503
              ? 'player.avatar_video_unavailable'
              : undefined,
          title: isCorruptImage ? 'Invalid image' : 'Service Unavailable',
          status,
        }),
      });
      return;
    }

    if (method === 'DELETE' && pathname === '/api/v1/players/account/avatar') {
      const status = options.avatarDeleteStatus ?? 204;
      if (status === 204) avatarExists = false;
      await route.fulfill({
        status,
        headers: status === 204 ? {} : { 'content-type': 'application/problem+json' },
        body: status === 204 ? undefined : JSON.stringify({ title: 'Service Unavailable', status }),
      });
      return;
    }
    await route.abort('blockedbyclient');
  });

  return {
    get meCalls() {
      return meCalls;
    },
    get avatarReads() {
      return avatarReads;
    },
    stalePollStarted,
    stalePollFinished,
    passwordRequestStarted,
    delayNextSessionPoll: () => { delaySessionPoll = true; },
    releaseStalePoll: () => releaseStalePoll?.(),
    releasePasswordResponse: () => releasePasswordResponse?.(),
    mutations,
    requests,
  };
};

const openAuthenticatedSettings = async (page: Page, options: MockAPIOptions = {}) => {
  const api = await mockAccountAPI(page, [{ status: 200, player }], true, options);

  await page.goto('/settings');
  await expect(page.getByRole('heading', { name: 'Настройки', exact: true })).toBeVisible();
  await expect(page.getByText(player.username, { exact: true })).toBeVisible();
  await expect.poll(() => api.meCalls).toBeGreaterThan(0);
  return api;
};

const selectSection = async (page: Page, section: 'Профиль' | 'Безопасность' | 'Оформление') => {
  const button = page.getByRole('button', { name: section, exact: true });
  await button.click();
  await expect(button).toHaveAttribute('aria-pressed', 'true');
  await expect(page.getByRole('heading', { name: section, exact: true, level: 2 })).toBeVisible();
  return button;
};

const formSkipsNativeValidation = async (input: Locator): Promise<boolean> => input.evaluate((element) => (
  element instanceof HTMLInputElement && element.form?.noValidate === true
));

test('loads account data and switches between settings sections', async ({ page }) => {
  const api = await openAuthenticatedSettings(page);
  await expect(page.getByText('demo@example.test', { exact: true })).toBeVisible();

  for (const section of ['Профиль', 'Безопасность', 'Оформление'] as const) {
    await selectSection(page, section);
  }

  expect(api.mutations).toEqual([]);
});

test('saves supported avatar formats and removes the saved image', async ({ page }) => {
  const api = await openAuthenticatedSettings(page);
  const fileInput = page.getByLabel('Выбрать аватар', { exact: true });
  const fallback = page.getByRole('img', { name: 'Инициалы аватара', exact: true });
  const preview = page.getByRole('img', { name: 'Аватар профиля', exact: true });
  const deleteAvatar = page.getByRole('button', { name: 'Удалить', exact: true });
  const headerButton = page.getByRole('button', { name: 'Меню аккаунта', exact: true });
  const headerAvatar = headerButton.locator('img');
  await expect(fallback).toBeVisible();
  await expect(deleteAvatar).toHaveCount(0);
  await expect(fileInput).toBeEnabled();
  await expect(headerAvatar).toHaveCount(0);

  await fileInput.setInputFiles({ name: 'avatar.png', mimeType: 'image/png', buffer: tinyPNG });
  await expect(preview).toBeVisible();
  await expect(headerAvatar).toBeVisible();
  await expect.poll(() => headerAvatar.evaluate((element: HTMLImageElement) => element.complete && element.naturalWidth > 0)).toBe(true);
  const renderedAvatarBytes = await preview.evaluate(async (element) => {
    const image = element as HTMLImageElement;
    const blob = await fetch(image.src).then((response) => response.blob());
    return Array.from(new Uint8Array(await blob.arrayBuffer()).slice(0, 6));
  });
  expect(renderedAvatarBytes).toEqual(Array.from(tinyGIF.slice(0, 6)));
  await expect(deleteAvatar).toBeVisible();
  await deleteAvatar.click();
  await expect(preview).toHaveCount(0);
  await expect(fallback).toBeVisible();
  await expect(deleteAvatar).toHaveCount(0);

  await expect(headerAvatar).toHaveCount(0);
  await expect(headerButton.locator('svg')).toBeVisible();

  const jpegDataUrl = await page.evaluate(() => {
    const canvas = document.createElement('canvas');
    canvas.width = 1;
    canvas.height = 1;
    const context = canvas.getContext('2d');
    context?.fillRect(0, 0, 1, 1);
    return canvas.toDataURL('image/jpeg');
  });
  const jpegPrefix = 'data:image/jpeg;base64,';
  expect(jpegDataUrl.startsWith(jpegPrefix)).toBe(true);
  const tinyJPEG = Buffer.from(jpegDataUrl.slice(jpegPrefix.length), 'base64');
  await fileInput.setInputFiles({ name: 'avatar.jpg', mimeType: 'image/jpeg', buffer: tinyJPEG });
  await expect(preview).toBeVisible();
  await expect(deleteAvatar).toBeVisible();
  await deleteAvatar.click();
  await expect(preview).toHaveCount(0);
  await expect(fallback).toBeVisible();

  await fileInput.setInputFiles({ name: 'avatar.gif', mimeType: 'image/gif', buffer: tinyGIF });
  await expect(preview).toBeVisible();
  await expect(headerAvatar).toBeVisible();
  await expect(deleteAvatar).toBeVisible();
  await deleteAvatar.click();
  await expect(preview).toHaveCount(0);
  await expect(fallback).toBeVisible();
  await expect(headerAvatar).toHaveCount(0);
  expect(api.mutations).toEqual([
    'PUT /api/v1/players/account/avatar',
    'DELETE /api/v1/players/account/avatar',
    'PUT /api/v1/players/account/avatar',
    'DELETE /api/v1/players/account/avatar',
    'PUT /api/v1/players/account/avatar',
    'DELETE /api/v1/players/account/avatar',
  ]);
});

test('rejects unsupported and oversized avatar replacements without replacing the preview', async ({ page }) => {
  const api = await openAuthenticatedSettings(page);
  const fileInput = page.getByLabel('Выбрать аватар', { exact: true });
  const fallback = page.getByRole('img', { name: 'Инициалы аватара', exact: true });
  const preview = page.getByRole('img', { name: 'Аватар профиля', exact: true });
  const deleteAvatar = page.getByRole('button', { name: 'Удалить', exact: true });

  await expect(fallback).toBeVisible();
  await fileInput.setInputFiles({
    name: 'avatar.txt',
    mimeType: 'text/plain',
    buffer: Buffer.from('not an image'),
  });
  await expect(page.getByRole('alert').filter({ hasText: avatarTypeError }))
    .toHaveText(avatarTypeError);
  await expect(fallback).toBeVisible();
  await expect(preview).toHaveCount(0);

  await fileInput.setInputFiles({
    name: 'large-avatar.png',
    mimeType: 'image/png',
    buffer: Buffer.alloc(5 * 1024 * 1024 + 1),
  });
  await expect(page.getByRole('alert').filter({ hasText: avatarSizeError }))
    .toHaveText(avatarSizeError);
  await expect(fallback).toBeVisible();
  await expect(preview).toHaveCount(0);

  await fileInput.setInputFiles({ name: 'avatar.png', mimeType: 'image/png', buffer: tinyPNG });
  await expect(preview).toBeVisible();
  const previewSource = await preview.getAttribute('src');
  expect(previewSource).toBeTruthy();

  await fileInput.setInputFiles({
    name: 'corrupt.png',
    mimeType: 'image/png',
    buffer: Buffer.from('not an encoded PNG'),
  });
  await expect(page.getByRole('alert').filter({ hasText: avatarDecodeError }))
    .toHaveText(avatarDecodeError);
  await expect(preview).toBeVisible();
  await expect(preview).toHaveAttribute('src', previewSource!);
  await expect(fallback).toHaveCount(0);

  await fileInput.setInputFiles({
    name: 'avatar.png',
    mimeType: 'text/plain',
    buffer: Buffer.from('wrong media type'),
  });
  await expect(page.getByRole('alert').filter({ hasText: avatarTypeError }))
    .toHaveText(avatarTypeError);
  await expect(preview).toBeVisible();
  await expect(preview).toHaveAttribute('src', previewSource!);
  await expect(fallback).toHaveCount(0);

  await deleteAvatar.click();
  await expect(preview).toHaveCount(0);
  await expect(fallback).toBeVisible();
  await expect(deleteAvatar).toHaveCount(0);
  expect(api.mutations).toEqual([
    'PUT /api/v1/players/account/avatar',
    'PUT /api/v1/players/account/avatar',
    'DELETE /api/v1/players/account/avatar',
  ]);
});

test('keeps the selected avatar while switching settings sections', async ({ page }) => {
  const api = await openAuthenticatedSettings(page);
  const fileInput = page.getByLabel('Выбрать аватар', { exact: true });
  const fallback = page.getByRole('img', { name: 'Инициалы аватара', exact: true });
  const preview = page.getByRole('img', { name: 'Аватар профиля', exact: true });

  await fileInput.setInputFiles({ name: 'avatar.png', mimeType: 'image/png', buffer: tinyPNG });
  await expect(preview).toBeVisible();
  const previewSource = await preview.getAttribute('src');
  expect(previewSource).toBeTruthy();
  await selectSection(page, 'Безопасность');
  await selectSection(page, 'Профиль');
  await expect(preview).toBeVisible();
  await expect(preview).toHaveAttribute('src', previewSource!);
  await expect(fallback).toHaveCount(0);
  await page.getByRole('button', { name: 'Удалить', exact: true }).click();
  await expect(preview).toHaveCount(0);
  await expect(fallback).toBeVisible();
  await expect(page.getByRole('button', { name: 'Удалить', exact: true })).toHaveCount(0);
  expect(api.mutations).toEqual([
    'PUT /api/v1/players/account/avatar',
    'DELETE /api/v1/players/account/avatar',
  ]);
});

test('loads the saved avatar and preserves it when replacement or removal fails', async ({ page }) => {
  const api = await openAuthenticatedSettings(page, {
    initialAvatar: true,
    avatarUploadStatus: 503,
    avatarDeleteStatus: 503,
  });
  const fileInput = page.getByLabel('Выбрать аватар', { exact: true });
  const preview = page.getByRole('img', { name: 'Аватар профиля', exact: true });

  await expect(preview).toBeVisible();
  const savedSource = await preview.getAttribute('src');
  expect(savedSource).toBeTruthy();
  await fileInput.setInputFiles({ name: 'replacement.png', mimeType: 'image/png', buffer: tinyPNG });
  await expect(page.getByRole('alert').filter({ hasText: 'Не удалось загрузить фото профиля. Попробуйте позже.' }))
    .toBeVisible();
  await expect(preview).toHaveAttribute('src', savedSource!);

  await page.getByRole('button', { name: 'Удалить', exact: true }).click();
  await expect(page.getByRole('alert').filter({ hasText: 'Не удалось загрузить фото профиля. Попробуйте позже.' }))
    .toBeVisible();
  await expect(preview).toHaveAttribute('src', savedSource!);
  expect(api.mutations).toEqual([
    'PUT /api/v1/players/account/avatar',
    'DELETE /api/v1/players/account/avatar',
  ]);
});

test('keeps the saved avatar when MP4 processing is unavailable', async ({ page }) => {
  const avatarVideo = await readFile(resolve(
    process.cwd(),
    '../backend/internal/adapter/outbound/media/ffmpeg/testdata/avatar.mp4',
  ));
  const api = await openAuthenticatedSettings(page, {
    initialAvatar: true,
    avatarUploadStatus: 503,
  });
  const preview = page.getByRole('img', { name: 'Аватар профиля', exact: true });
  const savedSource = await preview.getAttribute('src');
  expect(savedSource).toBeTruthy();

  await page.getByLabel('Выбрать аватар', { exact: true }).setInputFiles({
    name: 'replacement.mp4',
    mimeType: 'video/mp4',
    buffer: avatarVideo,
  });
  await expect(page.getByRole('alert').filter({ hasText: avatarVideoUnavailableError }))
    .toHaveText(avatarVideoUnavailableError);
  await expect(preview).toHaveAttribute('src', savedSource!);
  expect(api.mutations).toEqual(['PUT /api/v1/players/account/avatar']);
});

test('accepts an MP4 avatar and respects reduced motion and the media CSP', async ({ page }) => {
  const avatarVideo = await readFile(resolve(
    process.cwd(),
    '../backend/internal/adapter/outbound/media/ffmpeg/testdata/avatar.mp4',
  ));
  await page.emulateMedia({ reducedMotion: 'reduce' });
  const api = await openAuthenticatedSettings(page, { avatarVideo });
  const supportsH264 = await page.evaluate(() => {
    const probe = document.createElement('video');
    return probe.canPlayType('video/mp4; codecs="avc1.42E01E"') !== '';
  });
  await page.evaluate(() => {
    const target = window as Window & { __mediaPolicyViolations?: string[] };
    target.__mediaPolicyViolations = [];
    window.addEventListener('securitypolicyviolation', (event) => {
      if (event.effectiveDirective === 'media-src') target.__mediaPolicyViolations?.push(event.blockedURI);
    });
  });

  const fileInput = page.getByLabel('Выбрать аватар', { exact: true });
  await fileInput.setInputFiles({ name: 'avatar.mp4', mimeType: 'video/mp4', buffer: avatarVideo });
  const video = page.getByLabel('Видео аватара', { exact: true });
  const playbackError = page.getByRole('alert').filter({ hasText: avatarPlaybackError });
  await expect.poll(async () => (await video.count()) === 1 || (await playbackError.count()) === 1).toBe(true);

  if (supportsH264) {
    const headerVideo = page.getByRole('button', { name: 'Меню аккаунта', exact: true }).locator('video');
    await expect(headerVideo).toBeVisible();
    await expect.poll(() => headerVideo.evaluate((element: HTMLVideoElement) => element.readyState >= 2)).toBe(true);
    expect(await headerVideo.evaluate((element: HTMLVideoElement) => ({ controls: element.controls, muted: element.muted, paused: element.paused })))
      .toEqual({ controls: false, muted: true, paused: true });
    await expect(video).toBeVisible();
    expect(await video.evaluate((element) => {
      const media = element as HTMLVideoElement;
      return { muted: media.muted, playsInline: media.playsInline, loop: media.loop, autoplay: media.autoplay };
    })).toEqual({ muted: true, playsInline: true, loop: false, autoplay: false });
    await expect.poll(() => video.evaluate((element) => (element as HTMLVideoElement).readyState >= 2)).toBe(true);
    await expect.poll(() => video.evaluate((element) => (element as HTMLVideoElement).paused)).toBe(true);
    await expect(page.getByRole('button', { name: /видео аватара/i })).toHaveCount(0);
    await page.emulateMedia({ reducedMotion: 'no-preference' });
    await expect.poll(() => video.evaluate((element) => {
      const media = element as HTMLVideoElement;
      return { loop: media.loop, autoplay: media.autoplay };
    })).toEqual({ loop: true, autoplay: true });
    await expect.poll(() => video.evaluate((element) => !(element as HTMLVideoElement).paused)).toBe(true);
    await expect.poll(() => video.evaluate((element) => (element as HTMLVideoElement).currentTime)).toBeGreaterThan(0);
    await expect.poll(() => headerVideo.evaluate((element: HTMLVideoElement) => !element.paused)).toBe(true);
    await selectSection(page, 'Безопасность');
    await expect.poll(() => video.evaluate((element) => (element as HTMLVideoElement).paused)).toBe(true);
    await selectSection(page, 'Профиль');
    await expect.poll(() => video.evaluate((element) => !(element as HTMLVideoElement).paused)).toBe(true);
    await page.emulateMedia({ reducedMotion: 'reduce' });
    await expect.poll(() => headerVideo.evaluate((element: HTMLVideoElement) => element.paused)).toBe(true);
    if (process.env.E2E_AVATAR_SCREENSHOT_PATH) {
      await page.screenshot({ path: process.env.E2E_AVATAR_SCREENSHOT_PATH, fullPage: true });
    }
  } else {
    await expect(playbackError).toBeVisible();
    await expect(video).toHaveCount(0);
  }

  expect(await page.evaluate(() => (
    (window as Window & { __mediaPolicyViolations?: string[] }).__mediaPolicyViolations ?? []
  ))).toEqual([]);
  expect(api.mutations).toEqual(['PUT /api/v1/players/account/avatar']);
});

test('opens, validates, and cancels the login editor', async ({ page }) => {
  const api = await openAuthenticatedSettings(page);
  const editLogin = page.getByRole('button', { name: 'Изменить логин', exact: true });
  await expect(editLogin).toHaveAttribute('aria-expanded', 'false');

  await editLogin.click();
  const closeLoginEditor = page.getByRole('button', { name: 'Свернуть форму изменения логина', exact: true });
  const loginInput = page.getByLabel('Новый логин', { exact: true });
  const currentPassword = page.getByLabel('Текущий пароль', { exact: true });
  await expect(closeLoginEditor).toHaveAttribute('aria-expanded', 'true');
  await expect(closeLoginEditor).toHaveAttribute('aria-controls', /.+/);
  await expect(loginInput).toBeFocused();
  expect(await formSkipsNativeValidation(loginInput)).toBe(true);

  await loginInput.fill('x');
  await loginInput.blur();
  await expect(loginInput).toHaveAttribute('aria-invalid', 'true');
  await expect(page.getByText(usernameError, { exact: true })).toBeVisible();
  const saveLogin = page.getByRole('button', { name: 'Изменить логин', exact: true });
  await expect(saveLogin).toBeEnabled();
  await saveLogin.click();
  await expect(loginInput).toBeFocused();

  await loginInput.fill('draft_login');
  await expect(page.getByText(usernameError, { exact: true })).toHaveCount(0);
  await currentPassword.focus();
  await currentPassword.blur();
  await expect(currentPassword).toHaveAttribute('aria-invalid', 'true');
  await expect(page.getByText('Введите текущий пароль.', { exact: true })).toBeVisible();
  await currentPassword.fill('Synthetic-current-9284!');
  await expect(page.getByText('Введите текущий пароль.', { exact: true })).toHaveCount(0);
  await page.getByRole('button', { name: 'Отмена', exact: true }).click();
  await expect(editLogin).toHaveAttribute('aria-expanded', 'false');
  await expect(editLogin).toBeFocused();
  await expect(page.getByLabel('Новый логин', { exact: true })).toHaveCount(0);
  await editLogin.click();
  await expect(page.getByLabel('Новый логин', { exact: true })).toHaveValue(player.username);
  await expect(page.getByLabel('Текущий пароль', { exact: true })).toHaveValue('');
  await page.getByRole('button', { name: 'Отмена', exact: true }).click();
  expect(api.mutations).toEqual([]);
});

test('changes username and password with current-password checks without storing credentials', async ({ page }) => {
  const api = await openAuthenticatedSettings(page);
  await page.getByRole('button', { name: 'Изменить логин', exact: true }).click();
  await page.getByLabel('Новый логин', { exact: true }).fill('renamed_player');
  await page.getByLabel('Текущий пароль', { exact: true }).fill('Current-login-pass-9284!');
  const usernameResponse = page.waitForResponse((response) => (
    new URL(response.url()).pathname === '/api/v1/players/account/username'
      && response.request().method() === 'POST'
  ));
  await page.getByRole('button', { name: 'Изменить логин', exact: true }).click();
  expect((await usernameResponse).status()).toBe(200);
  await expect(page.getByText('renamed_player', { exact: true })).toBeVisible();

  await selectSection(page, 'Безопасность');
  await page.getByRole('button', { name: 'Изменить пароль', exact: true }).click();
  await page.getByLabel('Текущий пароль', { exact: true }).fill('Current-password-9284!');
  await page.getByLabel('Новый пароль', { exact: true }).fill('New-secure-9284!');
  await page.getByLabel('Повторите новый пароль', { exact: true }).fill('New-secure-9284!');
  const passwordResponse = page.waitForResponse((response) => (
    new URL(response.url()).pathname === '/api/v1/players/account/password'
      && response.request().method() === 'POST'
  ));
  await page.getByRole('button', { name: 'Изменить пароль', exact: true }).click();
  expect((await passwordResponse).status()).toBe(204);
  await expect(page.getByRole('status').getByText('Пароль изменен.', { exact: true })).toBeVisible();
  await expect(page.getByLabel('Текущий пароль', { exact: true })).toHaveCount(0);

  expect(api.requests.map(({ method, path, body }) => ({ method, path, body }))).toEqual([
    {
      method: 'POST',
      path: '/api/v1/players/account/username',
      body: { current_password: 'Current-login-pass-9284!', username: 'renamed_player' },
    },
    {
      method: 'POST',
      path: '/api/v1/players/account/password',
      body: { current_password: 'Current-password-9284!', new_password: 'New-secure-9284!' },
    },
  ]);
  expect(api.requests.every((request) => request.csrfToken === 'mock-csrf-token')).toBe(true);
  const storage = await page.evaluate(() => JSON.stringify({
    local: Object.entries(window.localStorage),
    session: Object.entries(window.sessionStorage),
  }));
  expect(storage).not.toContain('Current-login-pass-9284!');
  expect(storage).not.toContain('Current-password-9284!');
  expect(storage).not.toContain('New-secure-9284!');
});

test('fences stale session checks during password rotation and uses the replacement CSRF token', async ({ page }) => {
  const api = await openAuthenticatedSettings(page, {
    delayStalePollResponse: true,
    delayPasswordResponse: true,
  });

  await expect(page.getByRole('button', { name: 'Меню аккаунта', exact: true })).toBeVisible();
  const initialSessionChecks = api.meCalls;
  api.delayNextSessionPoll();
  await page.evaluate(() => window.dispatchEvent(new Event('focus')));
  await api.stalePollStarted;
  expect(api.meCalls).toBe(initialSessionChecks + 1);

  await selectSection(page, 'Безопасность');
  await page.getByRole('button', { name: 'Изменить пароль', exact: true }).click();
  await page.getByLabel('Текущий пароль', { exact: true }).fill('Current-password-9284!');
  await page.getByLabel('Новый пароль', { exact: true }).fill('New-secure-9284!');
  await page.getByLabel('Повторите новый пароль', { exact: true }).fill('New-secure-9284!');

  const passwordResponsePromise = page.waitForResponse((response) => (
    new URL(response.url()).pathname === '/api/v1/players/account/password'
      && response.request().method() === 'POST'
  ));
  await page.getByRole('button', { name: 'Изменить пароль', exact: true }).click();
  await api.passwordRequestStarted;

  await page.evaluate(() => window.dispatchEvent(new Event('focus')));
  await expect.poll(() => api.meCalls).toBe(initialSessionChecks + 1);
  api.releaseStalePoll();
  await api.stalePollFinished;
  api.releasePasswordResponse();
  const passwordResponse = await passwordResponsePromise;
  expect(passwordResponse.status()).toBe(204);
  await expect(page.getByRole('status').getByText('Пароль изменен.', { exact: true })).toBeVisible();
  await expect(page.getByRole('dialog', { name: 'Аккаунт удален' })).toHaveCount(0);
  await selectSection(page, 'Профиль');
  await expect(page.getByText('demo', { exact: true })).toBeVisible();

  await page.getByRole('button', { name: 'Изменить email', exact: true }).click();
  await page.getByLabel('Новый email', { exact: true }).fill('rotated@example.test');
  await page.getByLabel('Текущий пароль', { exact: true }).fill('New-secure-9284!');
  const emailResponsePromise = page.waitForResponse((response) => (
    new URL(response.url()).pathname === '/api/v1/players/account/email'
      && response.request().method() === 'POST'
  ));
  await page.getByRole('button', { name: 'Продолжить', exact: true }).click();
  const emailResponse = await emailResponsePromise;
  expect(emailResponse.status()).toBe(202);

  const requests = api.requests;
  expect(requests.find((request) => request.path === '/api/v1/players/account/email')?.csrfToken)
    .toBe('rotated-mock-csrf-token');
  expect(api.meCalls).toBe(initialSessionChecks + 1);
});

test('changes email through the pending code flow and submits only current-account requests', async ({ page }) => {
  const api = await openAuthenticatedSettings(page);
  const editEmail = page.getByRole('button', { name: 'Изменить email', exact: true });
  await editEmail.click();

  const emailInput = page.getByLabel('Новый email', { exact: true });
  const currentPassword = page.getByLabel('Текущий пароль', { exact: true });
  await expect(editEmail).toHaveAttribute('aria-expanded', 'true');
  await expect(editEmail).toHaveAttribute('aria-controls', /.+/);
  await expect(emailInput).toBeFocused();
  expect(await formSkipsNativeValidation(emailInput)).toBe(true);

  await emailInput.fill('bad@localhost');
  await emailInput.blur();
  await expect(emailInput).toHaveAttribute('aria-invalid', 'true');
  await expect(page.getByText(emailError, { exact: true })).toBeVisible();
  const continueButton = page.getByRole('button', { name: 'Продолжить', exact: true });
  await continueButton.click();
  await expect(emailInput).toBeFocused();
  await expect(currentPassword).toHaveAttribute('aria-invalid', 'true');

  await emailInput.fill('new-address@example.test');
  await currentPassword.fill('Synthetic-email-current-9284!');
  const firstBeginResponse = page.waitForResponse((response) => (
    new URL(response.url()).pathname === '/api/v1/players/account/email'
      && response.request().method() === 'POST'
  ));
  await continueButton.click();
  expect((await firstBeginResponse).status()).toBe(202);
  const codeInput = page.getByLabel('Код подтверждения', { exact: true });
  await expect(codeInput).toBeFocused();
  await expect(codeInput).toHaveAttribute('autocomplete', 'one-time-code');
  await expect(codeInput).toHaveAttribute('inputmode', 'numeric');
  await expect(codeInput).toHaveAttribute('maxlength', '6');
  await expect(codeInput).toHaveAttribute('pattern', '[0-9]{6}');
  await expect(page.getByText('new-address@example.test', { exact: true })).toBeVisible();
  await expect(page.getByText('Код действует 10 минут.', { exact: true })).toBeVisible();
  await expect(page.getByLabel('Текущий пароль', { exact: true })).toHaveCount(0);
  await expect(page.getByText('Код отправлен на новый адрес.', { exact: true })).toBeVisible();
  const confirmEmail = page.getByRole('button', { name: 'Подтвердить почту', exact: true });
  const resendCode = page.getByRole('button', { name: 'Отправить код повторно', exact: true });
  await expect(confirmEmail).toBeEnabled();
  await expect(resendCode).toBeDisabled();
  expect(await formSkipsNativeValidation(codeInput)).toBe(true);

  await codeInput.fill('12x345');
  await expect(codeInput).toHaveValue('12345');
  await codeInput.blur();
  await expect(codeInput).toHaveAttribute('aria-invalid', 'true');
  await expect(page.getByText(emailCodeLengthError, { exact: true })).toBeVisible();

  await codeInput.focus();
  await codeInput.evaluate((element) => {
    if (!(element instanceof HTMLInputElement)) throw new Error('Expected a code input');
    const clipboardData = new DataTransfer();
    clipboardData.setData('text/plain', '012 345');
    element.dispatchEvent(new ClipboardEvent('paste', {
      bubbles: true,
      cancelable: true,
      clipboardData,
    }));
  });
  await expect(codeInput).toHaveValue('012345');
  await expect(codeInput).not.toHaveAttribute('aria-invalid', 'true');
  await expect(confirmEmail).toBeEnabled();

  const cancelPendingResponse = page.waitForResponse((response) => (
    new URL(response.url()).pathname === '/api/v1/players/account/email'
      && response.request().method() === 'DELETE'
  ));
  await page.getByRole('button', { name: 'Изменить адрес', exact: true }).click();
  expect((await cancelPendingResponse).status()).toBe(200);
  await expect(emailInput).toBeFocused();
  await expect(emailInput).toHaveValue('new-address@example.test');
  await expect(currentPassword).toHaveValue('');
  await expect(page.getByLabel('Код подтверждения', { exact: true })).toHaveCount(0);

  await emailInput.fill('second-address@example.test');
  await currentPassword.fill('Synthetic-email-current-9284!');
  const secondBeginResponse = page.waitForResponse((response) => (
    new URL(response.url()).pathname === '/api/v1/players/account/email'
      && response.request().method() === 'POST'
  ));
  await continueButton.click();
  expect((await secondBeginResponse).status()).toBe(202);
  const secondCode = page.getByLabel('Код подтверждения', { exact: true });
  await expect(secondCode).toBeFocused();
  await secondCode.fill('001234');
  const confirmResponse = page.waitForResponse((response) => (
    new URL(response.url()).pathname === '/api/v1/players/account/email/confirm'
      && response.request().method() === 'POST'
  ));
  await confirmEmail.click();
  expect((await confirmResponse).status()).toBe(200);
  await expect(page.getByText('second-address@example.test', { exact: true })).toBeVisible();
  await expect(page.getByRole('status').getByText('Email обновлен.', { exact: true })).toBeVisible();
  await expect(editEmail).toHaveAttribute('aria-expanded', 'false');

  expect(api.requests.map(({ method, path, body }) => ({ method, path, body }))).toEqual([
    {
      method: 'POST',
      path: '/api/v1/players/account/email',
      body: { current_password: 'Synthetic-email-current-9284!', new_email: 'new-address@example.test' },
    },
    { method: 'DELETE', path: '/api/v1/players/account/email', body: null },
    {
      method: 'POST',
      path: '/api/v1/players/account/email',
      body: { current_password: 'Synthetic-email-current-9284!', new_email: 'second-address@example.test' },
    },
    {
      method: 'POST',
      path: '/api/v1/players/account/email/confirm',
      body: { code: '001234' },
    },
  ]);
  expect(api.requests.every((request) => request.csrfToken === 'mock-csrf-token')).toBe(true);
  const emailStorage = await page.evaluate(() => JSON.stringify({
    local: Object.entries(window.localStorage),
    session: Object.entries(window.sessionStorage),
  }));
  expect(emailStorage).not.toContain('Synthetic-email-current-9284!');
  expect(emailStorage).not.toContain('001234');
  expect(emailStorage).not.toContain('second-address@example.test');
});

test('keeps the password editor collapsed until requested and validates before cancel', async ({ page }) => {
  const api = await openAuthenticatedSettings(page);
  await selectSection(page, 'Безопасность');
  const editPassword = page.getByRole('button', { name: 'Изменить пароль', exact: true });
  await expect(editPassword).toHaveAttribute('aria-expanded', 'false');
  await expect(page.getByLabel('Текущий пароль', { exact: true })).toHaveCount(0);

  await editPassword.click();
  const closePasswordEditor = page.getByRole('button', {
    name: 'Свернуть форму изменения пароля',
    exact: true,
  });
  const currentPassword = page.getByLabel('Текущий пароль', { exact: true });
  const newPassword = page.getByLabel('Новый пароль', { exact: true });
  const repeatPassword = page.getByLabel('Повторите новый пароль', { exact: true });
  await expect(closePasswordEditor).toHaveAttribute('aria-expanded', 'true');
  await expect(closePasswordEditor).toHaveAttribute('aria-controls', /.+/);
  await expect(currentPassword).toBeFocused();
  expect(await formSkipsNativeValidation(currentPassword)).toBe(true);
  for (const passwordInput of [currentPassword, newPassword, repeatPassword]) {
    await expect(passwordInput).toHaveAttribute('spellcheck', 'false');
  }

  await expect(currentPassword).toHaveValue('');
  await expect(currentPassword).toBeFocused();
  await page.getByRole('button', { name: 'Отмена', exact: true }).click();
  await expect(editPassword).toHaveAttribute('aria-expanded', 'false');
  await expect(editPassword).toBeFocused();

  await editPassword.click();
  await newPassword.fill('short');
  await newPassword.blur();
  await expect(page.getByText(passwordLengthError, { exact: true })).toBeVisible();
  await repeatPassword.fill('not-the-same');
  await repeatPassword.blur();
  await expect(page.getByText(passwordMismatchError, { exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Изменить пароль', exact: true })).toBeEnabled();
  await expect(page.getByRole('button', { name: 'Настроить', exact: true })).toBeDisabled();
  await expect(page.getByRole('button', { name: 'Управлять', exact: true })).toBeDisabled();

  await page.getByRole('button', { name: 'Отмена', exact: true }).click();
  await expect(editPassword).toHaveAttribute('aria-expanded', 'false');
  await expect(editPassword).toBeFocused();
  expect(api.mutations).toEqual([]);
});

test('clears editor drafts and password fields when switching settings sections', async ({ page }) => {
  const api = await openAuthenticatedSettings(page);

  const editLogin = page.getByRole('button', { name: 'Изменить логин', exact: true });
  await editLogin.click();
  await page.getByLabel('Новый логин', { exact: true }).fill('draft_login');
  await selectSection(page, 'Безопасность');
  await selectSection(page, 'Профиль');
  await expect(page.getByLabel('Новый логин', { exact: true })).toHaveCount(0);
  await expect(page.getByText(player.username, { exact: true })).toBeVisible();

  await selectSection(page, 'Безопасность');
  const editPassword = page.getByRole('button', { name: 'Изменить пароль', exact: true });
  await editPassword.click();
  await page.getByLabel('Текущий пароль', { exact: true }).fill('Synthetic-current-9284!');
  await page.getByLabel('Новый пароль', { exact: true }).fill('Synthetic-next-9284!');
  await page.getByLabel('Повторите новый пароль', { exact: true }).fill('Synthetic-next-9284!');
  const passwordStorage = await page.evaluate(() => JSON.stringify({
    local: Object.entries(window.localStorage),
    session: Object.entries(window.sessionStorage),
  }));
  expect(passwordStorage).not.toContain('Synthetic-current-9284!');
  expect(passwordStorage).not.toContain('Synthetic-next-9284!');
  await selectSection(page, 'Оформление');
  await selectSection(page, 'Безопасность');
  await expect(editPassword).toHaveAttribute('aria-expanded', 'false');
  await editPassword.click();
  await expect(page.getByLabel('Текущий пароль', { exact: true })).toHaveValue('');
  await expect(page.getByLabel('Новый пароль', { exact: true })).toHaveValue('');
  await expect(page.getByLabel('Повторите новый пароль', { exact: true })).toHaveValue('');
  await page.getByRole('button', { name: 'Отмена', exact: true }).click();

  await selectSection(page, 'Профиль');
  await page.getByRole('button', { name: 'Изменить email', exact: true }).click();
  await page.getByLabel('Новый email', { exact: true }).fill('draft-address@example.test');
  await page.getByLabel('Текущий пароль', { exact: true }).fill('Synthetic-email-current-9284!');
  await page.getByRole('button', { name: 'Продолжить', exact: true }).click();
  const emailCode = page.getByLabel('Код подтверждения', { exact: true });
  await emailCode.fill('001234');
  const emailStorage = await page.evaluate(() => JSON.stringify({
    local: Object.entries(window.localStorage),
    session: Object.entries(window.sessionStorage),
  }));
  expect(emailStorage).not.toContain('Synthetic-email-current-9284!');
  expect(emailStorage).not.toContain('001234');
  expect(emailStorage).not.toContain('draft-address@example.test');
  const cancelPendingResponse = page.waitForResponse((response) => (
    new URL(response.url()).pathname === '/api/v1/players/account/email'
      && response.request().method() === 'DELETE'
  ));
  await page.getByRole('button', { name: 'Изменить адрес', exact: true }).click();
  expect((await cancelPendingResponse).status()).toBe(200);
  await selectSection(page, 'Безопасность');
  await selectSection(page, 'Профиль');
  await expect(page.getByLabel('Код подтверждения', { exact: true })).toHaveCount(0);
  await page.getByRole('button', { name: 'Изменить email', exact: true }).click();
  await expect(page.getByLabel('Новый email', { exact: true })).toHaveValue('');
  await expect(page.getByLabel('Текущий пароль', { exact: true })).toHaveValue('');
  await page.getByRole('button', { name: 'Отмена', exact: true }).click();

  const secretStorageKeys = await page.evaluate(() => [
    ...Object.keys(window.localStorage),
    ...Object.keys(window.sessionStorage),
  ].filter((key) => /password|secret|credential/i.test(key)));
  expect(secretStorageKeys).toEqual([]);
  expect(api.mutations).toEqual([
    'POST /api/v1/players/account/email',
    'DELETE /api/v1/players/account/email',
  ]);
});

test('lets a guest use appearance settings without sending account changes', async ({ page }) => {
  await page.setViewportSize({ width: 320, height: 844 });
  const api = await mockAccountAPI(page, [{ status: 401 }], false);

  await page.goto('/settings');

  await expect(page.getByRole('heading', { name: 'Настройки', exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Изменить логин', exact: true })).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Изменить email', exact: true })).toHaveCount(0);
  await expect(page.locator('main').getByRole('link', { name: 'Войти', exact: true }))
    .toHaveAttribute('href', '/login');
  await selectSection(page, 'Безопасность');
  await expect(page.getByRole('button', { name: 'Изменить пароль', exact: true })).toHaveCount(0);
  await page.getByRole('button', { name: 'Оформление', exact: true }).click();
  await expect(page.getByRole('button', { name: 'Русский', exact: true })).toBeDisabled();

  const main = page.locator('main');
  const lightTheme = main.getByRole('button', { name: 'Светлая тема', exact: true });
  await expect(lightTheme).toBeVisible();
  await lightTheme.click();
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'light');
  await expect(lightTheme).toHaveAttribute('aria-pressed', 'true');
  await expect.poll(() => page.evaluate(() => window.localStorage.getItem('task-per-minute-theme')))
    .toBe('light');

  await expect(page.getByRole('button', { name: 'Меню аккаунта', exact: true })).toHaveCount(0);
  const headerTheme = page.getByRole('switch', { name: 'Светлая тема', exact: true });
  await expect(headerTheme).toHaveAttribute('aria-checked', 'true');
  await headerTheme.click();
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');
  await expect(headerTheme).toHaveAttribute('aria-checked', 'false');
  await expect(lightTheme).toHaveAttribute('aria-pressed', 'false');
  await expect.poll(() => page.evaluate(() => window.localStorage.getItem('task-per-minute-theme')))
    .toBe('dark');
  await page.keyboard.press('Escape');

  await page.reload();
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');
  await page.getByRole('button', { name: 'Оформление', exact: true }).click();
  const restoredDarkTheme = page.locator('main').getByRole('button', { name: 'Темная тема', exact: true });
  await expect(restoredDarkTheme).toHaveAttribute('aria-pressed', 'true');
  await expect(page.getByRole('switch', { name: 'Светлая тема', exact: true }))
    .toHaveAttribute('aria-checked', 'false');

  const widths = await page.evaluate(() => ({
    body: document.body.scrollWidth,
    document: document.documentElement.scrollWidth,
    viewport: window.innerWidth,
  }));
  expect(widths.document).toBeLessThanOrEqual(widths.viewport);
  expect(widths.body).toBeLessThanOrEqual(widths.viewport);
  await expect.poll(() => api.meCalls).toBeGreaterThan(0);
  expect(api.mutations).toEqual([]);
});

test('can retry account restoration after a temporary failure', async ({ page }) => {
  const api = await mockAccountAPI(page, [
    { status: 200, player },
  ], false);
  let unavailable = true;
  await page.route('**/api/v1/players/me', async (route) => {
    if (unavailable) {
      await route.fulfill({ status: 503, json: { title: 'Service Unavailable', status: 503 } });
    } else {
      await route.fallback();
    }
  });

  await page.goto('/settings');

  const alert = page.locator('main').getByRole('alert');
  await expect(alert).toContainText('Не удалось проверить сессию. Попробуйте еще раз.');
  unavailable = false;
  await alert.getByRole('button', { name: 'Повторить', exact: true }).click();
  await expect(page.getByText(player.username, { exact: true })).toBeVisible();
  await expect.poll(() => api.meCalls).toBeGreaterThanOrEqual(1);
  expect(api.mutations).toEqual([]);
});
