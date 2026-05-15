import { expect, test, type Page, type WebSocketRoute } from '@playwright/test';
import { inSecondsISO, jsonHeaders, mockPlayerLogout, nowISO } from './support/common';
import {
  installWebSocketErrorProbe,
  type WebSocketErrorProbeWindow,
} from './support/browser';

const mockCurrentPlayerMe = async (
  page: Page,
  playerID: string,
  sessionToken: string,
  activeDuelID?: string,
): Promise<void> => {
  await page.route('**/api/v1/players/me', async (route) => {
    expect(route.request().headers()['x-session-token']).toBeUndefined();
    await route.fulfill({
      status: 200,
      headers: jsonHeaders,
      body: JSON.stringify({
        player: {
          id: playerID,
          username: 'alice',
          status: 'idle',
          created_at: nowISO(),
        },
        ...(activeDuelID
          ? {
              active_duel: {
                id: activeDuelID,
                status: 'active',
                deadline: new Date(Date.now() + 120_000).toISOString(),
                started_at: nowISO(),
              },
            }
          : {}),
      }),
    });
  });
};

test.beforeEach(async ({ page }) => {
  await mockPlayerLogout(page);
});

test('active task restore opens a single websocket after session preflight', async ({ page }) => {
  const playerID = '60606060-6060-6060-6060-606060606060';
  const sessionToken = '10000000-0000-0000-0000-000000000030';
  const duelID = '61616161-6161-6161-6161-616161616161';
  const task = {
    id: '62626262-6262-6262-6262-626262626262',
    title: 'Single Socket Restore',
    description: 'Restoring this task should not reconnect after setGameData.',
    category: 'web',
    difficulty: 'easy',
    time_limit: 120,
    time_limit_seconds: 120,
    task_url: 'https://example.com/task',
    hint_schedule: [],
  };
  let websocketOpenCount = 0;
  const messages: Array<{ type: string; payload?: unknown }> = [];

  await page.addInitScript(({ playerID, sessionToken, duelID, task }) => {
    window.sessionStorage.setItem('player_id', playerID);
    window.sessionStorage.setItem('username', 'alice');
    window.sessionStorage.setItem('currentGame', JSON.stringify({
      duel_id: duelID,
      deadline: new Date(Date.now() + 120_000).toISOString(),
      time_limit_seconds: 120,
      task,
    }));
  }, { playerID, sessionToken, duelID, task });

  await mockCurrentPlayerMe(page, playerID, sessionToken, duelID);
  await page.routeWebSocket((url) => url.pathname === '/ws', (ws) => {
    websocketOpenCount += 1;
    ws.onMessage((raw) => {
      messages.push(JSON.parse(String(raw)) as { type: string; payload?: unknown });
    });
  });

  await page.goto('/task');

  await expect(page.getByRole('heading', { name: 'Single Socket Restore' })).toBeVisible();
  await expect.poll(() => websocketOpenCount).toBe(1);
  await expect.poll(() => messages.some((message) => message.type === 'ping')).toBe(true);
  await page.waitForTimeout(300);
  expect(websocketOpenCount).toBe(1);
});

test('stale stored task is cleared when session has no active duel after retry', async ({ page }) => {
  const playerID = 'b1b1b1b1-b1b1-b1b1-b1b1-b1b1b1b1b1b1';
  const sessionToken = '10000000-0000-0000-0000-000000000133';
  const duelID = 'b2b2b2b2-b2b2-b2b2-b2b2-b2b2b2b2b2b2';
  const task = {
    id: 'b3b3b3b3-b3b3-b3b3-b3b3-b3b3b3b3b3b3',
    title: 'No Active Duel Stale Task',
    description: 'Backend idle state should win over stale local task.',
    category: 'web',
    difficulty: 'easy',
    time_limit: 120,
    time_limit_seconds: 120,
    task_url: 'https://example.com/task',
    hint_schedule: [],
  };
  let meCalls = 0;
  let websocketOpened = false;

  await page.addInitScript(({ playerID, sessionToken, duelID, task }) => {
    window.sessionStorage.setItem('player_id', playerID);
    window.sessionStorage.setItem('username', 'alice');
    window.sessionStorage.setItem('currentGame', JSON.stringify({
      duel_id: duelID,
      deadline: new Date(Date.now() + 120_000).toISOString(),
      time_limit_seconds: 120,
      task,
    }));
  }, { playerID, sessionToken, duelID, task });

  await page.route('**/api/v1/players/me', async (route) => {
    meCalls += 1;
    expect(route.request().headers()['x-session-token']).toBeUndefined();
    await route.fulfill({
      status: 200,
      headers: jsonHeaders,
      body: JSON.stringify({
        player: {
          id: playerID,
          username: 'alice',
          status: 'idle',
          created_at: nowISO(),
        },
      }),
    });
  });
  await page.routeWebSocket((url) => url.pathname === '/ws', () => {
    websocketOpened = true;
  });

  await page.goto('/task');

  await expect(page).toHaveURL(/\/$/);
  await expect(page.getByText('Активная дуэль не найдена. Возвращаемся на главную.')).toBeVisible();
  await expect.poll(() => meCalls).toBeGreaterThanOrEqual(2);
  await expect.poll(() => websocketOpened).toBe(false);
  await expect.poll(() => page.evaluate(() => window.sessionStorage.getItem('currentGame'))).toBeNull();
});

test('stale stored task is not rendered while active duel preflight is pending', async ({ page }) => {
  const playerID = '66666666-1111-1111-1111-111111111111';
  const sessionToken = '10000000-0000-0000-0000-000000000166';
  const duelID = '66666666-2222-2222-2222-222222222222';
  const task = {
    id: '66666666-3333-3333-3333-333333333333',
    title: 'Pending Stale Restore',
    description: 'This stale task must stay hidden until players/me resolves.',
    category: 'web',
    difficulty: 'easy',
    time_limit: 120,
    time_limit_seconds: 120,
    task_url: 'https://example.com/stale',
    hint_schedule: [],
  };
  let releasePreflight!: () => void;
  const preflightReleased = new Promise<void>((resolve) => {
    releasePreflight = resolve;
  });
  let preflightRequested!: () => void;
  const preflightStarted = new Promise<void>((resolve) => {
    preflightRequested = resolve;
  });

  await page.addInitScript(({ playerID, sessionToken, duelID, task }) => {
    window.sessionStorage.setItem('player_id', playerID);
    window.sessionStorage.setItem('username', 'alice');
    window.sessionStorage.setItem('currentGame', JSON.stringify({
      duel_id: duelID,
      deadline: new Date(Date.now() + 120_000).toISOString(),
      time_limit_seconds: 120,
      task,
    }));
  }, { playerID, sessionToken, duelID, task });

  await page.route('**/api/v1/players/me', async (route) => {
    preflightRequested();
    await preflightReleased;
    await route.fulfill({
      status: 200,
      headers: jsonHeaders,
      body: JSON.stringify({
        player: {
          id: playerID,
          username: 'alice',
          status: 'idle',
          created_at: nowISO(),
        },
      }),
    });
  });

  await page.goto('/task');
  await preflightStarted;

  await expect(page.getByRole('heading', { name: 'Pending Stale Restore' })).toBeHidden();
  releasePreflight();

  await expect(page).toHaveURL(/\/$/);
  await expect(page.getByText('Активная дуэль не найдена. Возвращаемся на главную.')).toBeVisible();
  await expect.poll(() => page.evaluate(() => window.sessionStorage.getItem('currentGame'))).toBeNull();
});

