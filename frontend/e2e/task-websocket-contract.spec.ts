import { expect, test, type Page, type WebSocketRoute } from '@playwright/test';
import { inSecondsISO, jsonHeaders, mockPlayerLogout, nowISO } from './support/common';
import {
  installWebSocketErrorProbe,
  type WebSocketErrorProbeWindow,
} from './support/browser';

const routePlayerMeFromBrowserStorage = async (page: Page) => {
  await page.route('**/api/v1/players/me', async (route) => {
    const state = await page.evaluate(() => {
      const rawGame = window.sessionStorage.getItem('currentGame');
      const game = rawGame
        ? JSON.parse(rawGame) as { duel_id?: string; deadline?: string }
        : null;
      return {
        playerID: window.sessionStorage.getItem('player_id'),
        username: window.sessionStorage.getItem('username') || 'alice',
        game,
      };
    });

    if (!state.playerID) {
      await route.fulfill({
        status: 401,
        headers: jsonHeaders,
        body: JSON.stringify({
          type: 'about:blank',
          title: 'unauthorized',
          status: 401,
        }),
      });
      return;
    }

    await route.fulfill({
      status: 200,
      headers: jsonHeaders,
      body: JSON.stringify({
        player: {
          id: state.playerID,
          username: state.username,
          status: state.game ? 'in_duel' : 'idle',
          created_at: nowISO(),
        },
        ...(state.game?.duel_id && state.game.deadline
          ? {
              active_duel: {
                id: state.game.duel_id,
                status: 'active',
                deadline: state.game.deadline,
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
  await routePlayerMeFromBrowserStorage(page);
});

test('task description wraps long unbroken text inside card', async ({ page }) => {
  const playerID = '10101010-1010-1010-1010-101010101010';
  const sessionToken = '10000000-0000-0000-0000-000000000100';
  const duelID = '11111111-1111-1111-1111-111111111111';
  const deadline = inSecondsISO(300);
  const description =
    'VmpJd2VFNUhSa2RpTTNCcUIwWktjbFpxVG01a01WSIhZVVZPYWsxWVFsaFVNV1F3VkdzeGNrNVVTbGhoTVVwSVdrWmFkbVZyTVVWTlJEQTk'.repeat(2);

  await page.addInitScript(({ playerID, sessionToken, duelID, deadline, description }) => {
    window.sessionStorage.setItem('player_id', playerID);
    window.sessionStorage.setItem('username', 'alice');
    window.sessionStorage.setItem('currentGame', JSON.stringify({
      duel_id: duelID,
      deadline,
      time_limit_seconds: 300,
      task: {
        id: '12121212-1212-1212-1212-121212121212',
        title: 'Long Crypto Contract',
        description,
        category: 'crypto',
        difficulty: 'easy',
        time_limit: 300,
        time_limit_seconds: 300,
        task_url: 'https://example.com/task',
        hint_schedule: [],
      },
      opponent_username: 'bob',
    }));
  }, { playerID, sessionToken, duelID, deadline, description });

  await page.routeWebSocket((url) => url.pathname === '/ws', () => {});

  await page.goto('/task');
  await expect(page.getByRole('heading', { name: 'Long Crypto Contract' })).toBeVisible();
  const descriptionNode = page.getByText(description);
  await expect(descriptionNode).toBeVisible();
  await expect.poll(async () => descriptionNode.evaluate((node) => node.scrollWidth <= node.clientWidth + 1)).toBe(true);
});

test('task_assigned accepts non-core task category from websocket guard', async ({ page }) => {
  const playerID = '12121212-1212-1212-1212-121212121212';
  const sessionToken = '10000000-0000-0000-0000-000000000101';
  const duelID = '13131313-1313-1313-1313-131313131313';
  const opponentID = '14141414-1414-1414-1414-141414141414';
  const taskID = '15151515-1515-1515-1515-151515151515';
  const deadline = inSecondsISO(180);

  await page.route('**/api/v1/players/join', async (route) => {
    await route.fulfill({
      status: 200,
      headers: jsonHeaders,
      body: JSON.stringify({
        player_id: playerID,
      }),
    });
  });

  await page.route('**/api/v1/players/me', async (route) => {
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
        ...(page.url().endsWith('/task')
          ? {
              active_duel: {
                id: duelID,
                status: 'active',
                deadline,
                started_at: nowISO(),
              },
            }
          : {}),
      }),
    });
  });

  await page.routeWebSocket((url) => url.pathname === '/ws', (ws) => {
    ws.onMessage((raw) => {
      const message = JSON.parse(String(raw)) as { type: string; payload?: unknown };
      if (message.type !== 'join_queue') {
        return;
      }
      ws.send(JSON.stringify({
        type: 'match_found',
        payload: {
          duel_id: duelID,
          opponent_username: 'bob',
          duel: {
            id: duelID,
            player1_id: playerID,
            player2_id: opponentID,
            status: 'active',
            deadline,
            started_at: nowISO(),
          },
        },
      }));
      ws.send(JSON.stringify({
        type: 'task_assigned',
        payload: {
          duel_id: duelID,
          deadline,
          time_limit_seconds: 180,
          task: {
            id: taskID,
            title: 'OSINT Task Assigned Contract',
            description: 'WS guard must accept backend task categories.',
            category: 'osint',
            difficulty: 'easy',
            time_limit: 180,
            time_limit_seconds: 180,
            task_url: 'https://example.com/task',
            hint_schedule: [],
          },
        },
      }));
    });
  });

  await page.goto('/');
  await page.getByPlaceholder('Введите никнейм...').fill('alice');
  await page.getByRole('button', { name: /ПОДКЛЮЧИТЬСЯ/ }).click();
  await expect(page.getByText('Игрок готов')).toBeVisible();

  await page.getByRole('button', { name: /ИГРАТЬ/ }).click();
  await expect(page).toHaveURL(/\/task$/);
  await expect(page.getByRole('heading', { name: 'OSINT Task Assigned Contract' })).toBeVisible();
  const storedGame = await page.evaluate(() => {
    const raw = window.sessionStorage.getItem('currentGame');
    return raw ? JSON.parse(raw) as { task?: { category?: string } } : null;
  });
  expect(storedGame?.task?.category).toBe('osint');
});

test('duel_resume accepts non-core task category from websocket guard', async ({ page }) => {
  const playerID = '16161616-1616-1616-1616-161616161616';
  const sessionToken = '10000000-0000-0000-0000-000000000102';
  const duelID = '17171717-1717-1717-1717-171717171717';
  const deadline = inSecondsISO(120);
  const resumedDeadline = inSecondsISO(240);
  const task = {
    id: '18181818-1818-1818-1818-181818181818',
    title: 'Initial Resume Contract',
    description: 'Initial stored task.',
    category: 'web',
    difficulty: 'easy',
    time_limit: 120,
    time_limit_seconds: 120,
    task_url: 'https://example.com/task',
    hint_schedule: [],
  };
  const resumedTask = {
    ...task,
    id: '19191919-1919-1919-1919-191919191919',
    title: 'Hardware Resume Contract',
    description: 'Duel resume should update to this backend task.',
    category: 'hardware',
    time_limit_seconds: 240,
    time_limit: 240,
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

  await page.routeWebSocket((url) => url.pathname === '/ws', (ws) => {
    activeSocket = ws;
  });

  await page.goto('/task');
  await expect(page.getByRole('heading', { name: 'Initial Resume Contract' })).toBeVisible();
  await expect.poll(() => activeSocket !== null).toBeTruthy();

  sendServerEvent({
    type: 'duel_resume',
    payload: {
      duel_id: duelID,
      deadline: resumedDeadline,
      opponent_disconnected: false,
      task: resumedTask,
    },
  });

  await expect(page.getByRole('heading', { name: 'Hardware Resume Contract' })).toBeVisible();
  const storedGame = await page.evaluate(() => {
    const raw = window.sessionStorage.getItem('currentGame');
    return raw ? JSON.parse(raw) as {
      deadline?: string;
      time_limit_seconds?: number;
      task?: { category?: string; title?: string };
    } : null;
  });
  expect(storedGame?.deadline).toBe(resumedDeadline);
  expect(storedGame?.time_limit_seconds).toBe(240);
  expect(storedGame?.task?.category).toBe('hardware');
});

test('reconnect pause disables flag submit until duel_resume updates deadline', async ({ page }) => {
  const playerID = 'aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee';
  const sessionToken = '10000000-0000-0000-0000-000000000025';
  const duelID = 'bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb';
  const opponentID = 'cccccccc-cccc-cccc-cccc-cccccccccccc';
  const taskID = 'dddddddd-dddd-dddd-dddd-dddddddddddd';
  const deadline = inSecondsISO(120);
  const resumedDeadline = inSecondsISO(240);
  const task = {
    id: taskID,
    title: 'Reconnect Contract Check',
    description: 'Submit after resume.',
    category: 'web',
    difficulty: 'easy',
    time_limit: 120,
    time_limit_seconds: 120,
    task_url: 'https://example.com/task',
    hint_schedule: [],
  };
  const messages: Array<{ type: string; payload?: unknown }> = [];
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

  await page.routeWebSocket((url) => url.pathname === '/ws', (ws) => {
    activeSocket = ws;
    ws.onMessage((raw) => {
      messages.push(JSON.parse(String(raw)) as { type: string; payload?: unknown });
    });
  });

  await page.goto('/task');
  await expect(page.getByRole('heading', { name: 'Reconnect Contract Check' })).toBeVisible();
  await expect.poll(() => activeSocket !== null).toBeTruthy();

  sendServerEvent({
    type: 'opponent_disconnected',
    payload: {
      duel_id: duelID,
      player_id: opponentID,
      reconnect_deadline: inSecondsISO(30),
    },
  });

  await expect(page.getByRole('heading', { name: 'СОПЕРНИК ОТКЛЮЧИЛСЯ' })).toBeVisible();
  await expect(page.getByPlaceholder('ctf{...}')).toBeDisabled();
  await expect(page.getByRole('button', { name: /Отправить/ })).toBeDisabled();

  sendServerEvent({
    type: 'duel_resume',
    payload: {
      duel_id: duelID,
      deadline: resumedDeadline,
      opponent_disconnected: false,
      task,
    },
  });

  await expect(page.getByRole('heading', { name: 'СОПЕРНИК ОТКЛЮЧИЛСЯ' })).toBeHidden();
  await expect(page.getByPlaceholder('ctf{...}')).toBeEnabled();

  await page.getByPlaceholder('ctf{...}').fill('ctf{resume}');
  await page.getByRole('button', { name: /Отправить/ }).click();

  expect(messages).toContainEqual({
    type: 'flag_submit',
    payload: {
      duel_id: duelID,
      flag: 'ctf{resume}',
    },
  });
  expect(messages).not.toContainEqual({
    type: 'flag_submit',
    payload: {
      duel_id: duelID,
      flag: 'ctf{paused}',
    },
  });
});

test('task reconnect fallback redirects home with stored notification', async ({ page }) => {
  const baseTime = new Date('2026-01-01T00:00:00.000Z');
  await page.clock.install({ time: baseTime });

  const playerID = '89898989-8989-8989-8989-898989898989';
  const sessionToken = '10000000-0000-0000-0000-000000000090';
  const duelID = '90909090-9090-9090-9090-909090909090';
  const opponentID = '91919191-9191-9191-9191-919191919191';
  const deadline = new Date(baseTime.getTime() + 120_000).toISOString();
  const reconnectDeadline = new Date(baseTime.getTime() + 1_000).toISOString();
  const task = {
    id: '92929292-9292-9292-9292-929292929292',
    title: 'Reconnect Timeout Redirect',
    description: 'Fallback timeout should survive the route change.',
    category: 'web',
    difficulty: 'easy',
    time_limit: 120,
    time_limit_seconds: 120,
    task_url: 'https://example.com/task',
    hint_schedule: [],
  };
  let activeSocket: WebSocketRoute | null = null;

  await page.addInitScript(({ playerID, sessionToken, duelID, opponentID, deadline, reconnectDeadline, task }) => {
    window.sessionStorage.setItem('player_id', playerID);
    window.sessionStorage.setItem('username', 'alice');
    window.sessionStorage.setItem('currentGame', JSON.stringify({
      duel_id: duelID,
      deadline,
      time_limit_seconds: 120,
      task,
      opponent_id: opponentID,
      opponent_username: 'bob',
      opponent_disconnected: true,
      opponent_reconnect_deadline: reconnectDeadline,
    }));
  }, { playerID, sessionToken, duelID, opponentID, deadline, reconnectDeadline, task });

  await page.route('**/api/v1/players/me', async (route) => {
    await route.fulfill({
      status: 200,
      headers: jsonHeaders,
      body: JSON.stringify({
        player: {
          id: playerID,
          username: 'alice',
          status: 'in_duel',
          created_at: baseTime.toISOString(),
        },
        active_duel: {
          id: duelID,
          status: 'active',
          deadline,
          started_at: baseTime.toISOString(),
        },
      }),
    });
  });

  await page.routeWebSocket((url) => url.pathname === '/ws', (ws) => {
    activeSocket = ws;
  });

  await page.goto('/task');
  await expect(page.getByRole('heading', { name: 'Reconnect Timeout Redirect' })).toBeVisible();
  await expect(page.getByRole('heading', { name: 'СОПЕРНИК ОТКЛЮЧИЛСЯ' })).toBeVisible();
  await expect.poll(() => activeSocket !== null).toBeTruthy();

  await page.clock.fastForward(6_100);

  await expect(page).toHaveURL(/\/$/);
  await expect(page.getByText('Соперник не вернулся вовремя. Дуэль закрыта.')).toBeVisible();
  await expect.poll(() => page.evaluate(() => window.sessionStorage.getItem('currentGame'))).toBeNull();
});

test('task page sends surrender payload and waits for duel_finished result', async ({ page }) => {
  const playerID = '71717171-7171-7171-7171-717171717171';
  const sessionToken = '10000000-0000-0000-0000-000000000026';
  const duelID = '72727272-7272-7272-7272-727272727272';
  const opponentID = '73737373-7373-7373-7373-737373737373';
  const deadline = inSecondsISO(120);
  const task = {
    id: '74747474-7474-7474-7474-747474747474',
    title: 'Surrender Contract Check',
    description: 'Surrender should wait for backend result.',
    category: 'web',
    difficulty: 'easy',
    time_limit: 120,
    time_limit_seconds: 120,
    task_url: 'https://example.com/task',
    hint_schedule: [],
  };
  const messages: Array<{ type: string; payload?: unknown }> = [];

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

  await page.routeWebSocket((url) => url.pathname === '/ws', (ws) => {
    ws.onMessage((raw) => {
      const message = JSON.parse(String(raw)) as { type: string; payload?: unknown };
      messages.push(message);
      if (message.type !== 'surrender') {
        return;
      }
      ws.send(JSON.stringify({
        type: 'duel_finished',
        payload: {
          duel_id: duelID,
          winner_id: opponentID,
          winner_username: 'bob',
          your_solved: false,
          opponent_solved: false,
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
      }));
    });
  });

  await page.goto('/task');
  await expect(page.getByRole('heading', { name: 'Surrender Contract Check' })).toBeVisible();

  page.once('dialog', async (dialog) => {
    expect(dialog.message()).toContain('Сдаться');
    await dialog.accept();
  });
  await page.getByRole('button', { name: 'Сдаться' }).click();

  await expect(page.getByText('ПОРАЖЕНИЕ')).toBeVisible();
  await expect.poll(() => page.evaluate(() => window.sessionStorage.getItem('player_id'))).toBeNull();
  await expect.poll(() => page.evaluate(() => window.sessionStorage.getItem('session_token'))).toBeNull();
  await expect.poll(() => page.evaluate(() => window.sessionStorage.getItem('username'))).toBeNull();
  await expect.poll(() => page.evaluate(() => window.sessionStorage.getItem('currentGame'))).toBeNull();
  await expect.poll(() => page.evaluate(() => window.sessionStorage.getItem('game_result'))).toContain(duelID);
  expect(messages).toContainEqual({
    type: 'surrender',
    payload: {
      duel_id: duelID,
    },
  });
});

test('task page allows surrender while duel is paused', async ({ page }) => {
  const playerID = '75757575-7575-7575-7575-757575757575';
  const sessionToken = '10000000-0000-0000-0000-000000000027';
  const duelID = '76767676-7676-7676-7676-767676767676';
  const opponentID = '77777777-7777-7777-7777-777777777777';
  const deadline = inSecondsISO(120);
  const task = {
    id: '78787878-7878-7878-7878-787878787878',
    title: 'Paused Surrender Contract Check',
    description: 'Surrender should work while flag submit is paused.',
    category: 'web',
    difficulty: 'easy',
    time_limit: 120,
    time_limit_seconds: 120,
    task_url: 'https://example.com/task',
    hint_schedule: [],
  };
  const messages: Array<{ type: string; payload?: unknown }> = [];
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

  await page.routeWebSocket((url) => url.pathname === '/ws', (ws) => {
    activeSocket = ws;
    ws.onMessage((raw) => {
      const message = JSON.parse(String(raw)) as { type: string; payload?: unknown };
      messages.push(message);
      if (message.type !== 'surrender') {
        return;
      }
      ws.send(JSON.stringify({
        type: 'duel_finished',
        payload: {
          duel_id: duelID,
          winner_id: opponentID,
          winner_username: 'bob',
          your_solved: false,
          opponent_solved: false,
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
      }));
    });
  });

  await page.goto('/task');
  await expect(page.getByRole('heading', { name: 'Paused Surrender Contract Check' })).toBeVisible();
  await expect.poll(() => activeSocket !== null).toBeTruthy();

  sendServerEvent({
    type: 'opponent_disconnected',
    payload: {
      duel_id: duelID,
      player_id: opponentID,
      reconnect_deadline: inSecondsISO(30),
    },
  });

  await expect(page.getByRole('heading', { name: 'СОПЕРНИК ОТКЛЮЧИЛСЯ' })).toBeVisible();
  await expect(page.getByRole('button', { name: /Отправить/ })).toBeDisabled();

  page.once('dialog', async (dialog) => {
    await dialog.accept();
  });
  await page.getByRole('button', { name: 'Сдаться' }).first().click();

  await expect(page.getByRole('heading', { name: 'СОПЕРНИК ОТКЛЮЧИЛСЯ' })).toBeHidden();
  await expect(page.getByText('ПОРАЖЕНИЕ')).toBeVisible();
  expect(messages).toContainEqual({
    type: 'surrender',
    payload: {
      duel_id: duelID,
    },
  });
});

test('task page ignores late websocket error after duel_finished', async ({ page }) => {
  await installWebSocketErrorProbe(page);

  const playerID = 'c7c7c7c7-c7c7-c7c7-c7c7-c7c7c7c7c7c7';
  const sessionToken = '10000000-0000-0000-0000-000000000028';
  const duelID = 'c8c8c8c8-c8c8-c8c8-c8c8-c8c8c8c8c8c8';
  const opponentID = 'c9c9c9c9-c9c9-c9c9-c9c9-c9c9c9c9c9c9';
  const deadline = inSecondsISO(120);
  const task = {
    id: 'cacacaca-caca-caca-caca-cacacacacaca',
    title: 'Late Error Finished Task',
    description: 'Terminal UI must ignore socket errors after finish.',
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

  await page.routeWebSocket((url) => url.pathname === '/ws', (ws) => {
    activeSocket = ws;
  });

  await page.goto('/task');
  await expect(page.getByRole('heading', { name: 'Late Error Finished Task' })).toBeVisible();
  await expect.poll(() => activeSocket !== null).toBeTruthy();

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
  const dispatched = await page.evaluate(() => {
    const testWindow = window as unknown as WebSocketErrorProbeWindow;
    return testWindow.__dispatchLastWebSocketError();
  });

  expect(dispatched).toBe(true);
  await page.waitForTimeout(150);
  await expect(page.getByText('ПОБЕДА!')).toBeVisible();
  await expect(page.getByText('Ошибка WebSocket соединения')).toBeHidden();
});

test('task page ignores late websocket error after duel_expired', async ({ page }) => {
  await installWebSocketErrorProbe(page);

  const playerID = 'cbcbcbcb-cbcb-cbcb-cbcb-cbcbcbcbcbcb';
  const sessionToken = '10000000-0000-0000-0000-000000000029';
  const duelID = 'cdcdcdcd-cdcd-cdcd-cdcd-cdcdcdcdcdcd';
  const deadline = inSecondsISO(120);
  const task = {
    id: 'cececece-cece-cece-cece-cececececece',
    title: 'Late Error Expired Task',
    description: 'Timeup UI must ignore socket errors after expiration.',
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

  await page.routeWebSocket((url) => url.pathname === '/ws', (ws) => {
    activeSocket = ws;
  });

  await page.goto('/task');
  await expect(page.getByRole('heading', { name: 'Late Error Expired Task' })).toBeVisible();
  await expect.poll(() => activeSocket !== null).toBeTruthy();

  sendServerEvent({
    type: 'duel_expired',
    payload: {
      duel_id: duelID,
    },
  });

  await expect(page.getByText('ВРЕМЯ ВЫШЛО!')).toBeVisible();
  const dispatched = await page.evaluate(() => {
    const testWindow = window as unknown as WebSocketErrorProbeWindow;
    return testWindow.__dispatchLastWebSocketError();
  });

  expect(dispatched).toBe(true);
  await page.waitForTimeout(150);
  await expect(page.getByText('ВРЕМЯ ВЫШЛО!')).toBeVisible();
  await expect(page.getByText('Ошибка WebSocket соединения')).toBeHidden();
});

test('server duel_resume recovers task page from local timer timeup', async ({ page }) => {
  const playerID = 'fafafafa-fafa-fafa-fafa-fafafafafafa';
  const sessionToken = '10000000-0000-0000-0000-000000000077';
  const duelID = 'fbfbfbfb-fbfb-fbfb-fbfb-fbfbfbfbfbfb';
  const pastDeadline = new Date(Date.now() - 1000).toISOString();
  const futureDeadline = inSecondsISO(120);
  const task = {
    id: 'fcfcfcfc-fcfc-fcfc-fcfc-fcfcfcfcfcfc',
    title: 'Local Timeup Resume Recovery',
    description: 'Server active state should override local timer drift.',
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

  await page.addInitScript(({ playerID, sessionToken, duelID, pastDeadline, task }) => {
    window.sessionStorage.setItem('player_id', playerID);
    window.sessionStorage.setItem('username', 'alice');
    window.sessionStorage.setItem('currentGame', JSON.stringify({
      duel_id: duelID,
      deadline: pastDeadline,
      time_limit_seconds: 120,
      task,
      opponent_username: 'bob',
    }));
  }, { playerID, sessionToken, duelID, pastDeadline, task });

  await page.routeWebSocket((url) => url.pathname === '/ws', (ws) => {
    activeSocket = ws;
  });

  await page.goto('/task');
  await expect(page.getByRole('heading', { name: 'Local Timeup Resume Recovery' })).toBeVisible();
  await expect.poll(() => activeSocket !== null).toBeTruthy();
  await expect(page.getByText('ВРЕМЯ ВЫШЛО!')).toBeVisible();

  sendServerEvent({
    type: 'duel_resume',
    payload: {
      duel_id: duelID,
      deadline: futureDeadline,
      opponent_disconnected: false,
      task,
    },
  });

  await expect(page.getByText('ВРЕМЯ ВЫШЛО!')).toBeHidden();
  await expect(page.getByPlaceholder('ctf{...}')).toBeEnabled();
  await expect.poll(() => page.evaluate(() => window.sessionStorage.getItem('game_result'))).toBeNull();
  const storedGame = await page.evaluate(() => {
    const raw = window.sessionStorage.getItem('currentGame');
    return raw ? JSON.parse(raw) as { deadline?: string } : null;
  });
  expect(storedGame?.deadline).toBe(futureDeadline);
});

test('server opponent_reconnected recovers task page from local timer timeup', async ({ page }) => {
  const playerID = 'abababab-abab-abab-abab-abababababab';
  const sessionToken = '10000000-0000-0000-0000-000000000078';
  const duelID = 'acacacac-acac-acac-acac-acacacacacac';
  const opponentID = 'adadadad-adad-adad-adad-adadadadadad';
  const pastDeadline = new Date(Date.now() - 1000).toISOString();
  const futureDeadline = inSecondsISO(120);
  const task = {
    id: 'aeaeaeae-aeae-aeae-aeae-aeaeaeaeaeae',
    title: 'Local Timeup Reconnect Recovery',
    description: 'Opponent reconnect should restore active UI after clock drift.',
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

  await page.addInitScript(({ playerID, sessionToken, duelID, pastDeadline, task }) => {
    window.sessionStorage.setItem('player_id', playerID);
    window.sessionStorage.setItem('username', 'alice');
    window.sessionStorage.setItem('currentGame', JSON.stringify({
      duel_id: duelID,
      deadline: pastDeadline,
      time_limit_seconds: 120,
      task,
      opponent_username: 'bob',
    }));
  }, { playerID, sessionToken, duelID, pastDeadline, task });

  await page.routeWebSocket((url) => url.pathname === '/ws', (ws) => {
    activeSocket = ws;
  });

  await page.goto('/task');
  await expect(page.getByRole('heading', { name: 'Local Timeup Reconnect Recovery' })).toBeVisible();
  await expect.poll(() => activeSocket !== null).toBeTruthy();
  await expect(page.getByText('ВРЕМЯ ВЫШЛО!')).toBeVisible();

  sendServerEvent({
    type: 'opponent_reconnected',
    payload: {
      duel_id: duelID,
      player_id: opponentID,
      deadline: futureDeadline,
    },
  });

  await expect(page.getByText('ВРЕМЯ ВЫШЛО!')).toBeHidden();
  await expect(page.getByText('Соперник вернулся. Дуэль продолжается.')).toBeVisible();
  await expect(page.getByPlaceholder('ctf{...}')).toBeEnabled();
  await expect.poll(() => page.evaluate(() => window.sessionStorage.getItem('game_result'))).toBeNull();
});

test('server opponent_disconnected replaces local timer timeup with pause UI', async ({ page }) => {
  const playerID = 'babababa-baba-baba-baba-babababababa';
  const sessionToken = '10000000-0000-0000-0000-000000000079';
  const duelID = 'bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb';
  const opponentID = 'bcbcbcbc-bcbc-bcbc-bcbc-bcbcbcbcbcbc';
  const pastDeadline = new Date(Date.now() - 1000).toISOString();
  const reconnectDeadline = inSecondsISO(60);
  const task = {
    id: 'bdbdbdbd-bdbd-bdbd-bdbd-bdbdbdbdbdbd',
    title: 'Local Timeup Pause Recovery',
    description: 'Opponent disconnect should show pause instead of provisional timeup.',
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

  await page.addInitScript(({ playerID, sessionToken, duelID, pastDeadline, task }) => {
    window.sessionStorage.setItem('player_id', playerID);
    window.sessionStorage.setItem('username', 'alice');
    window.sessionStorage.setItem('currentGame', JSON.stringify({
      duel_id: duelID,
      deadline: pastDeadline,
      time_limit_seconds: 120,
      task,
      opponent_username: 'bob',
    }));
  }, { playerID, sessionToken, duelID, pastDeadline, task });

  await page.routeWebSocket((url) => url.pathname === '/ws', (ws) => {
    activeSocket = ws;
  });

  await page.goto('/task');
  await expect(page.getByRole('heading', { name: 'Local Timeup Pause Recovery' })).toBeVisible();
  await expect.poll(() => activeSocket !== null).toBeTruthy();
  await expect(page.getByText('ВРЕМЯ ВЫШЛО!')).toBeVisible();

  sendServerEvent({
    type: 'opponent_disconnected',
    payload: {
      duel_id: duelID,
      player_id: opponentID,
      reconnect_deadline: reconnectDeadline,
    },
  });

  await expect(page.getByText('ВРЕМЯ ВЫШЛО!')).toBeHidden();
  await expect(page.getByRole('heading', { name: 'СОПЕРНИК ОТКЛЮЧИЛСЯ' })).toBeVisible();
  await expect(page.getByPlaceholder('ctf{...}')).toBeDisabled();
  await expect.poll(() => page.evaluate(() => window.sessionStorage.getItem('game_result'))).toBeNull();
  const storedGame = await page.evaluate(() => {
    const raw = window.sessionStorage.getItem('currentGame');
    return raw
      ? JSON.parse(raw) as {
          opponent_disconnected?: boolean;
          opponent_reconnect_deadline?: string;
        }
      : null;
  });
  expect(storedGame?.opponent_disconnected).toBe(true);
  expect(storedGame?.opponent_reconnect_deadline).toBe(reconnectDeadline);
});

test('task page ignores same-duel hint_unlocked for another task', async ({ page }) => {
  const playerID = 'c3c3c3c3-c3c3-c3c3-c3c3-c3c3c3c3c3c3';
  const sessionToken = '10000000-0000-0000-0000-000000000081';
  const duelID = 'c4c4c4c4-c4c4-c4c4-c4c4-c4c4c4c4c4c4';
  const wrongTaskID = 'c5c5c5c5-c5c5-c5c5-c5c5-c5c5c5c5c5c5';
  const deadline = inSecondsISO(120);
  const task = {
    id: 'c6c6c6c6-c6c6-c6c6-c6c6-c6c6c6c6c6c6',
    title: 'Hint Task Ownership Guard',
    description: 'Hints must belong to the current player task.',
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

  await page.routeWebSocket((url) => url.pathname === '/ws', (ws) => {
    activeSocket = ws;
  });

  await page.goto('/task');
  await expect(page.getByRole('heading', { name: 'Hint Task Ownership Guard' })).toBeVisible();
  await expect.poll(() => activeSocket !== null).toBeTruthy();

  activeSocket!.send(JSON.stringify({
    type: 'hint_unlocked',
    payload: {
      duel_id: duelID,
      task_id: wrongTaskID,
      hint_index: 1,
      hint: 'Wrong task hint must not render',
      unlocked_at: nowISO(),
    },
  }));

  await page.waitForTimeout(150);
  await expect(page.getByText('Wrong task hint must not render')).toBeHidden();
  await expect(page.getByPlaceholder('ctf{...}')).toBeEnabled();
  await expect(page.getByRole('heading', { name: 'ВРЕМЯ ВЫШЛО!' })).toBeHidden();
  await expect(page.getByRole('heading', { name: 'СОПЕРНИК ОТКЛЮЧИЛСЯ' })).toBeHidden();
  await expect.poll(() => page.evaluate(() => window.sessionStorage.getItem('game_result'))).toBeNull();
  const storedGame = await page.evaluate(() => {
    const raw = window.sessionStorage.getItem('currentGame');
    return raw ? JSON.parse(raw) as { deadline?: string; task?: { id?: string } } : null;
  });
  expect(storedGame?.deadline).toBe(deadline);
  expect(storedGame?.task?.id).toBe(task.id);
  expect(pageErrors).toEqual([]);
});

test('wrong-task hint_unlocked does not recover task page from local timer timeup', async ({ page }) => {
  const baseTime = new Date('2026-01-01T00:00:00.000Z');
  await page.clock.install({ time: baseTime });

  const playerID = 'c7c7c7c7-c7c7-c7c7-c7c7-c7c7c7c7c7c7';
  const sessionToken = '10000000-0000-0000-0000-000000000082';
  const duelID = 'c8c8c8c8-c8c8-c8c8-c8c8-c8c8c8c8c8c8';
  const wrongTaskID = 'c9c9c9c9-c9c9-c9c9-c9c9-c9c9c9c9c9c9';
  const deadline = new Date(baseTime.getTime() + 1000).toISOString();
  const task = {
    id: 'cacacaca-caca-caca-caca-cacacacacaca',
    title: 'Wrong Task Hint Local Timeup Guard',
    description: 'Wrong task hints must not revive provisional timeup state.',
    category: 'web',
    difficulty: 'easy',
    time_limit: 120,
    time_limit_seconds: 120,
    task_url: 'https://example.com/task',
    hint_schedule: [],
  };
  let activeSocket: WebSocketRoute | null = null;

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

  await page.routeWebSocket((url) => url.pathname === '/ws', (ws) => {
    activeSocket = ws;
  });

  await page.goto('/task');
  await expect(page.getByRole('heading', { name: 'Wrong Task Hint Local Timeup Guard' })).toBeVisible();
  await expect.poll(() => activeSocket !== null).toBeTruthy();
  await page.clock.fastForward(1100);
  await expect(page.getByText('ВРЕМЯ ВЫШЛО!')).toBeVisible();

  activeSocket!.send(JSON.stringify({
    type: 'hint_unlocked',
    payload: {
      duel_id: duelID,
      task_id: wrongTaskID,
      hint_index: 1,
      hint: 'Wrong task hint after timeup',
      unlocked_at: nowISO(),
    },
  }));

  await page.waitForTimeout(150);
  await expect(page.getByText('ВРЕМЯ ВЫШЛО!')).toBeVisible();
  await expect(page.getByText('Wrong task hint after timeup')).toBeHidden();
  const storedResult = await page.evaluate(() => {
    const raw = window.sessionStorage.getItem('game_result');
    return raw ? JSON.parse(raw) as { state?: string; source?: string; duel_id?: string } : null;
  });
  expect(storedResult).toMatchObject({
    state: 'timeup',
    source: 'local_timer',
    duel_id: duelID,
  });
});

test('same-task hint_unlocked still reveals current task hint while playing', async ({ page }) => {
  const playerID = 'cbcbcbcb-cbcb-cbcb-cbcb-cbcbcbcbcbcb';
  const sessionToken = '10000000-0000-0000-0000-000000000083';
  const duelID = 'cccccccc-cccc-cccc-cccc-cccccccccccc';
  const deadline = inSecondsISO(120);
  const task = {
    id: 'cdcdcdcd-cdcd-cdcd-cdcd-cdcdcdcdcdcd',
    title: 'Same Task Hint Ownership Guard',
    description: 'Same task hints should keep revealing normally.',
    category: 'web',
    difficulty: 'easy',
    time_limit: 120,
    time_limit_seconds: 120,
    task_url: 'https://example.com/task',
    hint_schedule: [],
  };
  let activeSocket: WebSocketRoute | null = null;

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

  await page.routeWebSocket((url) => url.pathname === '/ws', (ws) => {
    activeSocket = ws;
  });

  await page.goto('/task');
  await expect(page.getByRole('heading', { name: 'Same Task Hint Ownership Guard' })).toBeVisible();
  await expect.poll(() => activeSocket !== null).toBeTruthy();

  activeSocket!.send(JSON.stringify({
    type: 'hint_unlocked',
    payload: {
      duel_id: duelID,
      task_id: task.id,
      hint_index: 1,
      hint: 'Same task hint while playing',
      unlocked_at: nowISO(),
    },
  }));

  await expect(page.getByText('Same task hint while playing')).toBeVisible();
  await expect(page.getByText('ВРЕМЯ ВЫШЛО!')).toBeHidden();
  await expect(page.getByPlaceholder('ctf{...}')).toBeEnabled();
  await expect.poll(() => page.evaluate(() => window.sessionStorage.getItem('game_result'))).toBeNull();
});

test('task page ignores self opponent_solved but accepts opponent solved event', async ({ page }) => {
  const playerID = 'd0d0d0d0-d0d0-d0d0-d0d0-d0d0d0d0d0d0';
  const sessionToken = '10000000-0000-0000-0000-000000000084';
  const duelID = 'd1d1d1d1-d1d1-d1d1-d1d1-d1d1d1d1d1d1';
  const opponentID = 'd2d2d2d2-d2d2-d2d2-d2d2-d2d2d2d2d2d2';
  const deadline = inSecondsISO(120);
  const task = {
    id: 'd3d3d3d3-d3d3-d3d3-d3d3-d3d3d3d3d3d3',
    title: 'Opponent Solved Identity Guard',
    description: 'Self player events must not be shown as opponent progress.',
    category: 'web',
    difficulty: 'easy',
    time_limit: 120,
    time_limit_seconds: 120,
    task_url: 'https://example.com/task',
    hint_schedule: [],
  };
  let activeSocket: WebSocketRoute | null = null;

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

  await page.routeWebSocket((url) => url.pathname === '/ws', (ws) => {
    activeSocket = ws;
  });

  await page.goto('/task');
  await expect(page.getByRole('heading', { name: 'Opponent Solved Identity Guard' })).toBeVisible();
  await expect.poll(() => activeSocket !== null).toBeTruthy();

  activeSocket!.send(JSON.stringify({
    type: 'opponent_solved',
    payload: {
      duel_id: duelID,
      player_id: playerID,
    },
  }));

  await page.waitForTimeout(150);
  await expect(page.getByText('Соперник уже решил задание. Ждем завершение дуэли...')).toBeHidden();
  await expect(page.getByPlaceholder('ctf{...}')).toBeEnabled();
  await expect.poll(() => page.evaluate(() => window.sessionStorage.getItem('game_result'))).toBeNull();

  activeSocket!.send(JSON.stringify({
    type: 'opponent_solved',
    payload: {
      duel_id: duelID,
      player_id: opponentID,
    },
  }));

  await expect(page.getByText('Соперник уже решил задание. Ждем завершение дуэли...')).toBeVisible();
});

test('task page ignores self opponent disconnect and reconnect while playing', async ({ page }) => {
  const playerID = 'd4d4d4d4-d4d4-d4d4-d4d4-d4d4d4d4d4d4';
  const sessionToken = '10000000-0000-0000-0000-000000000085';
  const duelID = 'd5d5d5d5-d5d5-d5d5-d5d5-d5d5d5d5d5d5';
  const deadline = inSecondsISO(120);
  const laterDeadline = inSecondsISO(240);
  const task = {
    id: 'd6d6d6d6-d6d6-d6d6-d6d6-d6d6d6d6d6d6',
    title: 'Opponent Pause Identity Guard',
    description: 'Self pause events must not mutate opponent UI state.',
    category: 'web',
    difficulty: 'easy',
    time_limit: 120,
    time_limit_seconds: 120,
    task_url: 'https://example.com/task',
    hint_schedule: [],
  };
  let activeSocket: WebSocketRoute | null = null;

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

  await page.routeWebSocket((url) => url.pathname === '/ws', (ws) => {
    activeSocket = ws;
  });

  await page.goto('/task');
  await expect(page.getByRole('heading', { name: 'Opponent Pause Identity Guard' })).toBeVisible();
  await expect.poll(() => activeSocket !== null).toBeTruthy();

  activeSocket!.send(JSON.stringify({
    type: 'opponent_disconnected',
    payload: {
      duel_id: duelID,
      player_id: playerID,
      reconnect_deadline: inSecondsISO(60),
    },
  }));
  activeSocket!.send(JSON.stringify({
    type: 'opponent_reconnected',
    payload: {
      duel_id: duelID,
      player_id: playerID,
      deadline: laterDeadline,
    },
  }));

  await page.waitForTimeout(150);
  await expect(page.getByRole('heading', { name: 'СОПЕРНИК ОТКЛЮЧИЛСЯ' })).toBeHidden();
  await expect(page.getByText('Соперник отключился. Отправка флага временно недоступна.')).toBeHidden();
  await expect(page.getByText('Соперник вернулся. Дуэль продолжается.')).toBeHidden();
  await expect(page.getByPlaceholder('ctf{...}')).toBeEnabled();

  const storedGame = await page.evaluate(() => {
    const raw = window.sessionStorage.getItem('currentGame');
    return raw
      ? JSON.parse(raw) as {
          deadline?: string;
          opponent_disconnected?: boolean;
          opponent_reconnect_deadline?: string;
        }
      : null;
  });
  expect(storedGame?.deadline).toBe(deadline);
  expect(storedGame?.opponent_disconnected).toBeUndefined();
  expect(storedGame?.opponent_reconnect_deadline).toBeUndefined();
});

test('self opponent events do not recover task page from local timer timeup', async ({ page }) => {
  const baseTime = new Date('2026-01-01T00:00:00.000Z');
  await page.clock.install({ time: baseTime });

  const playerID = 'd7d7d7d7-d7d7-d7d7-d7d7-d7d7d7d7d7d7';
  const sessionToken = '10000000-0000-0000-0000-000000000086';
  const duelID = 'd8d8d8d8-d8d8-d8d8-d8d8-d8d8d8d8d8d8';
  const deadline = new Date(baseTime.getTime() + 1000).toISOString();
  const laterDeadline = new Date(baseTime.getTime() + 120_000).toISOString();
  const task = {
    id: 'd9d9d9d9-d9d9-d9d9-d9d9-d9d9d9d9d9d9',
    title: 'Opponent Identity Local Timeup Guard',
    description: 'Self opponent events must not revive provisional timeup state.',
    category: 'web',
    difficulty: 'easy',
    time_limit: 120,
    time_limit_seconds: 120,
    task_url: 'https://example.com/task',
    hint_schedule: [],
  };
  let activeSocket: WebSocketRoute | null = null;

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

  await page.routeWebSocket((url) => url.pathname === '/ws', (ws) => {
    activeSocket = ws;
  });

  await page.goto('/task');
  await expect(page.getByRole('heading', { name: 'Opponent Identity Local Timeup Guard' })).toBeVisible();
  await expect.poll(() => activeSocket !== null).toBeTruthy();
  await page.clock.fastForward(1100);
  await expect(page.getByText('ВРЕМЯ ВЫШЛО!')).toBeVisible();

  activeSocket!.send(JSON.stringify({
    type: 'opponent_solved',
    payload: {
      duel_id: duelID,
      player_id: playerID,
    },
  }));
  activeSocket!.send(JSON.stringify({
    type: 'opponent_reconnected',
    payload: {
      duel_id: duelID,
      player_id: playerID,
      deadline: laterDeadline,
    },
  }));
  activeSocket!.send(JSON.stringify({
    type: 'opponent_disconnected',
    payload: {
      duel_id: duelID,
      player_id: playerID,
      reconnect_deadline: laterDeadline,
    },
  }));

  await page.waitForTimeout(150);
  await expect(page.getByText('ВРЕМЯ ВЫШЛО!')).toBeVisible();
  await expect(page.getByText('Соперник уже решил задание. Ждем завершение дуэли...')).toBeHidden();
  await expect(page.getByText('Соперник вернулся. Дуэль продолжается.')).toBeHidden();
  await expect(page.getByRole('heading', { name: 'СОПЕРНИК ОТКЛЮЧИЛСЯ' })).toBeHidden();

  const storedResult = await page.evaluate(() => {
    const raw = window.sessionStorage.getItem('game_result');
    return raw ? JSON.parse(raw) as { state?: string; source?: string; duel_id?: string } : null;
  });
  expect(storedResult).toMatchObject({
    state: 'timeup',
    source: 'local_timer',
    duel_id: duelID,
  });

  const storedGame = await page.evaluate(() => {
    const raw = window.sessionStorage.getItem('currentGame');
    return raw
      ? JSON.parse(raw) as {
          deadline?: string;
          opponent_disconnected?: boolean;
          opponent_reconnect_deadline?: string;
        }
      : null;
  });
  expect(storedGame?.deadline).toBe(deadline);
  expect(storedGame?.opponent_disconnected).toBe(false);
  expect(storedGame?.opponent_reconnect_deadline).toBeUndefined();
});

test('task page ignores third-party opponent events when opponent id is known', async ({ page }) => {
  const playerID = 'e1e1e1e1-e1e1-e1e1-e1e1-e1e1e1e1e1e1';
  const sessionToken = '10000000-0000-0000-0000-000000000087';
  const duelID = 'e2e2e2e2-e2e2-e2e2-e2e2-e2e2e2e2e2e2';
  const opponentID = 'e3e3e3e3-e3e3-e3e3-e3e3-e3e3e3e3e3e3';
  const thirdPlayerID = 'e4e4e4e4-e4e4-e4e4-e4e4-e4e4e4e4e4e4';
  const deadline = inSecondsISO(120);
  const laterDeadline = inSecondsISO(240);
  const task = {
    id: 'e5e5e5e5-e5e5-e5e5-e5e5-e5e5e5e5e5e5',
    title: 'Opponent Exact Identity Guard',
    description: 'Third player events must not affect this duel UI.',
    category: 'web',
    difficulty: 'easy',
    time_limit: 120,
    time_limit_seconds: 120,
    task_url: 'https://example.com/task',
    hint_schedule: [],
  };
  let activeSocket: WebSocketRoute | null = null;

  await page.addInitScript(({ playerID, sessionToken, duelID, opponentID, deadline, task }) => {
    window.sessionStorage.setItem('player_id', playerID);
    window.sessionStorage.setItem('username', 'alice');
    window.sessionStorage.setItem('currentGame', JSON.stringify({
      duel_id: duelID,
      deadline,
      time_limit_seconds: 120,
      task,
      opponent_username: 'bob',
      opponent_id: opponentID,
    }));
  }, { playerID, sessionToken, duelID, opponentID, deadline, task });

  await page.routeWebSocket((url) => url.pathname === '/ws', (ws) => {
    activeSocket = ws;
  });

  await page.goto('/task');
  await expect(page.getByRole('heading', { name: 'Opponent Exact Identity Guard' })).toBeVisible();
  await expect.poll(() => activeSocket !== null).toBeTruthy();

  activeSocket!.send(JSON.stringify({
    type: 'opponent_solved',
    payload: {
      duel_id: duelID,
      player_id: thirdPlayerID,
    },
  }));
  activeSocket!.send(JSON.stringify({
    type: 'opponent_disconnected',
    payload: {
      duel_id: duelID,
      player_id: thirdPlayerID,
      reconnect_deadline: inSecondsISO(60),
    },
  }));
  activeSocket!.send(JSON.stringify({
    type: 'opponent_reconnected',
    payload: {
      duel_id: duelID,
      player_id: thirdPlayerID,
      deadline: laterDeadline,
    },
  }));

  await page.waitForTimeout(150);
  await expect(page.getByText('Соперник уже решил задание. Ждем завершение дуэли...')).toBeHidden();
  await expect(page.getByRole('heading', { name: 'СОПЕРНИК ОТКЛЮЧИЛСЯ' })).toBeHidden();
  await expect(page.getByText('Соперник вернулся. Дуэль продолжается.')).toBeHidden();
  await expect(page.getByPlaceholder('ctf{...}')).toBeEnabled();

  const storedAfterThirdParty = await page.evaluate(() => {
    const raw = window.sessionStorage.getItem('currentGame');
    return raw
      ? JSON.parse(raw) as {
          deadline?: string;
          opponent_disconnected?: boolean;
          opponent_id?: string;
        }
      : null;
  });
  expect(storedAfterThirdParty?.deadline).toBe(deadline);
  expect(storedAfterThirdParty?.opponent_disconnected).toBeUndefined();
  expect(storedAfterThirdParty?.opponent_id).toBe(opponentID);

  activeSocket!.send(JSON.stringify({
    type: 'opponent_disconnected',
    payload: {
      duel_id: duelID,
      player_id: opponentID,
      reconnect_deadline: inSecondsISO(60),
    },
  }));

  await expect(page.getByRole('heading', { name: 'СОПЕРНИК ОТКЛЮЧИЛСЯ' })).toBeVisible();

  activeSocket!.send(JSON.stringify({
    type: 'opponent_reconnected',
    payload: {
      duel_id: duelID,
      player_id: opponentID,
      deadline: laterDeadline,
    },
  }));

  await expect(page.getByRole('heading', { name: 'СОПЕРНИК ОТКЛЮЧИЛСЯ' })).toBeHidden();
  await expect(page.getByText('Соперник вернулся. Дуэль продолжается.')).toBeVisible();
  await expect.poll(async () => {
    const raw = await page.evaluate(() => window.sessionStorage.getItem('currentGame'));
    return raw ? (JSON.parse(raw) as { deadline?: string }).deadline : null;
  }).toBe(laterDeadline);
});

test('third-party opponent events do not recover task page from local timer timeup when opponent id is known', async ({ page }) => {
  const baseTime = new Date('2026-01-01T00:00:00.000Z');
  await page.clock.install({ time: baseTime });

  const playerID = 'e6e6e6e6-e6e6-e6e6-e6e6-e6e6e6e6e6e6';
  const sessionToken = '10000000-0000-0000-0000-000000000088';
  const duelID = 'e7e7e7e7-e7e7-e7e7-e7e7-e7e7e7e7e7e7';
  const opponentID = 'e8e8e8e8-e8e8-e8e8-e8e8-e8e8e8e8e8e8';
  const thirdPlayerID = 'e9e9e9e9-e9e9-e9e9-e9e9-e9e9e9e9e9e9';
  const deadline = new Date(baseTime.getTime() + 1000).toISOString();
  const laterDeadline = new Date(baseTime.getTime() + 120_000).toISOString();
  const task = {
    id: 'eaeaeaea-eaea-eaea-eaea-eaeaeaeaeaea',
    title: 'Opponent Exact Identity Local Timeup Guard',
    description: 'Third player events must not revive local timer result.',
    category: 'web',
    difficulty: 'easy',
    time_limit: 120,
    time_limit_seconds: 120,
    task_url: 'https://example.com/task',
    hint_schedule: [],
  };
  let activeSocket: WebSocketRoute | null = null;

  await page.addInitScript(({ playerID, sessionToken, duelID, opponentID, deadline, task }) => {
    window.sessionStorage.setItem('player_id', playerID);
    window.sessionStorage.setItem('username', 'alice');
    window.sessionStorage.setItem('currentGame', JSON.stringify({
      duel_id: duelID,
      deadline,
      time_limit_seconds: 120,
      task,
      opponent_username: 'bob',
      opponent_id: opponentID,
    }));
  }, { playerID, sessionToken, duelID, opponentID, deadline, task });

  await page.routeWebSocket((url) => url.pathname === '/ws', (ws) => {
    activeSocket = ws;
  });

  await page.goto('/task');
  await expect(page.getByRole('heading', { name: 'Opponent Exact Identity Local Timeup Guard' })).toBeVisible();
  await expect.poll(() => activeSocket !== null).toBeTruthy();
  await page.clock.fastForward(1100);
  await expect(page.getByText('ВРЕМЯ ВЫШЛО!')).toBeVisible();

  activeSocket!.send(JSON.stringify({
    type: 'opponent_solved',
    payload: {
      duel_id: duelID,
      player_id: thirdPlayerID,
    },
  }));
  activeSocket!.send(JSON.stringify({
    type: 'opponent_reconnected',
    payload: {
      duel_id: duelID,
      player_id: thirdPlayerID,
      deadline: laterDeadline,
    },
  }));
  activeSocket!.send(JSON.stringify({
    type: 'opponent_disconnected',
    payload: {
      duel_id: duelID,
      player_id: thirdPlayerID,
      reconnect_deadline: laterDeadline,
    },
  }));

  await page.waitForTimeout(150);
  await expect(page.getByText('ВРЕМЯ ВЫШЛО!')).toBeVisible();
  await expect(page.getByText('Соперник уже решил задание. Ждем завершение дуэли...')).toBeHidden();
  await expect(page.getByText('Соперник вернулся. Дуэль продолжается.')).toBeHidden();
  await expect(page.getByRole('heading', { name: 'СОПЕРНИК ОТКЛЮЧИЛСЯ' })).toBeHidden();

  const storedResult = await page.evaluate(() => {
    const raw = window.sessionStorage.getItem('game_result');
    return raw ? JSON.parse(raw) as { state?: string; source?: string; duel_id?: string } : null;
  });
  expect(storedResult).toMatchObject({
    state: 'timeup',
    source: 'local_timer',
    duel_id: duelID,
  });

  const storedGame = await page.evaluate(() => {
    const raw = window.sessionStorage.getItem('currentGame');
    return raw
      ? JSON.parse(raw) as {
          deadline?: string;
          opponent_disconnected?: boolean;
          opponent_id?: string;
        }
      : null;
  });
  expect(storedGame?.deadline).toBe(deadline);
  expect(storedGame?.opponent_disconnected).toBe(false);
  expect(storedGame?.opponent_id).toBe(opponentID);
});

test('task page ignores duel_resume with unexpected known opponent id', async ({ page }) => {
  const playerID = 'ebebebeb-ebeb-ebeb-ebeb-ebebebebebeb';
  const sessionToken = '10000000-0000-0000-0000-000000000090';
  const duelID = 'ecececec-ecec-ecec-ecec-ecececececec';
  const opponentID = 'edededed-eded-eded-eded-edededededed';
  const thirdPlayerID = 'efefefef-efef-efef-efef-efefefefefef';
  const deadline = inSecondsISO(120);
  const laterDeadline = inSecondsISO(240);
  const task = {
    id: 'f0f0f0f0-f0f0-f0f0-f0f0-f0f0f0f0f0f0',
    title: 'Known Opponent Resume Guard',
    description: 'Mismatched opponent resume must not update current game.',
    category: 'web',
    difficulty: 'easy',
    time_limit: 120,
    time_limit_seconds: 120,
    task_url: 'https://example.com/task',
    hint_schedule: [],
  };
  const mismatchedTask = {
    ...task,
    id: 'f1f1f1f1-f1f1-f1f1-f1f1-f1f1f1f1f1f1',
    title: 'Mismatched Opponent Resume Task',
  };
  let activeSocket: WebSocketRoute | null = null;

  await page.addInitScript(({ playerID, sessionToken, duelID, opponentID, deadline, task }) => {
    window.sessionStorage.setItem('player_id', playerID);
    window.sessionStorage.setItem('username', 'alice');
    window.sessionStorage.setItem('currentGame', JSON.stringify({
      duel_id: duelID,
      deadline,
      time_limit_seconds: 120,
      task,
      opponent_username: 'bob',
      opponent_id: opponentID,
    }));
  }, { playerID, sessionToken, duelID, opponentID, deadline, task });

  await page.routeWebSocket((url) => url.pathname === '/ws', (ws) => {
    activeSocket = ws;
  });

  await page.goto('/task');
  await expect(page.getByRole('heading', { name: 'Known Opponent Resume Guard' })).toBeVisible();
  await expect.poll(() => activeSocket !== null).toBeTruthy();

  activeSocket!.send(JSON.stringify({
    type: 'duel_resume',
    payload: {
      duel_id: duelID,
      opponent_id: thirdPlayerID,
      deadline: laterDeadline,
      opponent_disconnected: false,
      task: mismatchedTask,
    },
  }));

  await page.waitForTimeout(150);
  await expect(page.getByRole('heading', { name: 'Known Opponent Resume Guard' })).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Mismatched Opponent Resume Task' })).toBeHidden();
  const storedAfterMismatch = await page.evaluate(() => {
    const raw = window.sessionStorage.getItem('currentGame');
    return raw
      ? JSON.parse(raw) as { deadline?: string; opponent_id?: string; task?: { title?: string } }
      : null;
  });
  expect(storedAfterMismatch?.deadline).toBe(deadline);
  expect(storedAfterMismatch?.opponent_id).toBe(opponentID);
  expect(storedAfterMismatch?.task?.title).toBe('Known Opponent Resume Guard');

  activeSocket!.send(JSON.stringify({
    type: 'duel_resume',
    payload: {
      duel_id: duelID,
      opponent_id: opponentID,
      deadline: laterDeadline,
      opponent_disconnected: false,
      task,
    },
  }));

  await expect.poll(async () => {
    const raw = await page.evaluate(() => window.sessionStorage.getItem('currentGame'));
    return raw ? (JSON.parse(raw) as { deadline?: string }).deadline : null;
  }).toBe(laterDeadline);
});

test('self and mismatched duel_resume do not recover task page from local timer timeup', async ({ page }) => {
  const baseTime = new Date('2026-01-01T00:00:00.000Z');
  await page.clock.install({ time: baseTime });

  const playerID = 'f2f2f2f2-f2f2-f2f2-f2f2-f2f2f2f2f2f2';
  const sessionToken = '10000000-0000-0000-0000-000000000091';
  const duelID = 'f3f3f3f3-f3f3-f3f3-f3f3-f3f3f3f3f3f3';
  const opponentID = 'f4f4f4f4-f4f4-f4f4-f4f4-f4f4f4f4f4f4';
  const thirdPlayerID = 'f5f5f5f5-f5f5-f5f5-f5f5-f5f5f5f5f5f5';
  const deadline = new Date(baseTime.getTime() + 1000).toISOString();
  const laterDeadline = new Date(baseTime.getTime() + 120_000).toISOString();
  const task = {
    id: 'f6f6f6f6-f6f6-f6f6-f6f6-f6f6f6f6f6f6',
    title: 'Resume Identity Local Timeup Guard',
    description: 'Invalid resume opponent id must not revive local timer result.',
    category: 'web',
    difficulty: 'easy',
    time_limit: 120,
    time_limit_seconds: 120,
    task_url: 'https://example.com/task',
    hint_schedule: [],
  };
  let activeSocket: WebSocketRoute | null = null;

  await page.addInitScript(({ playerID, sessionToken, duelID, opponentID, deadline, task }) => {
    window.sessionStorage.setItem('player_id', playerID);
    window.sessionStorage.setItem('username', 'alice');
    window.sessionStorage.setItem('currentGame', JSON.stringify({
      duel_id: duelID,
      deadline,
      time_limit_seconds: 120,
      task,
      opponent_username: 'bob',
      opponent_id: opponentID,
    }));
  }, { playerID, sessionToken, duelID, opponentID, deadline, task });

  await page.routeWebSocket((url) => url.pathname === '/ws', (ws) => {
    activeSocket = ws;
  });

  await page.goto('/task');
  await expect(page.getByRole('heading', { name: 'Resume Identity Local Timeup Guard' })).toBeVisible();
  await expect.poll(() => activeSocket !== null).toBeTruthy();
  await page.clock.fastForward(1100);
  await expect(page.getByText('ВРЕМЯ ВЫШЛО!')).toBeVisible();

  for (const invalidOpponentID of [playerID, thirdPlayerID]) {
    activeSocket!.send(JSON.stringify({
      type: 'duel_resume',
      payload: {
        duel_id: duelID,
        opponent_id: invalidOpponentID,
        deadline: laterDeadline,
        opponent_disconnected: false,
        task,
      },
    }));
  }

  await page.waitForTimeout(150);
  await expect(page.getByText('ВРЕМЯ ВЫШЛО!')).toBeVisible();
  await expect(page.getByPlaceholder('ctf{...}')).toBeDisabled();
  const storedResult = await page.evaluate(() => {
    const raw = window.sessionStorage.getItem('game_result');
    return raw ? JSON.parse(raw) as { state?: string; source?: string; duel_id?: string } : null;
  });
  expect(storedResult).toMatchObject({
    state: 'timeup',
    source: 'local_timer',
    duel_id: duelID,
  });

  const storedGame = await page.evaluate(() => {
    const raw = window.sessionStorage.getItem('currentGame');
    return raw
      ? JSON.parse(raw) as { deadline?: string; opponent_id?: string }
      : null;
  });
  expect(storedGame?.deadline).toBe(deadline);
  expect(storedGame?.opponent_id).toBe(opponentID);
});

test('legacy task page stores valid non-self opponent id from duel_resume', async ({ page }) => {
  const playerID = 'f7f7f7f7-f7f7-f7f7-f7f7-f7f7f7f7f7f7';
  const sessionToken = '10000000-0000-0000-0000-000000000092';
  const duelID = 'f8f8f8f8-f8f8-f8f8-f8f8-f8f8f8f8f8f8';
  const opponentID = 'f9f9f9f9-f9f9-f9f9-f9f9-f9f9f9f9f9f9';
  const deadline = inSecondsISO(120);
  const laterDeadline = inSecondsISO(240);
  const task = {
    id: 'fafafafa-fafa-fafa-fafa-fafafafafafa',
    title: 'Legacy Resume Opponent Save',
    description: 'Legacy storage without opponent id should learn it from resume.',
    category: 'web',
    difficulty: 'easy',
    time_limit: 120,
    time_limit_seconds: 120,
    task_url: 'https://example.com/task',
    hint_schedule: [],
  };
  let activeSocket: WebSocketRoute | null = null;

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

  await page.routeWebSocket((url) => url.pathname === '/ws', (ws) => {
    activeSocket = ws;
  });

  await page.goto('/task');
  await expect(page.getByRole('heading', { name: 'Legacy Resume Opponent Save' })).toBeVisible();
  await expect.poll(() => activeSocket !== null).toBeTruthy();

  activeSocket!.send(JSON.stringify({
    type: 'duel_resume',
    payload: {
      duel_id: duelID,
      opponent_id: opponentID,
      deadline: laterDeadline,
      opponent_disconnected: false,
      task,
    },
  }));

  await expect.poll(async () => {
    const raw = await page.evaluate(() => window.sessionStorage.getItem('currentGame'));
    return raw ? (JSON.parse(raw) as { opponent_id?: string }).opponent_id : null;
  }).toBe(opponentID);
  await expect.poll(async () => {
    const raw = await page.evaluate(() => window.sessionStorage.getItem('currentGame'));
    return raw ? (JSON.parse(raw) as { deadline?: string }).deadline : null;
  }).toBe(laterDeadline);
});

test('accepted server terminal ignores later active websocket events', async ({ page }) => {
  const playerID = 'bebebebe-bebe-bebe-bebe-bebebebebebe';
  const sessionToken = '10000000-0000-0000-0000-000000000080';
  const duelID = 'bfbfbfbf-bfbf-bfbf-bfbf-bfbfbfbfbfbf';
  const opponentID = 'c0c0c0c0-c0c0-c0c0-c0c0-c0c0c0c0c0c0';
  const deadline = inSecondsISO(120);
  const laterDeadline = inSecondsISO(240);
  const task = {
    id: 'c1c1c1c1-c1c1-c1c1-c1c1-c1c1c1c1c1c1',
    title: 'Server Terminal Active Ignore',
    description: 'Active events after terminal must not revive the duel.',
    category: 'web',
    difficulty: 'easy',
    time_limit: 120,
    time_limit_seconds: 120,
    task_url: 'https://example.com/task',
    hint_schedule: [],
  };
  const laterTask = {
    ...task,
    id: 'c2c2c2c2-c2c2-c2c2-c2c2-c2c2c2c2c2c2',
    title: 'Revived Task Should Not Render',
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

  await page.routeWebSocket((url) => url.pathname === '/ws', (ws) => {
    activeSocket = ws;
  });

  await page.goto('/task');
  await expect(page.getByRole('heading', { name: 'Server Terminal Active Ignore' })).toBeVisible();
  await expect.poll(() => activeSocket !== null).toBeTruthy();

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

  sendServerEvent({
    type: 'duel_resume',
    payload: {
      duel_id: duelID,
      deadline: laterDeadline,
      opponent_disconnected: false,
      task: laterTask,
    },
  });
  sendServerEvent({
    type: 'opponent_reconnected',
    payload: {
      duel_id: duelID,
      player_id: opponentID,
      deadline: laterDeadline,
    },
  });
  sendServerEvent({
    type: 'hint_unlocked',
    payload: {
      duel_id: duelID,
      task_id: task.id,
      hint_index: 1,
      hint: 'Late hint must not render',
      unlocked_at: nowISO(),
    },
  });

  await page.waitForTimeout(150);
  await expect(page.getByText('ПОБЕДА!')).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Revived Task Should Not Render' })).toBeHidden();
  await expect(page.getByText('Late hint must not render')).toBeHidden();
  await expect(page.getByText('Соперник вернулся. Дуэль продолжается.')).toBeHidden();
  const storedResult = await page.evaluate(() => {
    const raw = window.sessionStorage.getItem('game_result');
    return raw ? JSON.parse(raw) as { state?: string; source?: string; duel_id?: string } : null;
  });
  expect(storedResult).toMatchObject({
    state: 'won',
    source: 'server',
    duel_id: duelID,
  });
});

test('duel.paused error after flag submit pauses UI without incorrect flag state', async ({ page }) => {
  const playerID = '79797979-7979-7979-7979-797979797979';
  const sessionToken = '10000000-0000-0000-0000-000000000030';
  const duelID = '80808080-8080-8080-8080-808080808080';
  const task = {
    id: '81818181-8181-8181-8181-818181818181',
    title: 'Duel Paused Error Contract Check',
    description: 'Backend duel.paused error should pause the UI.',
    category: 'web',
    difficulty: 'easy',
    time_limit: 120,
    time_limit_seconds: 120,
    task_url: 'https://example.com/task',
    hint_schedule: [],
  };
  const messages: Array<{ type: string; payload?: unknown }> = [];

  await page.addInitScript(({ playerID, sessionToken, duelID, task }) => {
    window.sessionStorage.setItem('player_id', playerID);
    window.sessionStorage.setItem('username', 'alice');
    window.sessionStorage.setItem('currentGame', JSON.stringify({
      duel_id: duelID,
      deadline: new Date(Date.now() + 120_000).toISOString(),
      time_limit_seconds: 120,
      task,
      opponent_username: 'bob',
    }));
  }, { playerID, sessionToken, duelID, task });

  await page.routeWebSocket((url) => url.pathname === '/ws', (ws) => {
    ws.onMessage((raw) => {
      const message = JSON.parse(String(raw)) as { type: string; payload?: unknown };
      messages.push(message);
      if (message.type === 'flag_submit') {
        ws.send(JSON.stringify({
          type: 'error',
          code: 'duel.paused',
          message: 'duel is paused while a player reconnects',
        }));
      }
    });
  });

  await page.goto('/task');
  await expect(page.getByRole('heading', { name: 'Duel Paused Error Contract Check' })).toBeVisible();

  await page.getByPlaceholder('ctf{...}').fill('ctf{paused}');
  await page.getByRole('button', { name: /Отправить/ }).click();

  await expect(page.getByText('Дуэль на паузе: дождитесь возвращения соперника.')).toBeVisible();
  await expect(page.getByRole('heading', { name: 'СОПЕРНИК ОТКЛЮЧИЛСЯ' })).toBeVisible();
  await expect(page.getByPlaceholder('ctf{...}')).toBeDisabled();
  await expect(page.getByRole('button', { name: /Отправить/ })).toBeDisabled();
  await expect(page.getByText('Неверный флаг')).toBeHidden();
  expect(messages).toContainEqual({
    type: 'flag_submit',
    payload: {
      duel_id: duelID,
      flag: 'ctf{paused}',
    },
  });
});

test('stale wrong flag timeout does not clear a later correct result', async ({ page }) => {
  const playerID = '81818181-8181-8181-8181-818181818181';
  const sessionToken = '10000000-0000-0000-0000-000000000031';
  const duelID = '82828282-8282-8282-8282-828282828282';
  const task = {
    id: '83838383-8383-8383-8383-838383838383',
    title: 'Flag Status Timer Guard',
    description: 'A stale incorrect timeout must not clear a newer correct status.',
    category: 'web',
    difficulty: 'easy',
    time_limit: 120,
    time_limit_seconds: 120,
    task_url: 'https://example.com/task',
    hint_schedule: [],
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

  await page.routeWebSocket((url) => url.pathname === '/ws', (ws) => {
    ws.onMessage((raw) => {
      const message = JSON.parse(String(raw)) as {
        type: string;
        payload?: { flag?: string };
      };
      if (message.type !== 'flag_submit') {
        return;
      }
      ws.send(JSON.stringify({
        type: 'flag_result',
        payload: {
          duel_id: duelID,
          correct: message.payload?.flag === 'ctf{correct}',
        },
      }));
    });
  });

  await page.goto('/task');
  await expect(page.getByRole('heading', { name: 'Flag Status Timer Guard' })).toBeVisible();

  await page.getByPlaceholder('ctf{...}').fill('ctf{wrong}');
  await page.getByRole('button', { name: /Отправить/ }).click();
  await expect(page.getByText('Неверный флаг')).toBeVisible();

  await page.getByPlaceholder('ctf{...}').fill('ctf{correct}');
  await page.getByRole('button', { name: /Отправить/ }).click();
  await expect(page.getByText('Флаг верный!')).toBeVisible();

  await page.waitForTimeout(3200);
  await expect(page.getByText('Флаг верный!')).toBeVisible();
  await expect(page.getByText('Неверный флаг')).toBeHidden();
});

test('invalid resume and reconnect payloads do not mutate task deadline', async ({ page }) => {
  const playerID = '61616161-6161-6161-6161-616161616161';
  const sessionToken = '10000000-0000-0000-0000-000000000032';
  const duelID = '62626262-6262-6262-6262-626262626262';
  const opponentID = '63636363-6363-6363-6363-636363636363';
  const deadline = inSecondsISO(120);
  const task = {
    id: '64646464-6464-6464-6464-646464646464',
    title: 'Invalid Resume Guard',
    description: 'Invalid resume payloads should be ignored.',
    category: 'web',
    difficulty: 'easy',
    time_limit: 120,
    time_limit_seconds: 120,
    task_url: 'https://example.com/task',
    hint_schedule: [],
  };
  const pageErrors: string[] = [];
  let activeSocket: WebSocketRoute | null = null;
  const sendServerEvent = (event: unknown) => {
    if (!activeSocket) {
      throw new Error('WebSocket was not opened');
    }
    activeSocket.send(JSON.stringify(event));
  };
  const currentStoredGame = () =>
    page.evaluate(() => {
      const raw = window.sessionStorage.getItem('currentGame');
      if (!raw) {
        return null;
      }
      return JSON.parse(raw) as {
        deadline?: string;
        opponent_disconnected?: boolean;
        opponent_reconnect_deadline?: string;
      };
    });

  page.on('pageerror', (error) => {
    pageErrors.push(error.message);
  });

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

  await page.routeWebSocket((url) => url.pathname === '/ws', (ws) => {
    activeSocket = ws;
  });

  await page.goto('/task');
  await expect(page.getByRole('heading', { name: 'Invalid Resume Guard' })).toBeVisible();
  await expect.poll(() => activeSocket !== null).toBeTruthy();

  sendServerEvent({
    type: 'opponent_reconnected',
    payload: {
      duel_id: duelID,
      player_id: opponentID,
      deadline: 'not-a-date',
    },
  });
  sendServerEvent({
    type: 'opponent_disconnected',
    payload: {
      duel_id: duelID,
      player_id: opponentID,
      reconnect_deadline: 'not-a-date',
    },
  });
  sendServerEvent({
    type: 'duel_resume',
    payload: {
      duel_id: duelID,
      deadline: 123,
      opponent_disconnected: false,
    },
  });
  sendServerEvent({
    type: 'duel_resume',
    payload: {
      duel_id: duelID,
      deadline: 'not-a-date',
      opponent_disconnected: false,
    },
  });

  await page.waitForTimeout(150);

  const storedGame = await currentStoredGame();
  expect(storedGame?.deadline).toBe(deadline);
  expect(storedGame?.opponent_disconnected).toBeUndefined();
  expect(storedGame?.opponent_reconnect_deadline).toBeUndefined();
  await expect(page.getByRole('heading', { name: 'Invalid Resume Guard' })).toBeVisible();
  await expect(page.getByPlaceholder('ctf{...}')).toBeEnabled();
  await expect(page.getByText('ВРЕМЯ ВЫШЛО!')).toBeHidden();
  expect(pageErrors).toEqual([]);
});

test('task page ignores websocket events for other duels', async ({ page }) => {
  const playerID = '91919191-9191-9191-9191-919191919191';
  const sessionToken = '10000000-0000-0000-0000-000000000033';
  const duelID = '92929292-9292-9292-9292-929292929292';
  const wrongDuelID = '93939393-9393-9393-9393-939393939393';
  const opponentID = '94949494-9494-9494-9494-949494949494';
  const deadline = inSecondsISO(120);
  const wrongDeadline = inSecondsISO(300);
  const task = {
    id: '95959595-9595-9595-9595-959595959595',
    title: 'Current Duel Guard',
    description: 'Only current-duel websocket events may mutate this page.',
    category: 'web',
    difficulty: 'easy',
    time_limit: 120,
    time_limit_seconds: 120,
    task_url: 'https://example.com/task',
    hint_schedule: [],
  };
  const wrongTask = {
    ...task,
    id: '96969696-9696-9696-9696-969696969696',
    title: 'Wrong Duel Task',
  };
  const pageErrors: string[] = [];
  let activeSocket: WebSocketRoute | null = null;
  const sendServerEvent = (event: unknown) => {
    if (!activeSocket) {
      throw new Error('WebSocket was not opened');
    }
    activeSocket.send(JSON.stringify(event));
  };
  const currentStoredGame = () =>
    page.evaluate(() => {
      const raw = window.sessionStorage.getItem('currentGame');
      if (!raw) {
        return null;
      }
      return JSON.parse(raw) as {
        deadline?: string;
        opponent_disconnected?: boolean;
        opponent_reconnect_deadline?: string;
        task?: {
          title?: string;
        };
      };
    });

  page.on('pageerror', (error) => {
    pageErrors.push(error.message);
  });

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

  await page.routeWebSocket((url) => url.pathname === '/ws', (ws) => {
    activeSocket = ws;
  });

  await page.goto('/task');
  await expect(page.getByRole('heading', { name: 'Current Duel Guard' })).toBeVisible();
  await expect.poll(() => activeSocket !== null).toBeTruthy();

  sendServerEvent({
    type: 'flag_result',
    payload: {
      duel_id: wrongDuelID,
      correct: true,
    },
  });
  sendServerEvent({
    type: 'hint_unlocked',
    payload: {
      duel_id: wrongDuelID,
      task_id: task.id,
      hint_index: 1,
      hint: 'Wrong duel hint',
      unlocked_at: nowISO(),
    },
  });
  sendServerEvent({
    type: 'opponent_solved',
    payload: {
      duel_id: wrongDuelID,
      player_id: opponentID,
    },
  });
  sendServerEvent({
    type: 'opponent_disconnected',
    payload: {
      duel_id: wrongDuelID,
      player_id: opponentID,
      reconnect_deadline: inSecondsISO(60),
    },
  });
  sendServerEvent({
    type: 'opponent_reconnected',
    payload: {
      duel_id: wrongDuelID,
      player_id: opponentID,
      deadline: wrongDeadline,
    },
  });
  sendServerEvent({
    type: 'duel_resume',
    payload: {
      duel_id: wrongDuelID,
      deadline: wrongDeadline,
      opponent_disconnected: true,
      opponent_reconnect_deadline: inSecondsISO(60),
      task: wrongTask,
    },
  });
  sendServerEvent({
    type: 'duel_expired',
    payload: {
      duel_id: wrongDuelID,
    },
  });
  sendServerEvent({
    type: 'duel_finished',
    payload: {
      duel_id: wrongDuelID,
      winner_id: playerID,
      winner_username: 'alice',
      your_solved: true,
      opponent_solved: false,
      duel: {
        id: wrongDuelID,
        player1_id: playerID,
        player2_id: opponentID,
        status: 'finished',
        winner_id: playerID,
        deadline: wrongDeadline,
        started_at: nowISO(),
        finished_at: nowISO(),
      },
    },
  });
  sendServerEvent({
    type: 'duel_finished',
    payload: {
      duel_id: duelID,
      winner_id: playerID,
      winner_username: 'alice',
      your_solved: true,
      opponent_solved: false,
      duel: {
        id: wrongDuelID,
        player1_id: playerID,
        player2_id: opponentID,
        status: 'finished',
        winner_id: playerID,
        deadline: wrongDeadline,
        started_at: nowISO(),
        finished_at: nowISO(),
      },
    },
  });

  await page.waitForTimeout(150);

  const storedGame = await currentStoredGame();
  expect(storedGame?.deadline).toBe(deadline);
  expect(storedGame?.opponent_disconnected).toBeUndefined();
  expect(storedGame?.opponent_reconnect_deadline).toBeUndefined();
  expect(storedGame?.task?.title).toBe('Current Duel Guard');
  await expect(page.getByRole('heading', { name: 'Current Duel Guard' })).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Wrong Duel Task' })).toBeHidden();
  await expect(page.getByPlaceholder('ctf{...}')).toBeEnabled();
  await expect(page.getByText('Флаг верный!')).toBeHidden();
  await expect(page.getByText('Неверный флаг')).toBeHidden();
  await expect(page.getByText('Wrong duel hint')).toBeHidden();
  await expect(page.getByText('Соперник уже решил задание. Ждем завершение дуэли...')).toBeHidden();
  await expect(page.getByRole('heading', { name: 'СОПЕРНИК ОТКЛЮЧИЛСЯ' })).toBeHidden();
  await expect(page.getByText('Соперник вернулся. Дуэль продолжается.')).toBeHidden();
  await expect(page.getByRole('heading', { name: 'ВРЕМЯ ВЫШЛО!' })).toBeHidden();
  await expect(page.getByRole('heading', { name: 'ПОБЕДА!' })).toBeHidden();
  await expect(page.getByRole('heading', { name: 'ПОРАЖЕНИЕ' })).toBeHidden();
  await expect.poll(() => page.evaluate(() => window.sessionStorage.getItem('game_result'))).toBeNull();
  expect(pageErrors).toEqual([]);
});