test('stale task session clears storage and returns home before websocket reconnect', async ({ page }) => {
  const playerID = '63636363-6363-6363-6363-636363636363';
  const sessionToken = '10000000-0000-0000-0000-000000000031';
  const duelID = '64646464-6464-6464-6464-646464646464';
  const task = {
    id: '65656565-6565-6565-6565-656565656565',
    title: 'Expired Session Restore',
    description: 'Expired sessions must be cleared before opening websocket.',
    category: 'web',
    difficulty: 'easy',
    time_limit: 120,
    time_limit_seconds: 120,
    task_url: 'https://example.com/task',
    hint_schedule: [],
  };
  let meCalls = 0;
  let websocketOpened = false;

  await page.addInitScript(({ playerID, sessionToken, duelID, task }) => {
    window.sessionStorage.setItem('player_id', playerID);
    window.sessionStorage.setItem('username', 'alice');
    window.sessionStorage.setItem('currentGame', JSON.stringify({
      duel_id: duelID,
      deadline: new Date(Date.now() + 120_000).toISOString(),
      time_limit_seconds: 120,
      task,
    }));
  }, { playerID, sessionToken, duelID, task });

  await page.route('**/api/v1/players/me', async (route) => {
    meCalls += 1;
    expect(route.request().headers()['x-session-token']).toBeUndefined();
    await route.fulfill({
      status: 401,
      headers: jsonHeaders,
      body: JSON.stringify({
        type: 'about:blank',
        title: 'Unauthorized',
        status: 401,
        detail: 'session expired',
      }),
    });
  });
  await page.routeWebSocket((url) => url.pathname === '/ws', () => {
    websocketOpened = true;
  });

  await page.goto('/task');

  await expect(page).toHaveURL(/\/$/);
  await expect(page.getByText('Сессия истекла. Возвращаемся на главную.')).toBeVisible();
  await expect.poll(() => meCalls).toBe(1);
  await expect.poll(() => websocketOpened).toBe(false);
  await expect.poll(() => page.evaluate(() => window.sessionStorage.getItem('player_id'))).toBeNull();
  await expect.poll(() => page.evaluate(() => window.sessionStorage.getItem('session_token'))).toBeNull();
  await expect.poll(() => page.evaluate(() => window.sessionStorage.getItem('username'))).toBeNull();
  await expect.poll(() => page.evaluate(() => window.sessionStorage.getItem('currentGame'))).toBeNull();
});

test('stale stored task is cleared when session has a different active duel', async ({ page }) => {
  const playerID = 'a1a1a1a1-a1a1-a1a1-a1a1-a1a1a1a1a1a1';
  const sessionToken = '10000000-0000-0000-0000-000000000132';
  const duelID = 'a2a2a2a2-a2a2-a2a2-a2a2-a2a2a2a2a2a2';
  const backendDuelID = 'a4a4a4a4-a4a4-a4a4-a4a4-a4a4a4a4a4a4';
  const task = {
    id: 'a3a3a3a3-a3a3-a3a3-a3a3-a3a3a3a3a3a3',
    title: 'Mismatched Local Task',
    description: 'This task must not restore when the backend reports another duel.',
    category: 'web',
    difficulty: 'easy',
    time_limit: 120,
    time_limit_seconds: 120,
    task_url: 'https://example.com/task',
    hint_schedule: [],
  };
  let websocketOpened = false;

  await page.addInitScript(({ playerID, sessionToken, duelID, task }) => {
    window.sessionStorage.setItem('player_id', playerID);
    window.sessionStorage.setItem('username', 'alice');
    window.sessionStorage.setItem('currentGame', JSON.stringify({
      duel_id: duelID,
      deadline: new Date(Date.now() + 120_000).toISOString(),
      time_limit_seconds: 120,
      task,
    }));
  }, { playerID, sessionToken, duelID, task });

  await page.route('**/api/v1/players/me', async (route) => {
    expect(route.request().headers()['x-session-token']).toBeUndefined();
    await route.fulfill({
      status: 200,
      headers: jsonHeaders,
      body: JSON.stringify({
        player: {
          id: playerID,
          username: 'alice',
          status: 'in_duel',
          created_at: nowISO(),
        },
        active_duel: {
          id: backendDuelID,
          status: 'active',
          deadline: new Date(Date.now() + 120_000).toISOString(),
          started_at: nowISO(),
        },
      }),
    });
  });
  await page.routeWebSocket((url) => url.pathname === '/ws', () => {
    websocketOpened = true;
  });

  await page.goto('/task');

  await expect(page).toHaveURL(/\/$/);
  await expect(page.getByText('Активная дуэль не найдена. Возвращаемся на главную.')).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Mismatched Local Task' })).toBeHidden();
  await expect.poll(() => websocketOpened).toBe(false);
  await expect.poll(() => page.evaluate(() => window.sessionStorage.getItem('currentGame'))).toBeNull();
});

test('task websocket invalid session clears storage and returns home without reconnect storm', async ({ page }) => {
  const playerID = '47474747-4747-4747-4747-474747474747';
  const sessionToken = '10000000-0000-0000-0000-000000000047';
  const duelID = '48484848-4848-4848-4848-484848484848';
  const task = {
    id: '49494949-4949-4949-4949-494949494949',
    title: 'Invalid Session Task',
    description: 'A websocket invalid-session event should expire the local session.',
    category: 'web',
    difficulty: 'easy',
    time_limit: 120,
    time_limit_seconds: 120,
    task_url: 'https://example.com/task',
    hint_schedule: [],
  };
  let websocketOpenCount = 0;
  let activeSocket: WebSocketRoute | null = null;
  const sendServerEvent = (event: unknown) => {
    if (!activeSocket) {
      throw new Error('WebSocket was not opened');
    }
    activeSocket.send(JSON.stringify(event));
  };
  const closeActiveSocket = async () => {
    if (!activeSocket) {
      throw new Error('WebSocket was not opened');
    }
    await activeSocket.close({ code: 1000 });
  };

  await page.addInitScript(({ playerID, sessionToken, duelID, task }) => {
    window.sessionStorage.setItem('player_id', playerID);
    window.sessionStorage.setItem('username', 'alice');
    window.sessionStorage.setItem('currentGame', JSON.stringify({
      duel_id: duelID,
      deadline: new Date(Date.now() + 120_000).toISOString(),
      time_limit_seconds: 120,
      task,
    }));
  }, { playerID, sessionToken, duelID, task });

  await mockCurrentPlayerMe(page, playerID, sessionToken, duelID);
  await page.routeWebSocket((url) => url.pathname === '/ws', (ws) => {
    websocketOpenCount += 1;
    activeSocket = ws;
  });

  await page.goto('/task');
  await expect(page.getByRole('heading', { name: 'Invalid Session Task' })).toBeVisible();
  await expect.poll(() => activeSocket !== null).toBeTruthy();

  sendServerEvent({
    type: 'error',
    code: 'player.invalid_session',
    message: 'invalid session token',
  });
  await closeActiveSocket();

  await expect(page).toHaveURL(/\/$/);
  await expect(page.getByText('Сессия истекла. Возвращаемся на главную.')).toBeVisible();
  await expect.poll(() => page.evaluate(() => window.sessionStorage.getItem('player_id'))).toBeNull();
  await expect.poll(() => page.evaluate(() => window.sessionStorage.getItem('session_token'))).toBeNull();
  await expect.poll(() => page.evaluate(() => window.sessionStorage.getItem('username'))).toBeNull();
  await expect.poll(() => page.evaluate(() => window.sessionStorage.getItem('currentGame'))).toBeNull();
  await page.waitForTimeout(300);
  expect(websocketOpenCount).toBe(1);
});

test('task websocket abnormal close rechecks session before reconnecting', async ({ page }) => {
  await page.addInitScript(() => {
    const openedUrls: string[] = [];

    class ClosingWebSocket extends EventTarget {
      static CONNECTING = 0;
      static OPEN = 1;
      static CLOSING = 2;
      static CLOSED = 3;

      readonly url: string;
      readonly protocol = '';
      readonly extensions = '';
      binaryType: BinaryType = 'blob';
      bufferedAmount = 0;
      readyState = ClosingWebSocket.CONNECTING;
      onopen: ((event: Event) => void) | null = null;
      onmessage: ((event: MessageEvent) => void) | null = null;
      onerror: ((event: Event) => void) | null = null;
      onclose: ((event: CloseEvent) => void) | null = null;

      constructor(url: string | URL) {
        super();
        this.url = String(url);
        openedUrls.push(this.url);
        setTimeout(() => {
          if (this.readyState !== ClosingWebSocket.CONNECTING) {
            return;
          }
          this.readyState = ClosingWebSocket.OPEN;
          this.dispatchEvent(new Event('open'));
          this.closeWith(1011, 'backend rejected websocket');
        }, 0);
      }

      dispatchEvent(event: Event): boolean {
        const result = super.dispatchEvent(event);
        const handler = this[`on${event.type}` as keyof this];
        if (typeof handler === 'function') {
          (handler as (value: Event) => void).call(this, event);
        }
        return result;
      }

      send(): void {}

      close(code = 1000, reason = ''): void {
        this.closeWith(code, reason);
      }

      private closeWith(code: number, reason: string): void {
        if (this.readyState === ClosingWebSocket.CLOSED) {
          return;
        }
        this.readyState = ClosingWebSocket.CLOSED;
        this.dispatchEvent(new CloseEvent('close', { code, reason }));
      }
    }

    window.WebSocket = ClosingWebSocket as unknown as typeof WebSocket;
    Object.assign(window, { __closingWebSocketOpenedUrls: openedUrls });
  });

  const playerID = 'abababab-abab-abab-abab-abababababab';
  const sessionToken = '10000000-0000-0000-0000-000000000134';
  const duelID = 'acacacac-acac-acac-acac-acacacacacac';
  const task = {
    id: 'adadadad-adad-adad-adad-adadadadadad',
    title: 'Handshake Session Recheck',
    description: 'Abnormal websocket close should verify REST session before retry.',
    category: 'web',
    difficulty: 'easy',
    time_limit: 120,
    time_limit_seconds: 120,
    task_url: 'https://example.com/task',
    hint_schedule: [],
  };
  let meCalls = 0;

  await page.addInitScript(({ playerID, sessionToken, duelID, task }) => {
    window.sessionStorage.setItem('player_id', playerID);
    window.sessionStorage.setItem('username', 'alice');
    window.sessionStorage.setItem('currentGame', JSON.stringify({
      duel_id: duelID,
      deadline: new Date(Date.now() + 120_000).toISOString(),
      time_limit_seconds: 120,
      task,
    }));
  }, { playerID, sessionToken, duelID, task });

  await page.route('**/api/v1/players/me', async (route) => {
    meCalls += 1;
    expect(route.request().headers()['x-session-token']).toBeUndefined();
    if (meCalls === 1) {
      await route.fulfill({
        status: 200,
        headers: jsonHeaders,
        body: JSON.stringify({
          player: {
            id: playerID,
            username: 'alice',
            status: 'idle',
            created_at: nowISO(),
          },
          active_duel: {
            id: duelID,
            status: 'active',
            deadline: new Date(Date.now() + 120_000).toISOString(),
            started_at: nowISO(),
          },
        }),
      });
      return;
    }
    await route.fulfill({
      status: 401,
      headers: jsonHeaders,
      body: JSON.stringify({
        type: 'about:blank',
        title: 'Unauthorized',
        status: 401,
        detail: 'websocket token expired',
      }),
    });
  });

  await page.goto('/task');

  await expect(page).toHaveURL(/\/$/);
  await expect(page.getByText('Сессия истекла. Возвращаемся на главную.')).toBeVisible();
  await expect.poll(() => meCalls).toBe(2);
  await expect.poll(() => page.evaluate(() => window.sessionStorage.getItem('session_token'))).toBeNull();
  const openedUrls = await page.evaluate(() => (
    window as unknown as { __closingWebSocketOpenedUrls: string[] }
  ).__closingWebSocketOpenedUrls.filter((url) => new URL(url).pathname === '/ws'));
  expect(openedUrls).toHaveLength(1);
});

test('task websocket abnormal close clears stale duel when session has no active duel', async ({ page }) => {
  await page.addInitScript(() => {
    const openedUrls: string[] = [];

    class ClosingWebSocket extends EventTarget {
      static CONNECTING = 0;
      static OPEN = 1;
      static CLOSING = 2;
      static CLOSED = 3;

      readonly url: string;
      readonly protocol = '';
      readonly extensions = '';
      binaryType: BinaryType = 'blob';
      bufferedAmount = 0;
      readyState = ClosingWebSocket.CONNECTING;
      onopen: ((event: Event) => void) | null = null;
      onmessage: ((event: MessageEvent) => void) | null = null;
      onerror: ((event: Event) => void) | null = null;
      onclose: ((event: CloseEvent) => void) | null = null;

      constructor(url: string | URL) {
        super();
        this.url = String(url);
        openedUrls.push(this.url);
        setTimeout(() => {
          if (this.readyState !== ClosingWebSocket.CONNECTING) {
            return;
          }
          this.readyState = ClosingWebSocket.OPEN;
          this.dispatchEvent(new Event('open'));
          this.closeWith(1011, 'backend closed websocket');
        }, 0);
      }

      dispatchEvent(event: Event): boolean {
        const result = super.dispatchEvent(event);
        const handler = this[`on${event.type}` as keyof this];
        if (typeof handler === 'function') {
          (handler as (value: Event) => void).call(this, event);
        }
        return result;
      }

      send(): void {}

      close(code = 1000, reason = ''): void {
        this.closeWith(code, reason);
      }

      private closeWith(code: number, reason: string): void {
        if (this.readyState === ClosingWebSocket.CLOSED) {
          return;
        }
        this.readyState = ClosingWebSocket.CLOSED;
        this.dispatchEvent(new CloseEvent('close', { code, reason }));
      }
    }

    window.WebSocket = ClosingWebSocket as unknown as typeof WebSocket;
    Object.assign(window, { __closingWebSocketOpenedUrls: openedUrls });
  });

  const playerID = 'edededed-eded-eded-eded-edededededed';
  const sessionToken = '10000000-0000-0000-0000-000000000167';
  const duelID = 'eeeeeeee-eeee-eeee-eeee-eeeeeeeeeeee';
  const task = {
    id: 'efefefef-efef-efef-efef-efefefefefef',
    title: 'Reconnect Stale Duel',
    description: 'Reconnect precheck must verify active duel identity.',
    category: 'web',
    difficulty: 'easy',
    time_limit: 120,
    time_limit_seconds: 120,
    task_url: 'https://example.com/task',
    hint_schedule: [],
  };
  let meCalls = 0;

  await page.addInitScript(({ playerID, sessionToken, duelID, task }) => {
    window.sessionStorage.setItem('player_id', playerID);
    window.sessionStorage.setItem('username', 'alice');
    window.sessionStorage.setItem('currentGame', JSON.stringify({
      duel_id: duelID,
      deadline: new Date(Date.now() + 120_000).toISOString(),
      time_limit_seconds: 120,
      task,
    }));
  }, { playerID, sessionToken, duelID, task });

  await page.route('**/api/v1/players/me', async (route) => {
    meCalls += 1;
    expect(route.request().headers()['x-session-token']).toBeUndefined();
    await route.fulfill({
      status: 200,
      headers: jsonHeaders,
      body: JSON.stringify({
        player: {
          id: playerID,
          username: 'alice',
          status: 'idle',
          created_at: nowISO(),
        },
        ...(meCalls === 1
          ? {
              active_duel: {
                id: duelID,
                status: 'active',
                deadline: new Date(Date.now() + 120_000).toISOString(),
                started_at: nowISO(),
              },
            }
          : {}),
      }),
    });
  });

  await page.goto('/task');

  await expect(page).toHaveURL(/\/$/);
  await expect(page.getByText('Активная дуэль не найдена. Возвращаемся на главную.')).toBeVisible();
  await expect.poll(() => meCalls).toBeGreaterThanOrEqual(2);
  await expect.poll(() => page.evaluate(() => window.sessionStorage.getItem('currentGame'))).toBeNull();
  const openedUrls = await page.evaluate(() => (
    window as unknown as { __closingWebSocketOpenedUrls: string[] }
  ).__closingWebSocketOpenedUrls.filter((url) => new URL(url).pathname === '/ws'));
  expect(openedUrls).toHaveLength(1);
});

test('corrupt currentGame storage is cleared before task restore', async ({ page }) => {
  const playerID = '65656565-6565-6565-6565-656565656565';
  const sessionToken = '10000000-0000-0000-0000-000000000020';
  const pageErrors: string[] = [];
  let websocketOpened = false;

  page.on('pageerror', (error) => {
    pageErrors.push(error.message);
  });

  await page.addInitScript(({ playerID, sessionToken }) => {
    window.sessionStorage.setItem('player_id', playerID);
    window.sessionStorage.setItem('username', 'alice');
    window.sessionStorage.setItem('currentGame', JSON.stringify({
      duel_id: '66666666-6666-6666-6666-666666666666',
      deadline: 'not-a-date',
      time_limit_seconds: 120,
      task: null,
    }));
  }, { playerID, sessionToken });

  await page.routeWebSocket((url) => url.pathname === '/ws', () => {
    websocketOpened = true;
  });
  await mockCurrentPlayerMe(page, playerID, sessionToken);

  await page.goto('/task');

  await expect(page).toHaveURL(/\/$/);
  await expect.poll(() => page.evaluate(() => window.sessionStorage.getItem('currentGame'))).toBeNull();
  await expect.poll(() => websocketOpened).toBe(false);
  expect(pageErrors).toEqual([]);
});

test('stored game data with malformed uuid is cleared before task restore', async ({ page }) => {
  const playerID = '65656565-6565-6565-6565-656565656565';
  const sessionToken = '65656565-0000-0000-0000-000000000001';
  const task = {
    id: '65656565-0000-0000-0000-000000000002',
    title: 'Malformed UUID Storage',
    description: 'This game must not restore because duel_id is malformed.',
    category: 'web',
    difficulty: 'easy',
    time_limit: 120,
    time_limit_seconds: 120,
    task_url: 'https://example.com/task',
    hint_schedule: [],
  };
  let websocketOpened = false;

  await page.addInitScript(({ playerID, sessionToken, task }) => {
    window.sessionStorage.setItem('player_id', playerID);
    window.sessionStorage.setItem('username', 'alice');
    window.sessionStorage.setItem('currentGame', JSON.stringify({
      duel_id: 'not-a-uuid',
      deadline: new Date(Date.now() + 120_000).toISOString(),
      time_limit_seconds: 120,
      task,
    }));
    window.sessionStorage.setItem('game_result', JSON.stringify({
      state: 'won',
      duel_id: 'also-not-a-uuid',
      winner_id: 'still-not-a-uuid',
      winner_username: 'alice',
    }));
  }, { playerID, sessionToken, task });

  await page.routeWebSocket((url) => url.pathname === '/ws', () => {
    websocketOpened = true;
  });
  await mockCurrentPlayerMe(page, playerID, sessionToken);

  await page.goto('/task');

  await expect(page).toHaveURL(/\/$/);
  await expect.poll(() => page.evaluate(() => window.sessionStorage.getItem('currentGame'))).toBeNull();
  await expect.poll(() => page.evaluate(() => window.sessionStorage.getItem('game_result'))).toBeNull();
  await expect.poll(() => websocketOpened).toBe(false);
});

test('stored currentGame with invalid task invariants is cleared before task restore', async ({ page }) => {
  const playerID = '75757575-7575-7575-7575-757575757575';
  const sessionToken = '10000000-0000-0000-0000-000000000021';
  const duelID = '76767676-7676-7676-7676-767676767676';
  const baseTask = {
    id: '77777777-7777-7777-7777-777777777777',
    title: 'Stored Invalid Task',
    description: 'Invalid source URLs or time values must not restore.',
    category: 'web',
    difficulty: 'easy',
    time_limit: 120,
    time_limit_seconds: 120,
    task_url: 'https://example.com/task',
    source_file_url: 'https://files.example/source.zip',
    hint_schedule: [],
  };
  const pageErrors: string[] = [];
  let websocketOpened = false;

  page.on('pageerror', (error) => {
    pageErrors.push(error.message);
  });

  await page.routeWebSocket((url) => url.pathname === '/ws', () => {
    websocketOpened = true;
  });
  await mockCurrentPlayerMe(page, playerID, sessionToken);

  await page.goto('/');

  for (const currentGame of [
    {
      duel_id: duelID,
      deadline: inSecondsISO(120),
      time_limit_seconds: 120.5,
      task: baseTask,
    },
    {
      duel_id: duelID,
      deadline: inSecondsISO(120),
      time_limit_seconds: 120,
      task: {
        ...baseTask,
        source_file_url: 'ftp://files.example/source.zip',
      },
    },
    {
      duel_id: duelID,
      deadline: inSecondsISO(120),
      time_limit_seconds: 120,
      task: {
        ...baseTask,
        hint_schedule: [
          { hint_index: 1, unlock_at: inSecondsISO(30) },
          { hint_index: 1, unlock_at: inSecondsISO(60) },
        ],
      },
    },
    {
      duel_id: duelID,
      deadline: inSecondsISO(120),
      time_limit_seconds: 120,
      task: {
        ...baseTask,
        unlocked_hints: [
          { hint_index: 1, hint: 'one', unlocked_at: nowISO() },
          { hint_index: 2, hint: 'two', unlocked_at: nowISO() },
          { hint_index: 3, hint: 'three', unlocked_at: nowISO() },
          { hint_index: 1, hint: 'four', unlocked_at: nowISO() },
        ],
      },
    },
  ]) {
    websocketOpened = false;
    await page.evaluate(({ playerID, sessionToken, currentGame }) => {
      window.sessionStorage.clear();
      window.sessionStorage.setItem('player_id', playerID);
      window.sessionStorage.setItem('username', 'alice');
      window.sessionStorage.setItem('currentGame', JSON.stringify(currentGame));
    }, { playerID, sessionToken, currentGame });

    await page.goto('/task');
    await expect(page).toHaveURL(/\/$/);
    await expect.poll(() => page.evaluate(() => window.sessionStorage.getItem('currentGame'))).toBeNull();
    await expect.poll(() => websocketOpened).toBe(false);
  }

  expect(pageErrors).toEqual([]);
});

test('stored terminal task result restores without opening websocket', async ({ page }) => {
  const playerID = '67676767-6767-6767-6767-676767676767';
  const sessionToken = '10000000-0000-0000-0000-000000000022';
  const duelID = '68686868-6868-6868-6868-686868686868';
  const task = {
    id: '69696969-6969-6969-6969-696969696969',
    title: 'Stored Terminal Result',
    description: 'Terminal result should not reconnect.',
    category: 'web',
    difficulty: 'easy',
    time_limit: 120,
    time_limit_seconds: 120,
    task_url: 'https://example.com/task',
    hint_schedule: [],
  };
  const pageErrors: string[] = [];
  let websocketOpened = false;

  page.on('pageerror', (error) => {
    pageErrors.push(error.message);
  });

  await page.addInitScript(({ playerID, sessionToken, duelID, task }) => {
    window.sessionStorage.setItem('player_id', playerID);
    window.sessionStorage.setItem('username', 'alice');
    window.sessionStorage.setItem('currentGame', JSON.stringify({
      duel_id: duelID,
      deadline: new Date(Date.now() + 120_000).toISOString(),
      time_limit_seconds: 120,
      task,
    }));
    window.sessionStorage.setItem('game_result', JSON.stringify({
      state: 'won',
      duel_id: duelID,
      winner_id: playerID,
      winner_username: 'alice',
    }));
  }, { playerID, sessionToken, duelID, task });

  await page.routeWebSocket((url) => url.pathname === '/ws', (ws) => {
    websocketOpened = true;
    ws.close();
  });

  await page.goto('/task');
  await expect(page.getByRole('heading', { name: 'Stored Terminal Result' })).toBeVisible();
  await expect(page.getByText('ПОБЕДА!')).toBeVisible();
  await page.waitForTimeout(200);

  await expect.poll(() => websocketOpened).toBe(false);
  await expect(page.getByText('Соединение закрыто. Обновите страницу для реконнекта.')).toBeHidden();
  await expect(page.getByText('Ошибка WebSocket соединения')).toBeHidden();
  expect(pageErrors).toEqual([]);
});

test('mismatched stored task result is cleared and active duel reconnects', async ({ page }) => {
  const playerID = '70707070-7070-7070-7070-707070707070';
  const sessionToken = '10000000-0000-0000-0000-000000000023';
  const duelID = '71717171-7171-7171-7171-717171717171';
  const staleDuelID = '72727272-7272-7272-7272-727272727272';
  const task = {
    id: '73737373-7373-7373-7373-737373737373',
    title: 'Active Duel With Stale Result',
    description: 'Stale result should not finish this duel.',
    category: 'web',
    difficulty: 'easy',
    time_limit: 120,
    time_limit_seconds: 120,
    task_url: 'https://example.com/task',
    hint_schedule: [],
  };
  const pageErrors: string[] = [];
  let activeSocket: WebSocketRoute | null = null;

  page.on('pageerror', (error) => {
    pageErrors.push(error.message);
  });

  await page.addInitScript(({ playerID, sessionToken, duelID, staleDuelID, task }) => {
    window.sessionStorage.setItem('player_id', playerID);
    window.sessionStorage.setItem('username', 'alice');
    window.sessionStorage.setItem('currentGame', JSON.stringify({
      duel_id: duelID,
      deadline: new Date(Date.now() + 120_000).toISOString(),
      time_limit_seconds: 120,
      task,
    }));
    window.sessionStorage.setItem('game_result', JSON.stringify({
      state: 'won',
      duel_id: staleDuelID,
      winner_id: playerID,
      winner_username: 'alice',
    }));
  }, { playerID, sessionToken, duelID, staleDuelID, task });

  await mockCurrentPlayerMe(page, playerID, sessionToken, duelID);
  await page.routeWebSocket((url) => url.pathname === '/ws', (ws) => {
    activeSocket = ws;
  });

  await page.goto('/task');
  await expect(page.getByRole('heading', { name: 'Active Duel With Stale Result' })).toBeVisible();
  await expect.poll(() => activeSocket !== null).toBeTruthy();
  await expect(page.getByText('ПОБЕДА!')).toBeHidden();
  await expect(page.getByPlaceholder('flag{...}')).toBeEnabled();
  await expect.poll(() => page.evaluate(() => window.sessionStorage.getItem('game_result'))).toBeNull();
  expect(pageErrors).toEqual([]);
});

test('invalid stored opponent reconnect deadline clears currentGame before render', async ({ page }) => {
  const playerID = '74747474-7474-7474-7474-747474747474';
  const sessionToken = '10000000-0000-0000-0000-000000000024';
  const duelID = '75757575-7575-7575-7575-757575757575';
  const task = {
    id: '76767676-7676-7676-7676-767676767676',
    title: 'Invalid Stored Reconnect Deadline',
    description: 'Invalid reconnect deadline should not render.',
    category: 'web',
    difficulty: 'easy',
    time_limit: 120,
    time_limit_seconds: 120,
    task_url: 'https://example.com/task',
    hint_schedule: [],
  };
  const pageErrors: string[] = [];
  let websocketOpened = false;

  page.on('pageerror', (error) => {
    pageErrors.push(error.message);
  });

  await page.addInitScript(({ playerID, sessionToken, duelID, task }) => {
    window.sessionStorage.setItem('player_id', playerID);
    window.sessionStorage.setItem('username', 'alice');
    window.sessionStorage.setItem('currentGame', JSON.stringify({
      duel_id: duelID,
      deadline: new Date(Date.now() + 120_000).toISOString(),
      time_limit_seconds: 120,
      task,
      opponent_disconnected: true,
      opponent_reconnect_deadline: 'not-a-date',
    }));
  }, { playerID, sessionToken, duelID, task });

  await page.routeWebSocket((url) => url.pathname === '/ws', () => {
    websocketOpened = true;
  });
  await mockCurrentPlayerMe(page, playerID, sessionToken);

  await page.goto('/task');

  await expect(page).toHaveURL(/\/$/);
  await expect(page.getByText('Invalid Date')).toBeHidden();
  await expect.poll(() => page.evaluate(() => window.sessionStorage.getItem('currentGame'))).toBeNull();
  await expect.poll(() => websocketOpened).toBe(false);
  expect(pageErrors).toEqual([]);
});

test('missing clipboard API does not mark endpoint as copied', async ({ page }) => {
  const playerID = '4a4a4a4a-4a4a-4a4a-4a4a-4a4a4a4a4a4a';
  const sessionToken = '10000000-0000-0000-0000-00000000004a';
  const duelID = '4b4b4b4b-4b4b-4b4b-4b4b-4b4b4b4b4b4b';
  const task = {
    id: '4c4c4c4c-4c4c-4c4c-4c4c-4c4c4c4c4c4c',
    title: 'Missing Clipboard Endpoint',
    description: 'Copy feedback must not claim success when clipboard is unavailable.',
    category: 'pwn',
    difficulty: 'easy',
    time_limit: 120,
    time_limit_seconds: 120,
    task_url: 'pwn.example:31337',
    hint_schedule: [],
  };
  const pageErrors: string[] = [];

  page.on('pageerror', (error) => {
    pageErrors.push(error.message);
  });

  await page.addInitScript(({ playerID, sessionToken, duelID, task }) => {
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: undefined,
    });
    window.sessionStorage.setItem('player_id', playerID);
    window.sessionStorage.setItem('username', 'alice');
    window.sessionStorage.setItem('currentGame', JSON.stringify({
      duel_id: duelID,
      deadline: new Date(Date.now() + 120_000).toISOString(),
      time_limit_seconds: 120,
      task,
    }));
  }, { playerID, sessionToken, duelID, task });

  await mockCurrentPlayerMe(page, playerID, sessionToken, duelID);
  await page.routeWebSocket((url) => url.pathname === '/ws', () => undefined);

  await page.goto('/task');
  await expect(page.getByRole('heading', { name: 'Missing Clipboard Endpoint' })).toBeVisible();
  await page.getByRole('button', { name: 'Копировать' }).click();

  await expect(page.getByText('Не удалось скопировать endpoint. Скопируйте вручную.')).toBeVisible();
  await expect(page.getByRole('button', { name: '✓ Скопировано' })).toBeHidden();
  expect(pageErrors).toEqual([]);
});

test('backend duel_finished win overrides local timer timeup', async ({ page }) => {
  await installWebSocketErrorProbe(page);

  const playerID = 'dddddddd-dddd-dddd-dddd-dddddddddddd';
  const sessionToken = '10000000-0000-0000-0000-000000000072';
  const duelID = 'dededede-dede-dede-dede-dededededede';
  const opponentID = 'dfdfdfdf-dfdf-dfdf-dfdf-dfdfdfdfdfdf';
  const deadline = new Date(Date.now() - 1000).toISOString();
  const task = {
    id: 'dcdcdcdc-dcdc-dcdc-dcdc-dcdcdcdcdcdc',
    title: 'Local Timer Override Win',
    description: 'Backend terminal result must override local timeup.',
    category: 'web',
    difficulty: 'easy',
    time_limit: 120,
    time_limit_seconds: 120,
    task_url: 'https://example.com/task',
    hint_schedule: [],
  };
  let activeSocket: WebSocketRoute | null = null;
  const sendServerEvent = (event: unknown) => {
    if (!activeSocket) {
      throw new Error('WebSocket was not opened');
    }
    activeSocket.send(JSON.stringify(event));
  };

  await page.addInitScript(({ playerID, sessionToken, duelID, deadline, task }) => {
    window.sessionStorage.setItem('player_id', playerID);
    window.sessionStorage.setItem('username', 'alice');
    window.sessionStorage.setItem('currentGame', JSON.stringify({
      duel_id: duelID,
      deadline,
      time_limit_seconds: 120,
      task,
      opponent_username: 'bob',
    }));
  }, { playerID, sessionToken, duelID, deadline, task });

  await mockCurrentPlayerMe(page, playerID, sessionToken, duelID);
  await page.routeWebSocket((url) => url.pathname === '/ws', (ws) => {
    activeSocket = ws;
  });

  await page.goto('/task');
  await expect(page.getByRole('heading', { name: 'Local Timer Override Win' })).toBeVisible();
  await expect.poll(() => activeSocket !== null).toBeTruthy();
  await expect(page.getByText('ВРЕМЯ ВЫШЛО!')).toBeVisible();

  const dispatched = await page.evaluate(() => {
    const testWindow = window as unknown as WebSocketErrorProbeWindow;
    return testWindow.__dispatchLastWebSocketError();
  });
  expect(dispatched).toBe(true);
  await page.waitForTimeout(150);
  await expect(page.getByText('Ошибка WebSocket соединения')).toBeHidden();

  sendServerEvent({
    type: 'duel_finished',
    payload: {
      duel_id: duelID,
      winner_id: playerID,
      winner_username: 'alice',
      your_solved: true,
      opponent_solved: false,
      duel: {
        id: duelID,
        player1_id: playerID,
        player2_id: opponentID,
        status: 'finished',
        winner_id: playerID,
        deadline,
        started_at: nowISO(),
        finished_at: nowISO(),
      },
    },
  });

  await expect(page.getByText('ПОБЕДА!')).toBeVisible();
  await expect(page.getByText('ВРЕМЯ ВЫШЛО!')).toBeHidden();
  const storedResult = await page.evaluate(() => {
    const raw = window.sessionStorage.getItem('game_result');
    return raw ? JSON.parse(raw) as { state?: string; source?: string; winner_id?: string | null } : null;
  });
  expect(storedResult).toMatchObject({
    state: 'won',
    source: 'server',
    duel_id: duelID,
    winner_id: playerID,
  });
});

test('backend duel_finished loss overrides local timer timeup', async ({ page }) => {
  const playerID = 'e4e4e4e4-e4e4-e4e4-e4e4-e4e4e4e4e4e4';
  const sessionToken = '10000000-0000-0000-0000-000000000073';
  const duelID = 'e5e5e5e5-e5e5-e5e5-e5e5-e5e5e5e5e5e5';
  const opponentID = 'e6e6e6e6-e6e6-e6e6-e6e6-e6e6e6e6e6e6';
  const deadline = new Date(Date.now() - 1000).toISOString();
  const task = {
    id: 'e7e7e7e7-e7e7-e7e7-e7e7-e7e7e7e7e7e7',
    title: 'Local Timer Override Loss',
    description: 'Opponent server win must override local timeup.',
    category: 'web',
    difficulty: 'easy',
    time_limit: 120,
    time_limit_seconds: 120,
    task_url: 'https://example.com/task',
    hint_schedule: [],
  };
  let activeSocket: WebSocketRoute | null = null;
  const sendServerEvent = (event: unknown) => {
    if (!activeSocket) {
      throw new Error('WebSocket was not opened');
    }
    activeSocket.send(JSON.stringify(event));
  };

  await page.addInitScript(({ playerID, sessionToken, duelID, deadline, task }) => {
    window.sessionStorage.setItem('player_id', playerID);
    window.sessionStorage.setItem('username', 'alice');
    window.sessionStorage.setItem('currentGame', JSON.stringify({
      duel_id: duelID,
      deadline,
      time_limit_seconds: 120,
      task,
      opponent_username: 'bob',
    }));
  }, { playerID, sessionToken, duelID, deadline, task });

  await mockCurrentPlayerMe(page, playerID, sessionToken, duelID);
  await page.routeWebSocket((url) => url.pathname === '/ws', (ws) => {
    activeSocket = ws;
  });

  await page.goto('/task');
  await expect(page.getByRole('heading', { name: 'Local Timer Override Loss' })).toBeVisible();
  await expect.poll(() => activeSocket !== null).toBeTruthy();
  await expect(page.getByText('ВРЕМЯ ВЫШЛО!')).toBeVisible();

  sendServerEvent({
    type: 'duel_finished',
    payload: {
      duel_id: duelID,
      winner_id: opponentID,
      winner_username: 'bob',
      your_solved: false,
      opponent_solved: true,
      duel: {
        id: duelID,
        player1_id: playerID,
        player2_id: opponentID,
        status: 'finished',
        winner_id: opponentID,
        deadline,
        started_at: nowISO(),
        finished_at: nowISO(),
      },
    },
  });

  await expect(page.getByText('ПОРАЖЕНИЕ')).toBeVisible();
  await expect(page.getByText('ВРЕМЯ ВЫШЛО!')).toBeHidden();
  const storedResult = await page.evaluate(() => {
    const raw = window.sessionStorage.getItem('game_result');
    return raw ? JSON.parse(raw) as { state?: string; source?: string; winner_id?: string | null } : null;
  });
  expect(storedResult).toMatchObject({
    state: 'lost',
    source: 'server',
    duel_id: duelID,
    winner_id: opponentID,
  });
});

test('stored local timer result does not block backend terminal restore', async ({ page }) => {
  const playerID = 'edededed-eded-eded-eded-edededededed';
  const sessionToken = '10000000-0000-0000-0000-000000000074';
  const duelID = 'eeeeeeee-eeee-eeee-eeee-eeeeeeeeeeee';
  const opponentID = 'efefefef-efef-efef-efef-efefefefefef';
  const deadline = inSecondsISO(120);
  const task = {
    id: 'e8e8e8e8-e8e8-e8e8-e8e8-e8e8e8e8e8e8',
    title: 'Stored Local Timer Result',
    description: 'Local timer result must reconnect for backend terminal state.',
    category: 'web',
    difficulty: 'easy',
    time_limit: 120,
    time_limit_seconds: 120,
    task_url: 'https://example.com/task',
    hint_schedule: [],
  };
  let activeSocket: WebSocketRoute | null = null;
  const sendServerEvent = (event: unknown) => {
    if (!activeSocket) {
      throw new Error('WebSocket was not opened');
    }
    activeSocket.send(JSON.stringify(event));
  };

  await page.addInitScript(({ playerID, sessionToken, duelID, deadline, task }) => {
    window.sessionStorage.setItem('player_id', playerID);
    window.sessionStorage.setItem('username', 'alice');
    window.sessionStorage.setItem('currentGame', JSON.stringify({
      duel_id: duelID,
      deadline,
      time_limit_seconds: 120,
      task,
      opponent_username: 'bob',
    }));
    window.sessionStorage.setItem('game_result', JSON.stringify({
      state: 'timeup',
      source: 'local_timer',
      duel_id: duelID,
    }));
  }, { playerID, sessionToken, duelID, deadline, task });

  await mockCurrentPlayerMe(page, playerID, sessionToken, duelID);
  await page.routeWebSocket((url) => url.pathname === '/ws', (ws) => {
    activeSocket = ws;
  });

  await page.goto('/task');
  await expect(page.getByRole('heading', { name: 'Stored Local Timer Result' })).toBeVisible();
  await expect.poll(() => activeSocket !== null).toBeTruthy();
  await expect(page.getByText('ВРЕМЯ ВЫШЛО!')).toBeHidden();

  sendServerEvent({
    type: 'duel_finished',
    payload: {
      duel_id: duelID,
      winner_id: opponentID,
      winner_username: 'bob',
      your_solved: false,
      opponent_solved: true,
      duel: {
        id: duelID,
        player1_id: playerID,
        player2_id: opponentID,
        status: 'finished',
        winner_id: opponentID,
        deadline,
        started_at: nowISO(),
        finished_at: nowISO(),
      },
    },
  });

  await expect(page.getByText('ПОРАЖЕНИЕ')).toBeVisible();
  const storedResult = await page.evaluate(() => {
    const raw = window.sessionStorage.getItem('game_result');
    return raw ? JSON.parse(raw) as { state?: string; source?: string; winner_id?: string | null } : null;
  });
  expect(storedResult).toMatchObject({
    state: 'lost',
    source: 'server',
    duel_id: duelID,
    winner_id: opponentID,
  });
});

test('stored server terminal task result restores without opening websocket', async ({ page }) => {
  const playerID = 'f1f1f1f1-f1f1-f1f1-f1f1-f1f1f1f1f1f1';
  const sessionToken = '10000000-0000-0000-0000-000000000075';
  const duelID = 'f2f2f2f2-f2f2-f2f2-f2f2-f2f2f2f2f2f2';
  const opponentID = 'f3f3f3f3-f3f3-f3f3-f3f3-f3f3f3f3f3f3';
  const task = {
    id: 'f4f4f4f4-f4f4-f4f4-f4f4-f4f4f4f4f4f4',
    title: 'Stored Server Result',
    description: 'Server terminal result should not reconnect.',
    category: 'web',
    difficulty: 'easy',
    time_limit: 120,
    time_limit_seconds: 120,
    task_url: 'https://example.com/task',
    hint_schedule: [],
  };
  let websocketOpened = false;

  await page.addInitScript(({ playerID, sessionToken, duelID, opponentID, task }) => {
    window.sessionStorage.setItem('player_id', playerID);
    window.sessionStorage.setItem('username', 'alice');
    window.sessionStorage.setItem('currentGame', JSON.stringify({
      duel_id: duelID,
      deadline: new Date(Date.now() + 120_000).toISOString(),
      time_limit_seconds: 120,
      task,
      opponent_username: 'bob',
    }));
    window.sessionStorage.setItem('game_result', JSON.stringify({
      state: 'lost',
      source: 'server',
      duel_id: duelID,
      winner_id: opponentID,
      winner_username: 'bob',
    }));
  }, { playerID, sessionToken, duelID, opponentID, task });

  await page.routeWebSocket((url) => url.pathname === '/ws', () => {
    websocketOpened = true;
  });

  await page.goto('/task');
  await expect(page.getByRole('heading', { name: 'Stored Server Result' })).toBeVisible();
  await expect(page.getByText('ПОРАЖЕНИЕ')).toBeVisible();
  await page.waitForTimeout(200);

  await expect.poll(() => websocketOpened).toBe(false);
  await expect(page.getByText('Соединение закрыто. Обновите страницу для реконнекта.')).toBeHidden();
  await expect(page.getByText('Ошибка WebSocket соединения')).toBeHidden();
});

test('task page keeps first server terminal result after duel_expired', async ({ page }) => {
  const playerID = 'f5f5f5f5-f5f5-f5f5-f5f5-f5f5f5f5f5f5';
  const sessionToken = '10000000-0000-0000-0000-000000000076';
  const duelID = 'f6f6f6f6-f6f6-f6f6-f6f6-f6f6f6f6f6f6';
  const opponentID = 'f7f7f7f7-f7f7-f7f7-f7f7-f7f7f7f7f7f7';
  const deadline = inSecondsISO(120);
  const task = {
    id: 'f8f8f8f8-f8f8-f8f8-f8f8-f8f8f8f8f8f8',
    title: 'First Server Terminal Wins',
    description: 'Later server terminal events must not rewrite accepted results.',
    category: 'web',
    difficulty: 'easy',
    time_limit: 120,
    time_limit_seconds: 120,
    task_url: 'https://example.com/task',
    hint_schedule: [],
  };
  let activeSocket: WebSocketRoute | null = null;
  const sendServerEvent = (event: unknown) => {
    if (!activeSocket) {
      throw new Error('WebSocket was not opened');
    }
    activeSocket.send(JSON.stringify(event));
  };

  await page.addInitScript(({ playerID, sessionToken, duelID, deadline, task }) => {
    window.sessionStorage.setItem('player_id', playerID);
    window.sessionStorage.setItem('username', 'alice');
    window.sessionStorage.setItem('currentGame', JSON.stringify({
      duel_id: duelID,
      deadline,
      time_limit_seconds: 120,
      task,
      opponent_username: 'bob',
    }));
  }, { playerID, sessionToken, duelID, deadline, task });

  await mockCurrentPlayerMe(page, playerID, sessionToken, duelID);
  await page.routeWebSocket((url) => url.pathname === '/ws', (ws) => {
    activeSocket = ws;
  });

  await page.goto('/task');
  await expect(page.getByRole('heading', { name: 'First Server Terminal Wins' })).toBeVisible();
  await expect.poll(() => activeSocket !== null).toBeTruthy();

  sendServerEvent({
    type: 'duel_expired',
    payload: {
      duel_id: duelID,
    },
  });

  await expect(page.getByText('ВРЕМЯ ВЫШЛО!')).toBeVisible();

  sendServerEvent({
    type: 'duel_finished',
    payload: {
      duel_id: duelID,
      winner_id: playerID,
      winner_username: 'alice',
      your_solved: true,
      opponent_solved: false,
      duel: {
        id: duelID,
        player1_id: playerID,
        player2_id: opponentID,
        status: 'finished',
        winner_id: playerID,
        deadline,
        started_at: nowISO(),
        finished_at: nowISO(),
      },
    },
  });

  await page.waitForTimeout(150);
  await expect(page.getByText('ВРЕМЯ ВЫШЛО!')).toBeVisible();
  await expect(page.getByText('ПОБЕДА!')).toBeHidden();
  const storedResult = await page.evaluate(() => {
    const raw = window.sessionStorage.getItem('game_result');
    return raw ? JSON.parse(raw) as { state?: string; source?: string; winner_id?: string | null } : null;
  });
  expect(storedResult).toMatchObject({
    state: 'timeup',
    source: 'server',
    duel_id: duelID,
  });
});
