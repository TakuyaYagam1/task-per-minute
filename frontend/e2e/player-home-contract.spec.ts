import { expect, test, type Page, type WebSocketRoute } from '@playwright/test';
import {
  expectWebSocketURLDoesNotLeakSession,
  inSecondsISO,
  jsonHeaders,
  nowISO,
} from './support/common';
import {
  installWebSocketErrorProbe,
  type WebSocketErrorProbeWindow,
  type WindowOpenCall,
} from './support/browser';

const activeDuelPayload = (duelID: string, deadline: string) => ({
  active_duel: {
    id: duelID,
    status: 'active',
    deadline,
    started_at: nowISO(),
  },
});

const activeDuelAfterTaskNavigation = (
  page: Page,
  duelID: string,
  deadline: string,
): Record<string, unknown> => (page.url().endsWith('/task')
  ? activeDuelPayload(duelID, deadline)
  : {});

test('legacy session_id storage does not restore player session', async ({ page }) => {
  const playerID = '77777777-7777-7777-7777-777777777777';
  let meCalls = 0;
  let websocketOpened = false;

  await page.addInitScript(({ playerID }) => {
    window.localStorage.setItem('player_id', playerID);
    window.localStorage.setItem('session_id', 'legacy-session-id');
    window.localStorage.setItem('username', 'alice');
  }, { playerID });

  await page.route('**/api/v1/players/me', async (route) => {
    meCalls += 1;
    await route.abort();
  });

  await page.routeWebSocket((url) => url.pathname === '/ws', () => {
    websocketOpened = true;
  });

  await page.goto('/');

  await expect(page.getByPlaceholder('Введите никнейм...')).toBeVisible();
  await expect(page.getByText('Введите никнейм')).toBeVisible();
  await expect(page.getByText('Игрок готов')).toBeHidden();
  await expect.poll(() => meCalls).toBe(0);
  await expect.poll(() => websocketOpened).toBe(false);
});

test('blocked browser storage does not crash home page', async ({ page }) => {
  const pageErrors: string[] = [];
  let meCalls = 0;

  page.on('pageerror', (error) => {
    pageErrors.push(error.message);
  });

  await page.addInitScript(() => {
    const throwSecurityError = () => {
      throw new DOMException('blocked', 'SecurityError');
    };
    Storage.prototype.getItem = throwSecurityError;
    Storage.prototype.setItem = throwSecurityError;
    Storage.prototype.removeItem = throwSecurityError;
  });

  await page.route('**/api/v1/players/me', async (route) => {
    meCalls += 1;
    await route.abort();
  });

  await page.goto('/');

  await expect(page.getByPlaceholder('Введите никнейм...')).toBeVisible();
  await expect(page.getByText('Введите никнейм')).toBeVisible();
  await expect(page.getByText('Игрок готов')).toBeHidden();
  expect(pageErrors).toEqual([]);
  expect(meCalls).toBe(0);
});

test('blocked browser storage still preserves task transition in memory', async ({ page }) => {
  const playerID = '35353535-3535-3535-3535-353535353535';
  const sessionToken = '10000000-0000-0000-0000-000000000091';
  const duelID = '36363636-3636-3636-3636-363636363636';
  const opponentID = '37373737-3737-3737-3737-373737373737';
  const taskID = '38383838-3838-3838-3838-383838383838';
  const deadline = inSecondsISO(180);
  const pageErrors: string[] = [];

  page.on('pageerror', (error) => {
    pageErrors.push(error.message);
  });

  await page.addInitScript(() => {
    const throwSecurityError = () => {
      throw new DOMException('blocked', 'SecurityError');
    };
    Storage.prototype.getItem = throwSecurityError;
    Storage.prototype.setItem = throwSecurityError;
    Storage.prototype.removeItem = throwSecurityError;
  });

  await page.route('**/api/v1/players/join', async (route) => {
    await route.fulfill({
      status: 200,
      headers: jsonHeaders,
      body: JSON.stringify({
        player_id: playerID,
        session_token: sessionToken,
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
        ...activeDuelAfterTaskNavigation(page, duelID, deadline),
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
            title: 'Blocked Storage Transition',
            description: 'Task handoff survives a restricted localStorage context.',
            category: 'web',
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
  await expect(page.getByRole('heading', { name: 'Blocked Storage Transition' })).toBeVisible();
  expect(pageErrors).toEqual([]);
});

test('malformed join response does not persist player session', async ({ page }) => {
  const playerID = '79797979-7979-7979-7979-797979797979';

  await page.route('**/api/v1/players/join', async (route) => {
    expect(route.request().method()).toBe('POST');
    await route.fulfill({
      status: 200,
      headers: jsonHeaders,
      body: JSON.stringify({
        player_id: playerID,
      }),
    });
  });

  await page.goto('/');
  await page.getByPlaceholder('Введите никнейм...').fill('alice');
  await page.getByRole('button', { name: /ПОДКЛЮЧИТЬСЯ/ }).click();

  await expect(page.getByText('Ошибка подключения к серверу')).toBeVisible();
  await expect(page.getByText('Игрок готов')).toBeHidden();
  await expect.poll(() => page.evaluate(() => window.localStorage.getItem('player_id'))).toBeNull();
  await expect.poll(() => page.evaluate(() => window.localStorage.getItem('session_token'))).toBeNull();
  await expect.poll(() => page.evaluate(() => window.localStorage.getItem('username'))).toBeNull();
});

test('join conflict without stored session shows conflict instead of restore promise', async ({ page }) => {
  let joinCalls = 0;

  await page.route('**/api/v1/players/join', async (route) => {
    joinCalls += 1;
    expect(route.request().postDataJSON()).toEqual({ username: 'alice' });
    await route.fulfill({
      status: 409,
      headers: jsonHeaders,
      body: JSON.stringify({
        type: 'about:blank',
        title: 'Conflict',
        status: 409,
        detail: 'player_in_duel',
      }),
    });
  });

  await page.goto('/');
  await page.getByPlaceholder('Введите никнейм...').fill('alice');
  await page.getByRole('button', { name: /ПОДКЛЮЧИТЬСЯ/ }).click();

  await expect(page.getByText('Игрок уже в активной дуэли. Откройте текущую сессию или дождитесь завершения.')).toBeVisible();
  await expect(page.getByText('Вы уже в активной дуэли. Восстанавливаем...')).toBeHidden();
  await expect(page.getByText('Игрок готов')).toBeHidden();
  expect(joinCalls).toBe(1);
  await expect.poll(() => page.evaluate(() => window.localStorage.getItem('player_id'))).toBeNull();
  await expect.poll(() => page.evaluate(() => window.localStorage.getItem('session_token'))).toBeNull();
  await expect.poll(() => page.evaluate(() => window.localStorage.getItem('username'))).toBeNull();
});

test('join rate limit shows retry message without persisting session', async ({ page }) => {
  let joinCalls = 0;

  await page.route('**/api/v1/players/join', async (route) => {
    joinCalls += 1;
    expect(route.request().postDataJSON()).toEqual({ username: 'alice' });
    await route.fulfill({
      status: 429,
      headers: {
        ...jsonHeaders,
        'Retry-After': '7',
      },
      body: JSON.stringify({
        type: 'about:blank',
        title: 'Too Many Requests',
        status: 429,
        detail: 'rate limit exceeded',
      }),
    });
  });

  await page.goto('/');
  await page.getByPlaceholder('Введите никнейм...').fill('alice');
  await page.getByRole('button', { name: /ПОДКЛЮЧИТЬСЯ/ }).click();

  await expect(page.getByText('Слишком много попыток. Повторите через 7 секунд.')).toBeVisible();
  await expect(page.getByText('Игрок готов')).toBeHidden();
  expect(joinCalls).toBe(1);
  await expect.poll(() => page.evaluate(() => window.localStorage.getItem('player_id'))).toBeNull();
  await expect.poll(() => page.evaluate(() => window.localStorage.getItem('session_token'))).toBeNull();
  await expect.poll(() => page.evaluate(() => window.localStorage.getItem('username'))).toBeNull();
});

test('join form blocks invalid usernames before backend request', async ({ page }) => {
  let joinCalls = 0;

  await page.route('**/api/v1/players/join', async (route) => {
    joinCalls += 1;
    await route.abort();
  });

  await page.goto('/');

  for (const username of ['a', 'alice!', 'alice bob', 'алиса']) {
    await page.getByPlaceholder('Введите никнейм...').fill(username);
    await page.getByRole('button', { name: /ПОДКЛЮЧИТЬСЯ/ }).click();
    await expect(page.getByText('Никнейм: 2-50 символов, латиница, цифры, _ или -')).toBeVisible();
  }

  await page.getByPlaceholder('Введите никнейм...').fill('   ');
  await expect(page.getByRole('button', { name: /ПОДКЛЮЧИТЬСЯ/ })).toBeDisabled();
  expect(joinCalls).toBe(0);
});

test('valid username and uuid join response persist player session', async ({ page }) => {
  const playerID = '7a7a7a7a-7a7a-7a7a-7a7a-7a7a7a7a7a7a';
  const sessionToken = '7b7b7b7b-7b7b-7b7b-7b7b-7b7b7b7b7b7b';
  let joinCalls = 0;

  await page.route('**/api/v1/players/join', async (route) => {
    joinCalls += 1;
    expect(route.request().postDataJSON()).toEqual({ username: 'alice_01' });
    await route.fulfill({
      status: 200,
      headers: jsonHeaders,
      body: JSON.stringify({
        player_id: playerID,
        session_token: sessionToken,
      }),
    });
  });

  await page.goto('/');
  await page.getByPlaceholder('Введите никнейм...').fill('  alice_01  ');
  await page.getByRole('button', { name: /ПОДКЛЮЧИТЬСЯ/ }).click();

  await expect(page.getByText('Игрок готов')).toBeVisible();
  expect(joinCalls).toBe(1);
  await expect.poll(() => page.evaluate(() => window.localStorage.getItem('player_id'))).toBe(playerID);
  await expect.poll(() => page.evaluate(() => window.localStorage.getItem('session_token'))).toBe(sessionToken);
  await expect.poll(() => page.evaluate(() => window.localStorage.getItem('username'))).toBe('alice_01');
});

test('join response with non-uuid ids does not persist player session', async ({ page }) => {
  await page.route('**/api/v1/players/join', async (route) => {
    await route.fulfill({
      status: 200,
      headers: jsonHeaders,
      body: JSON.stringify({
        player_id: 'not-a-uuid',
        session_token: 'also-not-a-uuid',
      }),
    });
  });

  await page.goto('/');
  await page.getByPlaceholder('Введите никнейм...').fill('alice-ctf');
  await page.getByRole('button', { name: /ПОДКЛЮЧИТЬСЯ/ }).click();

  await expect(page.getByText('Ошибка подключения к серверу')).toBeVisible();
  await expect(page.getByText('Игрок готов')).toBeHidden();
  await expect.poll(() => page.evaluate(() => window.localStorage.getItem('player_id'))).toBeNull();
  await expect.poll(() => page.evaluate(() => window.localStorage.getItem('session_token'))).toBeNull();
  await expect.poll(() => page.evaluate(() => window.localStorage.getItem('username'))).toBeNull();
});

test('successful join clears stale duel storage before saving rotated session', async ({ page }) => {
  const newPlayerID = '80808080-8080-8080-8080-808080808080';
  const newSessionToken = '10000000-0000-0000-0000-000000000001';
  const staleDuelID = '81818181-8181-8181-8181-818181818181';
  const staleTask = {
    id: '82828282-8282-8282-8282-828282828282',
    title: 'Stale Task',
    description: 'This task belongs to an old session.',
    category: 'web',
    difficulty: 'easy',
    time_limit: 120,
    time_limit_seconds: 120,
    task_url: 'https://example.com/stale-task',
    hint_schedule: [],
  };
  const websocketUrls: string[] = [];

  await page.route('**/api/v1/players/join', async (route) => {
    expect(route.request().method()).toBe('POST');
    expect(route.request().postDataJSON()).toEqual({ username: 'alice' });
    await route.fulfill({
      status: 200,
      headers: jsonHeaders,
      body: JSON.stringify({
        player_id: newPlayerID,
        session_token: newSessionToken,
      }),
    });
  });
  await page.route('**/api/v1/players/me', async (route) => {
    expect(route.request().headers()['x-session-token']).toBe(newSessionToken);
    await route.fulfill({
      status: 200,
      headers: jsonHeaders,
      body: JSON.stringify({
        player: {
          id: newPlayerID,
          username: 'alice',
          status: 'idle',
          created_at: nowISO(),
        },
      }),
    });
  });

  await page.routeWebSocket((url) => url.pathname === '/ws', (ws) => {
    websocketUrls.push(ws.url());
    ws.close();
  });

  await page.goto('/');
  await page.evaluate(({ staleDuelID, staleTask }) => {
    window.localStorage.setItem('currentGame', JSON.stringify({
      duel_id: staleDuelID,
      deadline: new Date(Date.now() + 120_000).toISOString(),
      time_limit_seconds: 120,
      task: staleTask,
    }));
    window.localStorage.setItem('game_result', JSON.stringify({
      state: 'won',
      duel_id: staleDuelID,
      winner_id: 'old-player-id',
      winner_username: 'old-alice',
    }));
  }, { staleDuelID, staleTask });

  await page.getByPlaceholder('Введите никнейм...').fill('alice');
  await page.getByRole('button', { name: /ПОДКЛЮЧИТЬСЯ/ }).click();

  await expect(page.getByText('Игрок готов')).toBeVisible();
  await expect.poll(() => page.evaluate(() => window.localStorage.getItem('player_id'))).toBe(newPlayerID);
  await expect.poll(() => page.evaluate(() => window.localStorage.getItem('session_token'))).toBe(newSessionToken);
  await expect.poll(() => page.evaluate(() => window.localStorage.getItem('username'))).toBe('alice');
  await expect.poll(() => page.evaluate(() => window.localStorage.getItem('currentGame'))).toBeNull();
  await expect.poll(() => page.evaluate(() => window.localStorage.getItem('game_result'))).toBeNull();

  await page.goto('/task');
  await expect(page).toHaveURL(/\/$/);
  expect(websocketUrls).toEqual([]);
});

test('malformed join response preserves existing duel storage', async ({ page }) => {
  const playerID = '83838383-8383-8383-8383-838383838383';
  const staleDuelID = '84848484-8484-8484-8484-848484848484';
  const staleTask = {
    id: '85858585-8585-8585-8585-858585858585',
    title: 'Preserved Stale Task',
    description: 'Malformed join must not delete this local active state.',
    category: 'web',
    difficulty: 'easy',
    time_limit: 120,
    time_limit_seconds: 120,
    task_url: 'https://example.com/preserved-task',
    hint_schedule: [],
  };

  await page.route('**/api/v1/players/join', async (route) => {
    expect(route.request().method()).toBe('POST');
    await route.fulfill({
      status: 200,
      headers: jsonHeaders,
      body: JSON.stringify({
        player_id: playerID,
      }),
    });
  });

  await page.goto('/');
  await page.evaluate(({ staleDuelID, staleTask }) => {
    window.localStorage.setItem('currentGame', JSON.stringify({
      duel_id: staleDuelID,
      deadline: new Date(Date.now() + 120_000).toISOString(),
      time_limit_seconds: 120,
      task: staleTask,
    }));
    window.localStorage.setItem('game_result', JSON.stringify({
      state: 'won',
      duel_id: staleDuelID,
      winner_id: 'preserved-player-id',
      winner_username: 'alice',
    }));
  }, { staleDuelID, staleTask });

  await page.getByPlaceholder('Введите никнейм...').fill('alice');
  await page.getByRole('button', { name: /ПОДКЛЮЧИТЬСЯ/ }).click();

  await expect(page.getByText('Ошибка подключения к серверу')).toBeVisible();
  await expect(page.getByText('Игрок готов')).toBeHidden();
  await expect.poll(() => page.evaluate(() => window.localStorage.getItem('player_id'))).toBeNull();
  await expect.poll(() => page.evaluate(() => window.localStorage.getItem('session_token'))).toBeNull();
  await expect.poll(() => page.evaluate(() => window.localStorage.getItem('username'))).toBeNull();
  await expect.poll(() => page.evaluate(() => window.localStorage.getItem('currentGame'))).toContain(staleDuelID);
  await expect.poll(() => page.evaluate(() => window.localStorage.getItem('game_result'))).toContain(staleDuelID);
});

test('invalid task_assigned payload does not create game state or navigate', async ({ page }) => {
  const playerID = '51515151-5151-5151-5151-515151515151';
  const sessionToken = '10000000-0000-0000-0000-000000000002';
  const duelID = '52525252-5252-5252-5252-525252525252';
  const opponentID = '53535353-5353-5353-5353-535353535353';
  const deadline = inSecondsISO(180);
  const messages: Array<{ type: string; payload?: unknown }> = [];
  const validTask = {
    id: '54545454-5454-5454-5454-545454545454',
    title: 'Invalid task',
    description: 'Invalid payload variants must be ignored.',
    category: 'web',
    difficulty: 'easy',
    time_limit: 180,
    time_limit_seconds: 180,
    task_url: 'https://example.com/task',
    source_url: 'https://files.example/source.zip',
    hint_schedule: [
      { hint_index: 1, unlock_at: inSecondsISO(30) },
      { hint_index: 2, unlock_at: inSecondsISO(60) },
      { hint_index: 3, unlock_at: inSecondsISO(90) },
    ],
  };

  await page.addInitScript(() => {
    const testWindow = window as unknown as Window & { __openedUrls: WindowOpenCall[] };
    testWindow.__openedUrls = [];
    window.open = (url?: string | URL, target?: string, features?: string) => {
      testWindow.__openedUrls.push({
        url: url?.toString() || '',
        target,
        features,
      });
      return null;
    };
  });

  await page.route('**/api/v1/players/join', async (route) => {
    await route.fulfill({
      status: 200,
      headers: jsonHeaders,
      body: JSON.stringify({
        player_id: playerID,
        session_token: sessionToken,
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
      }),
    });
  });

  await page.routeWebSocket((url) => url.pathname === '/ws', (ws) => {
    expectWebSocketURLDoesNotLeakSession(ws.url());
    ws.onMessage((raw) => {
      const message = JSON.parse(String(raw)) as { type: string; payload?: unknown };
      messages.push(message);

      if (message.type !== 'join_queue') {
        return;
      }

      ws.send(JSON.stringify({
        type: 'match_found',
        payload: {
          duel_id: 'not-a-uuid',
          opponent_username: 'bob',
          duel: {
            id: 'not-a-uuid',
            player1_id: playerID,
            player2_id: opponentID,
            status: 'active',
            deadline,
            started_at: nowISO(),
          },
        },
      }));
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
          duel_id: 'not-a-uuid',
          deadline,
          time_limit_seconds: 180,
          task: validTask,
        },
      }));
      ws.send(JSON.stringify({
        type: 'task_assigned',
        payload: {
          duel_id: duelID,
          deadline,
          time_limit_seconds: 180,
          task: {
            id: validTask.id,
            title: validTask.title,
          },
        },
      }));
      ws.send(JSON.stringify({
        type: 'task_assigned',
        payload: {
          duel_id: duelID,
          deadline,
          time_limit_seconds: 120.5,
          task: validTask,
        },
      }));
      ws.send(JSON.stringify({
        type: 'task_assigned',
        payload: {
          duel_id: duelID,
          deadline,
          time_limit_seconds: 180,
          task: {
            ...validTask,
            time_limit: 120.5,
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
            ...validTask,
            source_url: 'javascript:alert(1)',
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
            ...validTask,
            source_file_url: 'ftp://files.example/source.zip',
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
            ...validTask,
            id: 'not-a-uuid',
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
            ...validTask,
            hint_schedule: [{ hint_index: 4, unlock_at: inSecondsISO(30) }],
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
            ...validTask,
            hint_schedule: [
              { hint_index: 1, unlock_at: inSecondsISO(30) },
              { hint_index: 1, unlock_at: inSecondsISO(60) },
            ],
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
            ...validTask,
            unlocked_hints: [
              { hint_index: 2, hint: 'first duplicate', unlocked_at: nowISO() },
              { hint_index: 2, hint: 'second duplicate', unlocked_at: nowISO() },
            ],
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
            ...validTask,
            hint_schedule: [
              { hint_index: 1, unlock_at: inSecondsISO(30) },
              { hint_index: 2, unlock_at: inSecondsISO(60) },
              { hint_index: 3, unlock_at: inSecondsISO(90) },
            ],
            unlocked_hints: [
              { hint_index: 1, hint: 'one', unlocked_at: nowISO() },
              { hint_index: 2, hint: 'two', unlocked_at: nowISO() },
              { hint_index: 3, hint: 'three', unlocked_at: nowISO() },
              { hint_index: 1, hint: 'four', unlocked_at: nowISO() },
            ],
          },
        },
      }));
      ws.send(JSON.stringify({
        type: 'game_start',
        data: {
          duel_id: duelID,
        },
      }));
    });
  });

  await page.goto('/');
  await page.getByPlaceholder('Введите никнейм...').fill('alice');
  await page.getByRole('button', { name: /ПОДКЛЮЧИТЬСЯ/ }).click();
  await expect(page.getByText('Игрок готов')).toBeVisible();

  await page.getByRole('button', { name: /ИГРАТЬ/ }).click();
  await expect.poll(() => messages.some((message) => message.type === 'join_queue')).toBe(true);
  await page.waitForTimeout(150);

  await expect(page).toHaveURL(/\/$/);
  await expect(page.getByRole('button', { name: 'Отменить поиск' })).toBeVisible();
  await expect.poll(() => page.evaluate(() => window.localStorage.getItem('currentGame'))).toBeNull();
  const opened = await page.evaluate(() => {
    const testWindow = window as unknown as Window & { __openedUrls?: WindowOpenCall[] };
    return testWindow.__openedUrls || [];
  });
  expect(opened).toEqual([]);
});

test('home ignores mismatched duel task assignment until matching task arrives', async ({ page }) => {
  const playerID = '56565656-5656-5656-5656-565656565656';
  const sessionToken = '10000000-0000-0000-0000-000000000003';
  const duelID = '57575757-5757-5757-5757-575757575757';
  const wrongDuelID = '58585858-5858-5858-5858-585858585858';
  const opponentID = '59595959-5959-5959-5959-595959595959';
  const taskID = '60606060-6060-6060-6060-606060606060';
  const deadline = inSecondsISO(180);
  const validTask = {
    id: taskID,
    title: 'Matching Duel Task',
    description: 'Only the matching duel task assignment may start the task page.',
    category: 'web',
    difficulty: 'easy',
    time_limit: 180,
    time_limit_seconds: 180,
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

  await page.route('**/api/v1/players/join', async (route) => {
    await route.fulfill({
      status: 200,
      headers: jsonHeaders,
      body: JSON.stringify({
        player_id: playerID,
        session_token: sessionToken,
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
        ...activeDuelAfterTaskNavigation(page, duelID, deadline),
      }),
    });
  });

  await page.routeWebSocket((url) => url.pathname === '/ws', (ws) => {
    expectWebSocketURLDoesNotLeakSession(ws.url());
    activeSocket = ws;
    ws.onMessage((raw) => {
      const message = JSON.parse(String(raw)) as { type: string; payload?: unknown };
      messages.push(message);

      if (message.type !== 'join_queue') {
        return;
      }

      ws.send(JSON.stringify({
        type: 'match_found',
        payload: {
          duel_id: duelID,
          opponent_username: 'bob',
          duel: {
            id: wrongDuelID,
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
          task: validTask,
        },
      }));
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
          duel_id: wrongDuelID,
          deadline,
          time_limit_seconds: 180,
          task: {
            ...validTask,
            title: 'Wrong Duel Task',
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
  await expect.poll(() => messages.some((message) => message.type === 'join_queue')).toBe(true);

  expect(page.url()).toMatch(/\/$/);
  await expect(page.getByRole('button', { name: 'Отменить поиск' })).toBeVisible();
  expect(await page.evaluate(() => window.localStorage.getItem('currentGame'))).toBeNull();

  sendServerEvent({
    type: 'task_assigned',
    payload: {
      duel_id: duelID,
      deadline,
      time_limit_seconds: 180,
      task: validTask,
    },
  });

  await expect(page).toHaveURL(/\/task$/);
  await expect(page.getByRole('heading', { name: 'Matching Duel Task' })).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Wrong Duel Task' })).toBeHidden();
  const storedGame = await page.evaluate(() => {
    const raw = window.localStorage.getItem('currentGame');
    return raw ? JSON.parse(raw) as {
      duel_id?: string;
      opponent_id?: string;
      task?: { title?: string };
    } : null;
  });
  expect(storedGame?.duel_id).toBe(duelID);
  expect(storedGame?.opponent_id).toBe(opponentID);
  expect(storedGame?.task?.title).toBe('Matching Duel Task');
});

test('home queue flow ignores restore resume before matching task assignment', async ({ page }) => {
  const playerID = 'd1d1d1d1-d1d1-d1d1-d1d1-d1d1d1d1d1d1';
  const sessionToken = '10000000-0000-0000-0000-000000000004';
  const duelID = 'd2d2d2d2-d2d2-d2d2-d2d2-d2d2d2d2d2d2';
  const restoreDuelID = 'd3d3d3d3-d3d3-d3d3-d3d3-d3d3d3d3d3d3';
  const opponentID = 'd4d4d4d4-d4d4-d4d4-d4d4-d4d4d4d4d4d4';
  const deadline = inSecondsISO(180);
  const validTask = {
    id: 'd5d5d5d5-d5d5-d5d5-d5d5-d5d5d5d5d5d5',
    title: 'Queue Flow Task',
    description: 'Queue mode should ignore restore events.',
    category: 'web',
    difficulty: 'easy',
    time_limit: 180,
    time_limit_seconds: 180,
    task_url: 'https://example.com/task',
    hint_schedule: [],
  };
  const restoreTask = {
    ...validTask,
    id: 'd6d6d6d6-d6d6-d6d6-d6d6-d6d6d6d6d6d6',
    title: 'Unexpected Restore Task',
  };
  const messages: Array<{ type: string; payload?: unknown }> = [];
  let activeSocket: WebSocketRoute | null = null;
  const sendServerEvent = (event: unknown) => {
    if (!activeSocket) {
      throw new Error('WebSocket was not opened');
    }
    activeSocket.send(JSON.stringify(event));
  };

  await page.route('**/api/v1/players/join', async (route) => {
    await route.fulfill({
      status: 200,
      headers: jsonHeaders,
      body: JSON.stringify({
        player_id: playerID,
        session_token: sessionToken,
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
        ...activeDuelAfterTaskNavigation(page, duelID, deadline),
      }),
    });
  });

  await page.routeWebSocket((url) => url.pathname === '/ws', (ws) => {
    activeSocket = ws;
    ws.onMessage((raw) => {
      const message = JSON.parse(String(raw)) as { type: string; payload?: unknown };
      messages.push(message);

      if (message.type !== 'join_queue') {
        return;
      }

      ws.send(JSON.stringify({
        type: 'duel_resume',
        payload: {
          duel_id: restoreDuelID,
          deadline: inSecondsISO(240),
          opponent_disconnected: false,
          task: restoreTask,
        },
      }));
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
    });
  });

  await page.goto('/');
  await page.getByPlaceholder('Введите никнейм...').fill('alice');
  await page.getByRole('button', { name: /ПОДКЛЮЧИТЬСЯ/ }).click();
  await expect(page.getByText('Игрок готов')).toBeVisible();

  await page.getByRole('button', { name: /ИГРАТЬ/ }).click();
  await expect.poll(() => messages.some((message) => message.type === 'join_queue')).toBe(true);
  await page.waitForTimeout(150);

  expect(page.url()).toMatch(/\/$/);
  await expect(page.getByRole('button', { name: 'Отменить поиск' })).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Unexpected Restore Task' })).toBeHidden();
  expect(await page.evaluate(() => window.localStorage.getItem('currentGame'))).toBeNull();

  sendServerEvent({
    type: 'task_assigned',
    payload: {
      duel_id: duelID,
      deadline,
      time_limit_seconds: 180,
      task: validTask,
    },
  });

  await expect(page).toHaveURL(/\/task$/);
  await expect(page.getByRole('heading', { name: 'Queue Flow Task' })).toBeVisible();
  expect(messages.filter((message) => message.type === 'join_queue')).toHaveLength(1);
  const storedGame = await page.evaluate(() => {
    const raw = window.localStorage.getItem('currentGame');
    return raw ? JSON.parse(raw) as { duel_id?: string; task?: { title?: string } } : null;
  });
  expect(storedGame?.duel_id).toBe(duelID);
  expect(storedGame?.task?.title).toBe('Queue Flow Task');
});

test('player flow uses session-token WS envelope and flag_submit payload', async ({ page }) => {
  const playerID = '11111111-1111-1111-1111-111111111111';
  const sessionToken = '22222222-2222-2222-2222-222222222222';
  const duelID = '33333333-3333-3333-3333-333333333333';
  const opponentID = '44444444-4444-4444-4444-444444444444';
  const taskID = '55555555-5555-5555-5555-555555555555';
  const deadline = inSecondsISO(180);
  const messages: Array<{ type: string; payload?: unknown }> = [];
  let websocketURL = '';
  let submitFlagRESTCalls = 0;

  await page.route('**/api/v1/players/join', async (route) => {
    expect(route.request().method()).toBe('POST');
    expect(route.request().postDataJSON()).toEqual({ username: 'alice' });
    await route.fulfill({
      status: 200,
      headers: jsonHeaders,
      body: JSON.stringify({
        player_id: playerID,
        session_token: sessionToken,
      }),
    });
  });

  await page.route('**/api/v1/players/me', async (route) => {
    expect(route.request().method()).toBe('GET');
    expect(route.request().headers()['x-session-token']).toBe(sessionToken);
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
        ...activeDuelAfterTaskNavigation(page, duelID, deadline),
      }),
    });
  });

  await page.route('**/submit-flag**', async (route) => {
    submitFlagRESTCalls += 1;
    await route.abort();
  });

  await page.routeWebSocket((url) => url.pathname === '/ws', (ws) => {
    websocketURL = ws.url();
    ws.onMessage((raw) => {
      const message = JSON.parse(String(raw)) as { type: string; payload?: { flag?: string } };
      messages.push(message);

      if (message.type === 'join_queue') {
        ws.send(JSON.stringify({ type: 'queue_joined' }));
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
              title: 'Contract Check',
              description: 'Submit the expected flag.',
              category: 'web',
              difficulty: 'easy',
              time_limit: 180,
              time_limit_seconds: 180,
              task_url: 'https://example.com/task',
              hint_schedule: [
                { hint_index: 1, unlock_at: inSecondsISO(30) },
                { hint_index: 2, unlock_at: inSecondsISO(60) },
                { hint_index: 3, unlock_at: inSecondsISO(90) },
              ],
            },
          },
        }));
      }

      if (message.type === 'flag_submit') {
        const correct = message.payload?.flag === 'ctf{ok}';
        ws.send(JSON.stringify({
          type: 'flag_result',
          payload: {
            duel_id: duelID,
            correct,
          },
        }));
        if (correct) {
          ws.send(JSON.stringify({
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
          }));
        }
      }
    });
  });

  await page.goto('/');
  await page.getByPlaceholder('Введите никнейм...').fill('alice');
  await page.getByRole('button', { name: /ПОДКЛЮЧИТЬСЯ/ }).click();
  await expect(page.getByText('Игрок готов')).toBeVisible();

  await page.getByRole('button', { name: /ИГРАТЬ/ }).click();
  await expect(page).toHaveURL(/\/task$/);
  await expect(page.getByRole('heading', { name: 'Contract Check' })).toBeVisible();
  await expect
    .poll(() => page.evaluate(() => Object.prototype.hasOwnProperty.call(window, 'gameWebSocket')))
    .toBe(false);

  await page.getByPlaceholder('ctf{...}').fill('wrong');
  await page.getByRole('button', { name: /Отправить/ }).click();
  await expect(page.getByText('Неверный флаг')).toBeVisible();

  await page.getByPlaceholder('ctf{...}').fill('ctf{ok}');
  await page.getByRole('button', { name: /Отправить/ }).click();
  await expect(page.getByText('ПОБЕДА!')).toBeVisible();

  expectWebSocketURLDoesNotLeakSession(websocketURL);
  expect(submitFlagRESTCalls).toBe(0);
  expect(messages.filter((message) => message.type === 'join_queue')).toHaveLength(1);
  expect(messages.every((message) => !('data' in message))).toBe(true);
  expect(messages).toContainEqual({ type: 'join_queue' });
  expect(messages).toContainEqual({
    type: 'flag_submit',
    payload: {
      duel_id: duelID,
      flag: 'wrong',
    },
  });
  expect(messages).toContainEqual({
    type: 'flag_submit',
    payload: {
      duel_id: duelID,
      flag: 'ctf{ok}',
    },
  });
});

test('home preserves immediate terminal result during task transition', async ({ page }) => {
  const playerID = '31313131-3131-3131-3131-313131313131';
  const sessionToken = '10000000-0000-0000-0000-000000000089';
  const duelID = '32323232-3232-3232-3232-323232323232';
  const opponentID = '33333333-3333-3333-3333-333333333333';
  const taskID = '34343434-3434-3434-3434-343434343434';
  const deadline = inSecondsISO(180);
  const messages: Array<{ type: string; payload?: unknown }> = [];

  await page.route('**/api/v1/players/join', async (route) => {
    await route.fulfill({
      status: 200,
      headers: jsonHeaders,
      body: JSON.stringify({
        player_id: playerID,
        session_token: sessionToken,
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
        ...activeDuelAfterTaskNavigation(page, duelID, deadline),
      }),
    });
  });

  await page.routeWebSocket((url) => url.pathname === '/ws', (ws) => {
    ws.onMessage((raw) => {
      const message = JSON.parse(String(raw)) as { type: string; payload?: unknown };
      messages.push(message);
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
            title: 'Immediate Terminal Transition',
            description: 'Terminal event arrives before the task page opens its own socket.',
            category: 'web',
            difficulty: 'easy',
            time_limit: 180,
            time_limit_seconds: 180,
            task_url: 'https://example.com/task',
            hint_schedule: [],
          },
        },
      }));
      ws.send(JSON.stringify({
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
      }));
    });
  });

  await page.goto('/');
  await page.getByPlaceholder('Введите никнейм...').fill('alice');
  await page.getByRole('button', { name: /ПОДКЛЮЧИТЬСЯ/ }).click();
  await expect(page.getByText('Игрок готов')).toBeVisible();

  await page.getByRole('button', { name: /ИГРАТЬ/ }).click();
  await expect(page).toHaveURL(/\/task$/);
  await expect(page.getByRole('heading', { name: 'ПОРАЖЕНИЕ' })).toBeVisible();

  const storedResult = await page.evaluate(() => {
    const raw = window.localStorage.getItem('game_result');
    return raw
      ? JSON.parse(raw) as { state?: string; source?: string; duel_id?: string; winner_id?: string }
      : null;
  });
  expect(storedResult).toMatchObject({
    state: 'lost',
    source: 'server',
    duel_id: duelID,
    winner_id: opponentID,
  });
  expect(messages.filter((message) => message.type === 'join_queue')).toHaveLength(1);
});

test('player queue flow renders pwn host-port task target as copy endpoint', async ({ page }) => {
  const playerID = '26262626-2626-2626-2626-262626262626';
  const sessionToken = '10000000-0000-0000-0000-000000000005';
  const duelID = '27272727-2727-2727-2727-272727272727';
  const opponentID = '28282828-2828-2828-2828-282828282828';
  const taskID = '29292929-2929-2929-2929-292929292929';
  const deadline = inSecondsISO(180);
  const messages: Array<{ type: string; payload?: unknown }> = [];
  let websocketURL = '';

  await page.addInitScript(() => {
    const testWindow = window as unknown as Window & {
      __openedUrls: WindowOpenCall[];
      __copiedTargets: string[];
    };
    testWindow.__openedUrls = [];
    testWindow.__copiedTargets = [];
    window.open = (url?: string | URL, target?: string, features?: string) => {
      testWindow.__openedUrls.push({
        url: url?.toString() || '',
        target,
        features,
      });
      return null;
    };
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: {
        writeText: async (value: string) => {
          testWindow.__copiedTargets.push(value);
        },
      },
    });
  });

  await page.route('**/api/v1/players/join', async (route) => {
    expect(route.request().method()).toBe('POST');
    expect(route.request().postDataJSON()).toEqual({ username: 'alice' });
    await route.fulfill({
      status: 200,
      headers: jsonHeaders,
      body: JSON.stringify({
        player_id: playerID,
        session_token: sessionToken,
      }),
    });
  });

  await page.route('**/api/v1/players/me', async (route) => {
    expect(route.request().method()).toBe('GET');
    expect(route.request().headers()['x-session-token']).toBe(sessionToken);
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
        ...activeDuelAfterTaskNavigation(page, duelID, deadline),
      }),
    });
  });

  await page.routeWebSocket((url) => url.pathname === '/ws', (ws) => {
    websocketURL = ws.url();
    ws.onMessage((raw) => {
      const message = JSON.parse(String(raw)) as { type: string; payload?: unknown };
      messages.push(message);

      if (message.type !== 'join_queue') {
        return;
      }

      ws.send(JSON.stringify({ type: 'queue_joined' }));
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
            title: 'Pwn Host Port Flow',
            description: 'Queue flow should preserve non-http task targets.',
            category: 'pwn',
            difficulty: 'easy',
            time_limit: 180,
            time_limit_seconds: 180,
            task_url: 'pwn.example:31337',
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
  await expect(page.getByRole('heading', { name: 'Pwn Host Port Flow' })).toBeVisible();
  await expect(page.getByText('Endpoint подключения')).toBeVisible();
  await expect(page.getByText('pwn.example:31337')).toBeVisible();

  await page.getByRole('button', { name: 'Копировать' }).click();
  await expect(page.getByText('Endpoint скопирован')).toBeVisible();

  const browserState = await page.evaluate(() => {
    const testWindow = window as unknown as Window & {
      __openedUrls?: WindowOpenCall[];
      __copiedTargets?: string[];
    };
    return {
      opened: testWindow.__openedUrls || [],
      copied: testWindow.__copiedTargets || [],
    };
  });

  expectWebSocketURLDoesNotLeakSession(websocketURL);
  expect(messages.filter((message) => message.type === 'join_queue')).toHaveLength(1);
  expect(browserState).toEqual({
    opened: [],
    copied: ['pwn.example:31337'],
  });
});

test('home queue websocket invalid session clears player state and waiting overlay', async ({ page }) => {
  const playerID = '41414141-4141-4141-4141-414141414141';
  const sessionToken = '10000000-0000-0000-0000-000000000041';
  const messages: Array<{ type: string; payload?: unknown }> = [];

  await page.addInitScript(({ playerID, sessionToken }) => {
    window.localStorage.setItem('player_id', playerID);
    window.localStorage.setItem('session_token', sessionToken);
    window.localStorage.setItem('username', 'alice');
  }, { playerID, sessionToken });

  await page.route('**/api/v1/players/me', async (route) => {
    expect(route.request().headers()['x-session-token']).toBe(sessionToken);
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

  await page.routeWebSocket((url) => url.pathname === '/ws', (ws) => {
    expectWebSocketURLDoesNotLeakSession(ws.url());
    ws.onMessage((raw) => {
      const message = JSON.parse(String(raw)) as { type: string; payload?: unknown };
      messages.push(message);
      if (message.type !== 'join_queue') {
        return;
      }
      ws.send(JSON.stringify({
        type: 'error',
        code: 'player.invalid_session',
        message: 'invalid session token',
      }));
      ws.close({ code: 1000 }).catch(() => undefined);
    });
  });

  await page.goto('/');
  await expect(page.getByText('Игрок готов')).toBeVisible();
  await page.getByRole('button', { name: /ИГРАТЬ/ }).click();

  await expect(page.getByText('Сессия истекла. Введите никнейм заново.')).toBeVisible();
  await expect(page.getByPlaceholder('Введите никнейм...')).toBeVisible();
  await expect(page.getByText('Игрок готов')).toBeHidden();
  await expect(page.getByRole('button', { name: 'Отменить поиск' })).toBeHidden();
  expect(messages.filter((message) => message.type === 'join_queue')).toHaveLength(1);
  await expect.poll(() => page.evaluate(() => window.localStorage.getItem('player_id'))).toBeNull();
  await expect.poll(() => page.evaluate(() => window.localStorage.getItem('session_token'))).toBeNull();
  await expect.poll(() => page.evaluate(() => window.localStorage.getItem('username'))).toBeNull();
  await expect.poll(() => page.evaluate(() => window.localStorage.getItem('currentGame'))).toBeNull();
});

test('queue cancel sends leave_queue for an active queue flow', async ({ page }) => {
  const playerID = '42424242-4242-4242-4242-424242424242';
  const sessionToken = '10000000-0000-0000-0000-000000000042';
  const messages: Array<{ type: string; payload?: unknown }> = [];

  await page.addInitScript(({ playerID, sessionToken }) => {
    window.localStorage.setItem('player_id', playerID);
    window.localStorage.setItem('session_token', sessionToken);
    window.localStorage.setItem('username', 'alice');
  }, { playerID, sessionToken });

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
      }),
    });
  });

  await page.routeWebSocket((url) => url.pathname === '/ws', (ws) => {
    ws.onMessage((raw) => {
      messages.push(JSON.parse(String(raw)) as { type: string; payload?: unknown });
    });
  });

  await page.goto('/');
  await expect(page.getByText('Игрок готов')).toBeVisible();
  await page.getByRole('button', { name: /ИГРАТЬ/ }).click();
  await expect.poll(() => messages.some((message) => message.type === 'join_queue')).toBe(true);

  await page.getByRole('button', { name: 'Отменить поиск' }).click();
  await expect(page.getByRole('button', { name: 'Отменить поиск' })).toBeHidden();
  await expect.poll(() => messages.filter((message) => message.type === 'leave_queue').length).toBe(1);
  expect(messages.filter((message) => message.type === 'join_queue')).toHaveLength(1);
});

test('cancelled queue attempt does not send delayed join_queue', async ({ page }) => {
  const playerID = '88888888-8888-8888-8888-888888888888';
  const sessionToken = '10000000-0000-0000-0000-000000000006';
  let releasePlayerMe: () => void = () => {};
  let meCalls = 0;
  let meCompleted = false;
  let websocketOpened = false;
  const messages: Array<{ type: string; payload?: unknown }> = [];
  const playerMeGate = new Promise<void>((resolve) => {
    releasePlayerMe = resolve;
  });

  await page.addInitScript(({ playerID, sessionToken }) => {
    window.localStorage.setItem('player_id', playerID);
    window.localStorage.setItem('session_token', sessionToken);
    window.localStorage.setItem('username', 'alice');
  }, { playerID, sessionToken });

  await page.route('**/api/v1/players/me', async (route) => {
    meCalls += 1;
    expect(route.request().headers()['x-session-token']).toBe(sessionToken);
    await playerMeGate;
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
    meCompleted = true;
  });

  await page.routeWebSocket((url) => url.pathname === '/ws', (ws) => {
    websocketOpened = true;
    ws.onMessage((raw) => {
      messages.push(JSON.parse(String(raw)) as { type: string; payload?: unknown });
    });
  });

  await page.goto('/');
  await expect(page.getByText('Игрок готов')).toBeVisible();

  await page.getByRole('button', { name: /ИГРАТЬ/ }).click();
  await expect(page.getByRole('button', { name: 'Отменить поиск' })).toBeVisible();
  await expect.poll(() => meCalls).toBe(1);

  await page.getByRole('button', { name: 'Отменить поиск' }).click();
  await expect(page.getByRole('button', { name: 'Отменить поиск' })).toBeHidden();

  releasePlayerMe();
  await expect.poll(() => meCompleted).toBe(true);
  await page.waitForTimeout(150);

  expect(websocketOpened).toBe(false);
  expect(messages.filter((message) => message.type === 'join_queue')).toHaveLength(0);
});

test('queue websocket reconnect resends join_queue for current search', async ({ page }) => {
  await page.clock.install({ time: new Date('2026-01-01T00:00:00Z') });
  await page.addInitScript(() => {
    Math.random = () => 0;
  });

  const playerID = '89898989-8989-8989-8989-898989898989';
  const sessionToken = '10000000-0000-0000-0000-000000000090';
  const messages: Array<{ socket: number; type: string; payload?: unknown }> = [];
  let socketCount = 0;

  await page.addInitScript(({ playerID, sessionToken }) => {
    window.localStorage.setItem('player_id', playerID);
    window.localStorage.setItem('session_token', sessionToken);
    window.localStorage.setItem('username', 'alice');
  }, { playerID, sessionToken });

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
      }),
    });
  });

  await page.routeWebSocket((url) => url.pathname === '/ws', (ws) => {
    const socketIndex = socketCount;
    socketCount += 1;
    expectWebSocketURLDoesNotLeakSession(ws.url());
    ws.onMessage((raw) => {
      const message = JSON.parse(String(raw)) as { type: string; payload?: unknown };
      messages.push({ socket: socketIndex, ...message });
      if (message.type !== 'join_queue') {
        return;
      }
      if (socketIndex === 0) {
        ws.close({ code: 1011, reason: 'backend restart' }).catch(() => undefined);
        return;
      }
      ws.send(JSON.stringify({ type: 'queue_joined' }));
    });
  });

  await page.goto('/');
  await expect(page.getByText('Игрок готов')).toBeVisible();
  await page.getByRole('button', { name: /ИГРАТЬ/ }).click();

  await expect.poll(() => messages.filter((message) => message.type === 'join_queue').length).toBe(1);
  await page.clock.fastForward(1000);
  await expect.poll(() => messages.filter((message) => message.type === 'join_queue').length).toBe(2);
  await expect(page.getByRole('button', { name: 'Отменить поиск' })).toBeVisible();
  expect(messages.filter((message) => message.type === 'join_queue').map((message) => message.socket)).toEqual([0, 1]);
});

test('cancelled search does not send join_queue on delayed reconnect', async ({ page }) => {
  await page.clock.install({ time: new Date('2026-01-01T00:00:00Z') });
  await page.addInitScript(() => {
    Math.random = () => 0;
  });

  const playerID = '8a8a8a8a-8a8a-8a8a-8a8a-8a8a8a8a8a8a';
  const sessionToken = '10000000-0000-0000-0000-000000000091';
  const messages: Array<{ socket: number; type: string; payload?: unknown }> = [];
  let socketCount = 0;

  await page.addInitScript(({ playerID, sessionToken }) => {
    window.localStorage.setItem('player_id', playerID);
    window.localStorage.setItem('session_token', sessionToken);
    window.localStorage.setItem('username', 'alice');
  }, { playerID, sessionToken });

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
      }),
    });
  });

  await page.routeWebSocket((url) => url.pathname === '/ws', (ws) => {
    const socketIndex = socketCount;
    socketCount += 1;
    ws.onMessage((raw) => {
      const message = JSON.parse(String(raw)) as { type: string; payload?: unknown };
      messages.push({ socket: socketIndex, ...message });
      if (message.type === 'join_queue') {
        ws.close({ code: 1011, reason: 'backend restart' }).catch(() => undefined);
      }
    });
  });

  await page.goto('/');
  await expect(page.getByText('Игрок готов')).toBeVisible();
  await page.getByRole('button', { name: /ИГРАТЬ/ }).click();
  await expect.poll(() => messages.filter((message) => message.type === 'join_queue').length).toBe(1);

  await page.getByRole('button', { name: 'Отменить поиск' }).click();
  await expect(page.getByRole('button', { name: 'Отменить поиск' })).toBeHidden();
  await page.clock.fastForward(5000);
  await page.waitForTimeout(100);

  expect(socketCount).toBe(1);
  expect(messages.filter((message) => message.type === 'join_queue')).toHaveLength(1);
});

test('restore websocket reconnect never sends join_queue', async ({ page }) => {
  await page.clock.install({ time: new Date('2026-01-01T00:00:00Z') });
  await page.addInitScript(() => {
    Math.random = () => 0;
  });

  const playerID = '8b8b8b8b-8b8b-8b8b-8b8b-8b8b8b8b8b8b';
  const sessionToken = '10000000-0000-0000-0000-000000000092';
  const duelID = '8c8c8c8c-8c8c-8c8c-8c8c-8c8c8c8c8c8c';
  const opponentID = '8d8d8d8d-8d8d-8d8d-8d8d-8d8d8d8d8d8d';
  const taskID = '8e8e8e8e-8e8e-8e8e-8e8e-8e8e8e8e8e8e';
  const deadline = inSecondsISO(180);
  const messages: Array<{ socket: number; type: string; payload?: unknown }> = [];
  let socketCount = 0;

  await page.addInitScript(({ playerID, sessionToken }) => {
    window.localStorage.setItem('player_id', playerID);
    window.localStorage.setItem('session_token', sessionToken);
    window.localStorage.setItem('username', 'alice');
  }, { playerID, sessionToken });

  await page.route('**/api/v1/players/me', async (route) => {
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
          id: duelID,
          status: 'active',
          deadline,
          started_at: nowISO(),
        },
      }),
    });
  });

  await page.routeWebSocket((url) => url.pathname === '/ws', (ws) => {
    const socketIndex = socketCount;
    socketCount += 1;
    ws.onMessage((raw) => {
      const message = JSON.parse(String(raw)) as { type: string; payload?: unknown };
      messages.push({ socket: socketIndex, ...message });
    });
    if (socketIndex === 0) {
      setTimeout(() => {
        ws.close({ code: 1011, reason: 'backend restart' }).catch(() => undefined);
      }, 0);
      return;
    }
    setTimeout(() => {
      ws.send(JSON.stringify({
        type: 'duel_resume',
        payload: {
          duel_id: duelID,
          opponent_id: opponentID,
          deadline,
          opponent_disconnected: false,
          task: {
            id: taskID,
            title: 'Restore Reconnect Task',
            description: 'Restore reconnect should not enter queue.',
            category: 'web',
            difficulty: 'easy',
            time_limit: 180,
            time_limit_seconds: 180,
            task_url: 'https://example.com/task',
            hint_schedule: [],
          },
        },
      }));
    }, 0);
  });

  await page.goto('/');
  await expect(page.getByText('Игрок готов')).toBeVisible();
  await page.getByRole('button', { name: /ИГРАТЬ/ }).click();
  await expect.poll(() => socketCount).toBe(1);
  await page.clock.fastForward(1000);

  await expect(page).toHaveURL(/\/task$/);
  await expect(page.getByRole('heading', { name: 'Restore Reconnect Task' })).toBeVisible();
  expect(messages.filter((message) => message.type === 'join_queue')).toHaveLength(0);
  expect(socketCount).toBeGreaterThanOrEqual(2);
});

test('rapid websocket reconnect failures eventually give up', async ({ page }) => {
  await page.clock.install({ time: new Date('2026-01-01T00:00:00Z') });
  await page.addInitScript(() => {
    Math.random = () => 0;
    const sentMessages: string[] = [];
    const openedUrls: string[] = [];

    class FailingWebSocket extends EventTarget {
      static CONNECTING = 0;
      static OPEN = 1;
      static CLOSING = 2;
      static CLOSED = 3;

      readonly url: string;
      readonly protocol = '';
      readonly extensions = '';
      binaryType: BinaryType = 'blob';
      bufferedAmount = 0;
      readyState = FailingWebSocket.CONNECTING;
      onopen: ((event: Event) => void) | null = null;
      onmessage: ((event: MessageEvent) => void) | null = null;
      onerror: ((event: Event) => void) | null = null;
      onclose: ((event: CloseEvent) => void) | null = null;

      constructor(url: string | URL) {
        super();
        this.url = String(url);
        openedUrls.push(this.url);
        setTimeout(() => {
          if (this.readyState !== FailingWebSocket.CONNECTING) {
            return;
          }
          this.readyState = FailingWebSocket.OPEN;
          this.dispatchEvent(new Event('open'));
          this.serverClose();
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

      send(data: string | ArrayBufferLike | Blob | ArrayBufferView): void {
        sentMessages.push(String(data));
      }

      close(code = 1000, reason = ''): void {
        this.closeWith(code, reason);
      }

      private serverClose(): void {
        this.closeWith(1011, 'backend restart');
      }

      private closeWith(code: number, reason: string): void {
        if (this.readyState === FailingWebSocket.CLOSED) {
          return;
        }
        this.readyState = FailingWebSocket.CLOSED;
        this.dispatchEvent(new CloseEvent('close', { code, reason }));
      }
    }

    window.WebSocket = FailingWebSocket as unknown as typeof WebSocket;
    Object.assign(window, {
      __failingWebSocketSentMessages: sentMessages,
      __failingWebSocketOpenedUrls: openedUrls,
    });
  });

  const playerID = '8f8f8f8f-8f8f-8f8f-8f8f-8f8f8f8f8f8f';
  const sessionToken = '10000000-0000-0000-0000-000000000093';

  await page.addInitScript(({ playerID, sessionToken }) => {
    window.localStorage.setItem('player_id', playerID);
    window.localStorage.setItem('session_token', sessionToken);
    window.localStorage.setItem('username', 'alice');
  }, { playerID, sessionToken });

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
      }),
    });
  });

  await page.goto('/');
  await expect(page.getByText('Игрок готов')).toBeVisible();
  await page.getByRole('button', { name: /ИГРАТЬ/ }).click();

  await expect
    .poll(() => page.evaluate(() => {
      const messages = (window as unknown as { __failingWebSocketSentMessages: string[] })
        .__failingWebSocketSentMessages;
      return messages.filter((raw) => JSON.parse(raw).type === 'join_queue').length;
    }))
    .toBe(1);

  let giveUpVisible = false;
  for (let i = 0; i < 220; i += 1) {
    await page.clock.fastForward(1_000);
    giveUpVisible = await page
      .getByText('Соединение потеряно. Обновите страницу.')
      .isVisible()
      .catch(() => false);
    if (giveUpVisible) {
      break;
    }
  }

  const clientState = await page.evaluate(() => {
    const state = window as unknown as {
      __failingWebSocketSentMessages: string[];
      __failingWebSocketOpenedUrls: string[];
    };
    return {
      joinQueueCount: state.__failingWebSocketSentMessages
        .filter((raw) => JSON.parse(raw).type === 'join_queue')
        .length,
      openedUrls: state.__failingWebSocketOpenedUrls
        .filter((url) => new URL(url).pathname === '/ws'),
    };
  });

  expect(clientState.joinQueueCount).toBe(11);
  expect(clientState.openedUrls).toHaveLength(11);
  expect(giveUpVisible).toBe(true);
  for (const url of clientState.openedUrls) {
    expectWebSocketURLDoesNotLeakSession(url);
  }
});

test('home queue websocket handshake failure clears waiting state', async ({ page }) => {
  await page.addInitScript(() => {
    class RejectedWebSocket extends EventTarget {
      static CONNECTING = 0;
      static OPEN = 1;
      static CLOSING = 2;
      static CLOSED = 3;

      readonly url: string;
      readonly protocol = '';
      readonly extensions = '';
      binaryType: BinaryType = 'blob';
      bufferedAmount = 0;
      readyState = RejectedWebSocket.CONNECTING;
      onopen: ((event: Event) => void) | null = null;
      onmessage: ((event: MessageEvent) => void) | null = null;
      onerror: ((event: Event) => void) | null = null;
      onclose: ((event: CloseEvent) => void) | null = null;

      constructor(url: string | URL) {
        super();
        this.url = String(url);
        setTimeout(() => {
          this.readyState = RejectedWebSocket.CLOSED;
          this.dispatchEvent(new Event('error'));
          this.dispatchEvent(new CloseEvent('close', { code: 1006 }));
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
      close(): void {
        this.readyState = RejectedWebSocket.CLOSED;
      }
    }

    window.WebSocket = RejectedWebSocket as unknown as typeof WebSocket;
  });

  const playerID = '91919191-9191-9191-9191-919191919191';
  const sessionToken = '10000000-0000-0000-0000-000000000191';

  await page.addInitScript(({ playerID, sessionToken }) => {
    window.localStorage.setItem('player_id', playerID);
    window.localStorage.setItem('session_token', sessionToken);
    window.localStorage.setItem('username', 'alice');
  }, { playerID, sessionToken });

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
      }),
    });
  });

  await page.goto('/');
  await expect(page.getByText('Игрок готов')).toBeVisible();
  await page.getByRole('button', { name: /ИГРАТЬ/ }).click();

  await expect(page.getByText('Ошибка WebSocket соединения. Проверьте адрес страницы и обновите.')).toBeVisible();
  await expect(page.getByRole('button', { name: 'Отменить поиск' })).toBeHidden();
  await expect(page.getByText('Игрок готов')).toBeVisible();
});

test('existing active duel restores through WS without sending join_queue', async ({ page }) => {
  const playerID = 'eeeeeeee-eeee-eeee-eeee-eeeeeeeeeeee';
  const sessionToken = '10000000-0000-0000-0000-000000000007';
  const duelID = 'ffffffff-ffff-ffff-ffff-ffffffffffff';
  const opponentID = '12121212-1212-1212-1212-121212121212';
  const taskID = '13131313-1313-1313-1313-131313131313';
  const deadline = inSecondsISO(180);
  const messages: Array<{ type: string; payload?: unknown }> = [];
  let activeSocket: WebSocketRoute | null = null;
  const sendServerEvent = (event: unknown) => {
    if (!activeSocket) {
      throw new Error('WebSocket was not opened');
    }
    activeSocket.send(JSON.stringify(event));
  };

  await page.addInitScript(({ playerID, sessionToken }) => {
    window.localStorage.setItem('player_id', playerID);
    window.localStorage.setItem('session_token', sessionToken);
    window.localStorage.setItem('username', 'alice');
  }, { playerID, sessionToken });

  await page.route('**/api/v1/players/me', async (route) => {
    expect(route.request().headers()['x-session-token']).toBe(sessionToken);
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
          id: duelID,
          status: 'active',
          deadline,
          started_at: nowISO(),
        },
      }),
    });
  });

  await page.routeWebSocket((url) => url.pathname === '/ws', (ws) => {
    expectWebSocketURLDoesNotLeakSession(ws.url());
    activeSocket = ws;
    ws.onMessage((raw) => {
      messages.push(JSON.parse(String(raw)) as { type: string; payload?: unknown });
    });
  });

  await page.goto('/');
  await expect(page.getByText('Игрок готов')).toBeVisible();

  await page.getByRole('button', { name: /ИГРАТЬ/ }).click();
  await expect.poll(() => activeSocket !== null).toBeTruthy();
  sendServerEvent({
    type: 'duel_resume',
    payload: {
      duel_id: duelID,
      opponent_id: opponentID,
      deadline,
      opponent_disconnected: false,
      task: {
        id: taskID,
        title: 'Restored Active Duel',
        description: 'This task came from duel_resume.',
        category: 'web',
        difficulty: 'easy',
        time_limit: 180,
        time_limit_seconds: 180,
        task_url: 'https://example.com/task',
        hint_schedule: [],
      },
    },
  });
  await expect(page).toHaveURL(/\/task$/);
  await expect(page.getByRole('heading', { name: 'Restored Active Duel' })).toBeVisible();
  const storedGame = await page.evaluate(() => {
    const raw = window.localStorage.getItem('currentGame');
    return raw ? JSON.parse(raw) as { opponent_id?: string } : null;
  });
  expect(storedGame?.opponent_id).toBe(opponentID);

  expect(messages.filter((message) => message.type === 'join_queue')).toHaveLength(0);
});

test('play action refreshes session after mount preflight resolved idle', async ({ page }) => {
  const playerID = '51515151-5151-5151-5151-515151515151';
  const sessionToken = '10000000-0000-0000-0000-000000000151';
  const duelID = '52525252-5252-5252-5252-525252525252';
  const opponentID = '53535353-5353-5353-5353-535353535353';
  const taskID = '54545454-5454-5454-5454-545454545454';
  const deadline = inSecondsISO(180);
  const messages: Array<{ type: string; payload?: unknown }> = [];
  let meCalls = 0;
  let activeSocket: WebSocketRoute | null = null;
  const sendServerEvent = (event: unknown) => {
    if (!activeSocket) {
      throw new Error('WebSocket was not opened');
    }
    activeSocket.send(JSON.stringify(event));
  };

  await page.addInitScript(({ playerID, sessionToken }) => {
    window.localStorage.setItem('player_id', playerID);
    window.localStorage.setItem('session_token', sessionToken);
    window.localStorage.setItem('username', 'alice');
  }, { playerID, sessionToken });

  await page.route('**/api/v1/players/me', async (route) => {
    meCalls += 1;
    const hasActiveDuel = meCalls >= 2;
    await route.fulfill({
      status: 200,
      headers: jsonHeaders,
      body: JSON.stringify({
        player: {
          id: playerID,
          username: 'alice',
          status: hasActiveDuel ? 'in_duel' : 'idle',
          created_at: nowISO(),
        },
        ...(hasActiveDuel
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
    activeSocket = ws;
    ws.onMessage((raw) => {
      messages.push(JSON.parse(String(raw)) as { type: string; payload?: unknown });
    });
  });

  await page.goto('/');
  await expect(page.getByText('Игрок готов')).toBeVisible();
  await expect.poll(() => meCalls).toBe(1);

  await page.getByRole('button', { name: /ИГРАТЬ/ }).click();
  await expect.poll(() => meCalls).toBe(2);
  await expect(page.getByText('Восстанавливаем активную дуэль...')).toBeVisible();
  await expect.poll(() => activeSocket !== null).toBeTruthy();

  sendServerEvent({
    type: 'duel_resume',
    payload: {
      duel_id: duelID,
      opponent_id: opponentID,
      deadline,
      opponent_disconnected: false,
      task: {
        id: taskID,
        title: 'Fresh Restore After Idle',
        description: 'Play must use a fresh session preflight.',
        category: 'web',
        difficulty: 'easy',
        time_limit: 180,
        time_limit_seconds: 180,
        task_url: 'https://example.com/task',
        hint_schedule: [],
      },
    },
  });

  await expect(page).toHaveURL(/\/task$/);
  await expect(page.getByRole('heading', { name: 'Fresh Restore After Idle' })).toBeVisible();
  expect(messages.filter((message) => message.type === 'join_queue')).toHaveLength(0);
});

test('active duel restore failure shows a user-facing error', async ({ page }) => {
  const playerID = '23232323-2323-2323-2323-232323232323';
  const sessionToken = '10000000-0000-0000-0000-000000000008';
  const duelID = '24242424-2424-2424-2424-242424242424';
  const deadline = inSecondsISO(180);
  const messages: Array<{ type: string; payload?: unknown }> = [];
  let activeSocket: WebSocketRoute | null = null;
  const closeActiveSocket = async () => {
    if (!activeSocket) {
      throw new Error('WebSocket was not opened');
    }
    await activeSocket.close({ code: 1000 });
  };

  await page.addInitScript(({ playerID, sessionToken }) => {
    window.localStorage.setItem('player_id', playerID);
    window.localStorage.setItem('session_token', sessionToken);
    window.localStorage.setItem('username', 'alice');
  }, { playerID, sessionToken });

  await page.route('**/api/v1/players/me', async (route) => {
    expect(route.request().headers()['x-session-token']).toBe(sessionToken);
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
          id: duelID,
          status: 'active',
          deadline,
          started_at: nowISO(),
        },
      }),
    });
  });

  await page.routeWebSocket((url) => url.pathname === '/ws', (ws) => {
    expectWebSocketURLDoesNotLeakSession(ws.url());
    activeSocket = ws;
    ws.onMessage((raw) => {
      messages.push(JSON.parse(String(raw)) as { type: string; payload?: unknown });
    });
  });

  await page.goto('/');
  await expect(page.getByText('Игрок готов')).toBeVisible();

  await page.getByRole('button', { name: /ИГРАТЬ/ }).click();
  await expect.poll(() => activeSocket !== null).toBeTruthy();
  await closeActiveSocket();

  await expect(page.getByText('Не удалось восстановить активную дуэль. Обновите страницу.')).toBeVisible();
  await expect(page).toHaveURL(/\/$/);
  expect(messages.filter((message) => message.type === 'join_queue')).toHaveLength(0);
});

test('active duel restore without task shows failure instead of hanging', async ({ page }) => {
  const playerID = '43434343-4343-4343-4343-434343434343';
  const sessionToken = '10000000-0000-0000-0000-000000000043';
  const duelID = '44444444-4444-4444-4444-444444444444';
  const deadline = inSecondsISO(180);
  const messages: Array<{ type: string; payload?: unknown }> = [];
  let activeSocket: WebSocketRoute | null = null;
  const sendServerEvent = (event: unknown) => {
    if (!activeSocket) {
      throw new Error('WebSocket was not opened');
    }
    activeSocket.send(JSON.stringify(event));
  };

  await page.addInitScript(({ playerID, sessionToken }) => {
    window.localStorage.setItem('player_id', playerID);
    window.localStorage.setItem('session_token', sessionToken);
    window.localStorage.setItem('username', 'alice');
  }, { playerID, sessionToken });

  await page.route('**/api/v1/players/me', async (route) => {
    expect(route.request().headers()['x-session-token']).toBe(sessionToken);
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
          id: duelID,
          status: 'active',
          deadline,
          started_at: nowISO(),
        },
      }),
    });
  });

  await page.routeWebSocket((url) => url.pathname === '/ws', (ws) => {
    expectWebSocketURLDoesNotLeakSession(ws.url());
    activeSocket = ws;
    ws.onMessage((raw) => {
      messages.push(JSON.parse(String(raw)) as { type: string; payload?: unknown });
    });
  });

  await page.goto('/');
  await expect(page.getByText('Игрок готов')).toBeVisible();
  await page.getByRole('button', { name: /ИГРАТЬ/ }).click();
  await expect.poll(() => activeSocket !== null).toBeTruthy();

  sendServerEvent({
    type: 'duel_resume',
    payload: {
      duel_id: duelID,
      deadline,
      opponent_disconnected: false,
    },
  });

  await expect(page.getByText('Не удалось восстановить активную дуэль. Обновите страницу.')).toBeVisible();
  await expect(page.getByRole('button', { name: 'Отменить поиск' })).toBeHidden();
  await expect(page).toHaveURL(/\/$/);
  expect(messages.filter((message) => message.type === 'join_queue')).toHaveLength(0);
  expect(messages.filter((message) => message.type === 'leave_queue')).toHaveLength(0);
  expect(await page.evaluate(() => window.localStorage.getItem('currentGame'))).toBeNull();
});

test('restore cancel does not send leave_queue', async ({ page }) => {
  const playerID = '45454545-4545-4545-4545-454545454545';
  const sessionToken = '10000000-0000-0000-0000-000000000045';
  const duelID = '46464646-4646-4646-4646-464646464646';
  const deadline = inSecondsISO(180);
  const messages: Array<{ type: string; payload?: unknown }> = [];
  let activeSocket: WebSocketRoute | null = null;

  await page.addInitScript(({ playerID, sessionToken }) => {
    window.localStorage.setItem('player_id', playerID);
    window.localStorage.setItem('session_token', sessionToken);
    window.localStorage.setItem('username', 'alice');
  }, { playerID, sessionToken });

  await page.route('**/api/v1/players/me', async (route) => {
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
          id: duelID,
          status: 'active',
          deadline,
          started_at: nowISO(),
        },
      }),
    });
  });

  await page.routeWebSocket((url) => url.pathname === '/ws', (ws) => {
    activeSocket = ws;
    ws.onMessage((raw) => {
      messages.push(JSON.parse(String(raw)) as { type: string; payload?: unknown });
    });
  });

  await page.goto('/');
  await expect(page.getByText('Игрок готов')).toBeVisible();
  await page.getByRole('button', { name: /ИГРАТЬ/ }).click();
  await expect.poll(() => activeSocket !== null).toBeTruthy();
  await expect(page.getByRole('button', { name: 'Отменить поиск' })).toBeVisible();

  await page.getByRole('button', { name: 'Отменить поиск' }).click();
  await expect(page.getByRole('button', { name: 'Отменить поиск' })).toBeHidden();
  await page.waitForTimeout(100);

  expect(messages.filter((message) => message.type === 'join_queue')).toHaveLength(0);
  expect(messages.filter((message) => message.type === 'leave_queue')).toHaveLength(0);
});

test('active duel restore ignores mismatched duel_resume until matching resume arrives', async ({ page }) => {
  const playerID = 'a1a1a1a1-a1a1-a1a1-a1a1-a1a1a1a1a1a1';
  const sessionToken = '10000000-0000-0000-0000-000000000009';
  const duelID = 'a2a2a2a2-a2a2-a2a2-a2a2-a2a2a2a2a2a2';
  const wrongDuelID = 'a3a3a3a3-a3a3-a3a3-a3a3-a3a3a3a3a3a3';
  const deadline = inSecondsISO(180);
  const wrongDeadline = inSecondsISO(240);
  const task = {
    id: 'a4a4a4a4-a4a4-a4a4-a4a4-a4a4a4a4a4a4',
    title: 'Matching Restore Task',
    description: 'Only the active_duel restore id may enter the task page.',
    category: 'web',
    difficulty: 'easy',
    time_limit: 180,
    time_limit_seconds: 180,
    task_url: 'https://example.com/task',
    hint_schedule: [],
  };
  const wrongTask = {
    ...task,
    id: 'a5a5a5a5-a5a5-a5a5-a5a5-a5a5a5a5a5a5',
    title: 'Wrong Restore Task',
  };
  const messages: Array<{ type: string; payload?: unknown }> = [];
  let activeSocket: WebSocketRoute | null = null;
  const sendServerEvent = (event: unknown) => {
    if (!activeSocket) {
      throw new Error('WebSocket was not opened');
    }
    activeSocket.send(JSON.stringify(event));
  };

  await page.addInitScript(({ playerID, sessionToken }) => {
    window.localStorage.setItem('player_id', playerID);
    window.localStorage.setItem('session_token', sessionToken);
    window.localStorage.setItem('username', 'alice');
  }, { playerID, sessionToken });

  await page.route('**/api/v1/players/me', async (route) => {
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
          id: duelID,
          status: 'active',
          deadline,
          started_at: nowISO(),
        },
      }),
    });
  });

  await page.routeWebSocket((url) => url.pathname === '/ws', (ws) => {
    expectWebSocketURLDoesNotLeakSession(ws.url());
    activeSocket = ws;
    ws.onMessage((raw) => {
      messages.push(JSON.parse(String(raw)) as { type: string; payload?: unknown });
    });
  });

  await page.goto('/');
  await expect(page.getByText('Игрок готов')).toBeVisible();
  await page.getByRole('button', { name: /ИГРАТЬ/ }).click();
  await expect.poll(() => activeSocket !== null).toBeTruthy();

  sendServerEvent({
    type: 'duel_resume',
    payload: {
      duel_id: wrongDuelID,
      deadline: wrongDeadline,
      opponent_disconnected: false,
      task: wrongTask,
    },
  });

  await page.waitForTimeout(150);
  expect(page.url()).toMatch(/\/$/);
  await expect(page.getByRole('button', { name: 'Отменить поиск' })).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Wrong Restore Task' })).toBeHidden();
  expect(await page.evaluate(() => window.localStorage.getItem('currentGame'))).toBeNull();

  sendServerEvent({
    type: 'duel_resume',
    payload: {
      duel_id: duelID,
      deadline,
      opponent_disconnected: false,
      task,
    },
  });

  await expect(page).toHaveURL(/\/task$/);
  await expect(page.getByRole('heading', { name: 'Matching Restore Task' })).toBeVisible();
  expect(messages.filter((message) => message.type === 'join_queue')).toHaveLength(0);
  const storedGame = await page.evaluate(() => {
    const raw = window.localStorage.getItem('currentGame');
    return raw ? JSON.parse(raw) as { duel_id?: string; task?: { title?: string } } : null;
  });
  expect(storedGame?.duel_id).toBe(duelID);
  expect(storedGame?.task?.title).toBe('Matching Restore Task');
});

test('active duel restore ignores self-opponent duel_resume until valid resume arrives', async ({ page }) => {
  const playerID = 'b1b1b1b1-b1b1-b1b1-b1b1-b1b1b1b1b1b1';
  const sessionToken = '10000000-0000-0000-0000-000000000089';
  const duelID = 'b2b2b2b2-b2b2-b2b2-b2b2-b2b2b2b2b2b2';
  const opponentID = 'b3b3b3b3-b3b3-b3b3-b3b3-b3b3b3b3b3b3';
  const deadline = inSecondsISO(180);
  const task = {
    id: 'b4b4b4b4-b4b4-b4b4-b4b4-b4b4b4b4b4b4',
    title: 'Valid Opponent Restore Task',
    description: 'Self opponent resume must not restore this duel.',
    category: 'web',
    difficulty: 'easy',
    time_limit: 180,
    time_limit_seconds: 180,
    task_url: 'https://example.com/task',
    hint_schedule: [],
  };
  const selfTask = {
    ...task,
    id: 'b5b5b5b5-b5b5-b5b5-b5b5-b5b5b5b5b5b5',
    title: 'Self Opponent Restore Task',
  };
  let activeSocket: WebSocketRoute | null = null;
  const sendServerEvent = (event: unknown) => {
    if (!activeSocket) {
      throw new Error('WebSocket was not opened');
    }
    activeSocket.send(JSON.stringify(event));
  };

  await page.addInitScript(({ playerID, sessionToken }) => {
    window.localStorage.setItem('player_id', playerID);
    window.localStorage.setItem('session_token', sessionToken);
    window.localStorage.setItem('username', 'alice');
  }, { playerID, sessionToken });

  await page.route('**/api/v1/players/me', async (route) => {
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
          id: duelID,
          status: 'active',
          deadline,
          started_at: nowISO(),
        },
      }),
    });
  });

  await page.routeWebSocket((url) => url.pathname === '/ws', (ws) => {
    activeSocket = ws;
  });

  await page.goto('/');
  await expect(page.getByText('Игрок готов')).toBeVisible();
  await page.getByRole('button', { name: /ИГРАТЬ/ }).click();
  await expect.poll(() => activeSocket !== null).toBeTruthy();

  sendServerEvent({
    type: 'duel_resume',
    payload: {
      duel_id: duelID,
      opponent_id: playerID,
      deadline,
      opponent_disconnected: false,
      task: selfTask,
    },
  });

  await page.waitForTimeout(150);
  expect(page.url()).toMatch(/\/$/);
  await expect(page.getByRole('button', { name: 'Отменить поиск' })).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Self Opponent Restore Task' })).toBeHidden();
  expect(await page.evaluate(() => window.localStorage.getItem('currentGame'))).toBeNull();

  sendServerEvent({
    type: 'duel_resume',
    payload: {
      duel_id: duelID,
      opponent_id: opponentID,
      deadline,
      opponent_disconnected: false,
      task,
    },
  });

  await expect(page).toHaveURL(/\/task$/);
  await expect(page.getByRole('heading', { name: 'Valid Opponent Restore Task' })).toBeVisible();
  const storedGame = await page.evaluate(() => {
    const raw = window.localStorage.getItem('currentGame');
    return raw ? JSON.parse(raw) as { opponent_id?: string; task?: { title?: string } } : null;
  });
  expect(storedGame?.opponent_id).toBe(opponentID);
  expect(storedGame?.task?.title).toBe('Valid Opponent Restore Task');
});

test('active duel restore ignores queue events until matching resume arrives', async ({ page }) => {
  const playerID = 'c1c1c1c1-c1c1-c1c1-c1c1-c1c1c1c1c1c1';
  const sessionToken = '10000000-0000-0000-0000-000000000010';
  const duelID = 'c2c2c2c2-c2c2-c2c2-c2c2-c2c2c2c2c2c2';
  const wrongDuelID = 'c3c3c3c3-c3c3-c3c3-c3c3-c3c3c3c3c3c3';
  const opponentID = 'c4c4c4c4-c4c4-c4c4-c4c4-c4c4c4c4c4c4';
  const deadline = inSecondsISO(180);
  const task = {
    id: 'c5c5c5c5-c5c5-c5c5-c5c5-c5c5c5c5c5c5',
    title: 'Restore Flow Task',
    description: 'Restore mode should ignore queue events.',
    category: 'web',
    difficulty: 'easy',
    time_limit: 180,
    time_limit_seconds: 180,
    task_url: 'https://example.com/task',
    hint_schedule: [],
  };
  const wrongTask = {
    ...task,
    id: 'c6c6c6c6-c6c6-c6c6-c6c6-c6c6c6c6c6c6',
    title: 'Queue Event Task',
  };
  const messages: Array<{ type: string; payload?: unknown }> = [];
  let activeSocket: WebSocketRoute | null = null;
  const sendServerEvent = (event: unknown) => {
    if (!activeSocket) {
      throw new Error('WebSocket was not opened');
    }
    activeSocket.send(JSON.stringify(event));
  };

  await page.addInitScript(({ playerID, sessionToken }) => {
    window.localStorage.setItem('player_id', playerID);
    window.localStorage.setItem('session_token', sessionToken);
    window.localStorage.setItem('username', 'alice');
  }, { playerID, sessionToken });

  await page.route('**/api/v1/players/me', async (route) => {
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
          id: duelID,
          status: 'active',
          deadline,
          started_at: nowISO(),
        },
      }),
    });
  });

  await page.routeWebSocket((url) => url.pathname === '/ws', (ws) => {
    activeSocket = ws;
    ws.onMessage((raw) => {
      messages.push(JSON.parse(String(raw)) as { type: string; payload?: unknown });
    });
  });

  await page.goto('/');
  await expect(page.getByText('Игрок готов')).toBeVisible();
  await page.getByRole('button', { name: /ИГРАТЬ/ }).click();
  await expect.poll(() => activeSocket !== null).toBeTruthy();

  sendServerEvent({ type: 'queue_joined' });
  sendServerEvent({
    type: 'match_found',
    payload: {
      duel_id: wrongDuelID,
      opponent_username: 'bob',
      duel: {
        id: wrongDuelID,
        player1_id: playerID,
        player2_id: opponentID,
        status: 'active',
        deadline,
        started_at: nowISO(),
      },
    },
  });
  sendServerEvent({
    type: 'task_assigned',
    payload: {
      duel_id: wrongDuelID,
      deadline,
      time_limit_seconds: 180,
      task: wrongTask,
    },
  });

  await page.waitForTimeout(150);
  expect(page.url()).toMatch(/\/$/);
  await expect(page.getByRole('button', { name: 'Отменить поиск' })).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Queue Event Task' })).toBeHidden();
  expect(await page.evaluate(() => window.localStorage.getItem('currentGame'))).toBeNull();

  sendServerEvent({
    type: 'duel_resume',
    payload: {
      duel_id: duelID,
      deadline,
      opponent_disconnected: false,
      task,
    },
  });

  await expect(page).toHaveURL(/\/task$/);
  await expect(page.getByRole('heading', { name: 'Restore Flow Task' })).toBeVisible();
  expect(messages.filter((message) => message.type === 'join_queue')).toHaveLength(0);
});

test('active duel restore ignores queue_left and still reports restore failure on close', async ({ page }) => {
  const playerID = 'c7c7c7c7-c7c7-c7c7-c7c7-c7c7c7c7c7c7';
  const sessionToken = '10000000-0000-0000-0000-000000000011';
  const duelID = 'c8c8c8c8-c8c8-c8c8-c8c8-c8c8c8c8c8c8';
  const deadline = inSecondsISO(180);
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

  await page.addInitScript(({ playerID, sessionToken }) => {
    window.localStorage.setItem('player_id', playerID);
    window.localStorage.setItem('session_token', sessionToken);
    window.localStorage.setItem('username', 'alice');
  }, { playerID, sessionToken });

  await page.route('**/api/v1/players/me', async (route) => {
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
          id: duelID,
          status: 'active',
          deadline,
          started_at: nowISO(),
        },
      }),
    });
  });

  await page.routeWebSocket((url) => url.pathname === '/ws', (ws) => {
    activeSocket = ws;
  });

  await page.goto('/');
  await expect(page.getByText('Игрок готов')).toBeVisible();
  await page.getByRole('button', { name: /ИГРАТЬ/ }).click();
  await expect.poll(() => activeSocket !== null).toBeTruthy();

  sendServerEvent({ type: 'queue_left' });
  await page.waitForTimeout(150);
  await expect(page.getByRole('button', { name: 'Отменить поиск' })).toBeVisible();
  await expect(page.getByText('Не удалось восстановить активную дуэль. Обновите страницу.')).toBeHidden();

  await closeActiveSocket();

  await expect(page.getByText('Не удалось восстановить активную дуэль. Обновите страницу.')).toBeVisible();
  await expect(page).toHaveURL(/\/$/);
  expect(await page.evaluate(() => window.localStorage.getItem('currentGame'))).toBeNull();
});

test('active duel restore failure still fires after wrong-duel resume then close', async ({ page }) => {
  const playerID = 'a6a6a6a6-a6a6-a6a6-a6a6-a6a6a6a6a6a6';
  const sessionToken = '10000000-0000-0000-0000-000000000012';
  const duelID = 'a7a7a7a7-a7a7-a7a7-a7a7-a7a7a7a7a7a7';
  const wrongDuelID = 'a8a8a8a8-a8a8-a8a8-a8a8-a8a8a8a8a8a8';
  const deadline = inSecondsISO(180);
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

  await page.addInitScript(({ playerID, sessionToken }) => {
    window.localStorage.setItem('player_id', playerID);
    window.localStorage.setItem('session_token', sessionToken);
    window.localStorage.setItem('username', 'alice');
  }, { playerID, sessionToken });

  await page.route('**/api/v1/players/me', async (route) => {
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
          id: duelID,
          status: 'active',
          deadline,
          started_at: nowISO(),
        },
      }),
    });
  });

  await page.routeWebSocket((url) => url.pathname === '/ws', (ws) => {
    activeSocket = ws;
  });

  await page.goto('/');
  await expect(page.getByText('Игрок готов')).toBeVisible();
  await page.getByRole('button', { name: /ИГРАТЬ/ }).click();
  await expect.poll(() => activeSocket !== null).toBeTruthy();

  sendServerEvent({
    type: 'duel_resume',
    payload: {
      duel_id: wrongDuelID,
      deadline: inSecondsISO(240),
      opponent_disconnected: false,
      task: {
        id: 'a9a9a9a9-a9a9-a9a9-a9a9-a9a9a9a9a9a9',
        title: 'Wrong Close Restore Task',
        description: 'Wrong-duel resume must not mark restore as successful.',
        category: 'web',
        difficulty: 'easy',
        time_limit: 180,
        time_limit_seconds: 180,
        task_url: 'https://example.com/task',
        hint_schedule: [],
      },
    },
  });
  await closeActiveSocket();

  await expect(page.getByText('Не удалось восстановить активную дуэль. Обновите страницу.')).toBeVisible();
  await expect(page).toHaveURL(/\/$/);
  await expect(page.getByRole('heading', { name: 'Wrong Close Restore Task' })).toBeHidden();
  expect(await page.evaluate(() => window.localStorage.getItem('currentGame'))).toBeNull();
});

test('home ignores wrong-duel terminal event and handles matching queue terminal event', async ({ page }) => {
  const playerID = 'b1b1b1b1-b1b1-b1b1-b1b1-b1b1b1b1b1b1';
  const sessionToken = '10000000-0000-0000-0000-000000000013';
  const duelID = 'b2b2b2b2-b2b2-b2b2-b2b2-b2b2b2b2b2b2';
  const wrongDuelID = 'b3b3b3b3-b3b3-b3b3-b3b3-b3b3b3b3b3b3';
  const opponentID = 'b4b4b4b4-b4b4-b4b4-b4b4-b4b4b4b4b4b4';
  const deadline = inSecondsISO(180);
  const messages: Array<{ type: string; payload?: unknown }> = [];
  let activeSocket: WebSocketRoute | null = null;
  const sendServerEvent = (event: unknown) => {
    if (!activeSocket) {
      throw new Error('WebSocket was not opened');
    }
    activeSocket.send(JSON.stringify(event));
  };
  const duelFinishedEvent = (id: string, winnerID: string | null) => ({
    type: 'duel_finished',
    payload: {
      duel_id: id,
      winner_id: winnerID,
      winner_username: winnerID === playerID ? 'alice' : null,
      your_solved: winnerID === playerID,
      opponent_solved: winnerID === opponentID,
      duel: {
        id,
        player1_id: playerID,
        player2_id: opponentID,
        status: 'finished',
        winner_id: winnerID,
        deadline,
        started_at: nowISO(),
        finished_at: nowISO(),
      },
    },
  });

  await page.route('**/api/v1/players/join', async (route) => {
    await route.fulfill({
      status: 200,
      headers: jsonHeaders,
      body: JSON.stringify({
        player_id: playerID,
        session_token: sessionToken,
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
      }),
    });
  });

  await page.routeWebSocket((url) => url.pathname === '/ws', (ws) => {
    expectWebSocketURLDoesNotLeakSession(ws.url());
    activeSocket = ws;
    ws.onMessage((raw) => {
      const message = JSON.parse(String(raw)) as { type: string; payload?: unknown };
      messages.push(message);
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
      ws.send(JSON.stringify(duelFinishedEvent(wrongDuelID, playerID)));
    });
  });

  await page.goto('/');
  await page.getByPlaceholder('Введите никнейм...').fill('alice');
  await page.getByRole('button', { name: /ПОДКЛЮЧИТЬСЯ/ }).click();
  await expect(page.getByText('Игрок готов')).toBeVisible();
  await page.getByRole('button', { name: /ИГРАТЬ/ }).click();
  await expect.poll(() => messages.some((message) => message.type === 'join_queue')).toBe(true);

  await expect(page.getByRole('button', { name: 'Отменить поиск' })).toBeVisible();
  await expect(page.getByText('Поздравляем! Вы выиграли!')).toBeHidden();
  expect(await page.evaluate(() => window.localStorage.getItem('currentGame'))).toBeNull();

  sendServerEvent(duelFinishedEvent(duelID, playerID));

  await expect(page.getByText('Поздравляем! Вы выиграли!')).toBeVisible();
  await expect(page.getByRole('button', { name: 'Отменить поиск' })).toBeHidden();
  await expect(page).toHaveURL(/\/$/);
  expect(await page.evaluate(() => window.localStorage.getItem('currentGame'))).toBeNull();
  expect(messages.filter((message) => message.type === 'join_queue')).toHaveLength(1);
});

test('home terminal event uses refreshed player id for winner notification', async ({ page }) => {
  const stalePlayerID = 'd1d1d1d1-d1d1-d1d1-d1d1-d1d1d1d1d1d1';
  const refreshedPlayerID = 'd2d2d2d2-d2d2-d2d2-d2d2-d2d2d2d2d2d2';
  const sessionToken = '10000000-0000-0000-0000-000000000070';
  const duelID = 'd3d3d3d3-d3d3-d3d3-d3d3-d3d3d3d3d3d3';
  const opponentID = 'd4d4d4d4-d4d4-d4d4-d4d4-d4d4d4d4d4d4';
  const deadline = inSecondsISO(180);
  const messages: Array<{ type: string; payload?: unknown }> = [];

  await page.addInitScript(({ stalePlayerID, sessionToken }) => {
    window.localStorage.setItem('player_id', stalePlayerID);
    window.localStorage.setItem('session_token', sessionToken);
    window.localStorage.setItem('username', 'alice-old');
  }, { stalePlayerID, sessionToken });

  await page.route('**/api/v1/players/me', async (route) => {
    await route.fulfill({
      status: 200,
      headers: jsonHeaders,
      body: JSON.stringify({
        player: {
          id: refreshedPlayerID,
          username: 'alice',
          status: 'idle',
          created_at: nowISO(),
        },
      }),
    });
  });

  await page.routeWebSocket((url) => url.pathname === '/ws', (ws) => {
    expectWebSocketURLDoesNotLeakSession(ws.url());
    ws.onMessage((raw) => {
      const message = JSON.parse(String(raw)) as { type: string; payload?: unknown };
      messages.push(message);
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
            player1_id: refreshedPlayerID,
            player2_id: opponentID,
            status: 'active',
            deadline,
            started_at: nowISO(),
          },
        },
      }));
      ws.send(JSON.stringify({
        type: 'duel_finished',
        payload: {
          duel_id: duelID,
          winner_id: refreshedPlayerID,
          winner_username: 'alice',
          your_solved: true,
          opponent_solved: false,
          duel: {
            id: duelID,
            player1_id: refreshedPlayerID,
            player2_id: opponentID,
            status: 'finished',
            winner_id: refreshedPlayerID,
            deadline,
            started_at: nowISO(),
            finished_at: nowISO(),
          },
        },
      }));
    });
  });

  await page.goto('/');
  await expect(page.getByText('Игрок готов')).toBeVisible();
  await page.getByRole('button', { name: /ИГРАТЬ/ }).click();
  await expect.poll(() => messages.some((message) => message.type === 'join_queue')).toBe(true);

  await expect(page.getByText('Поздравляем! Вы выиграли!')).toBeVisible();
  await expect(page.getByRole('button', { name: 'Отменить поиск' })).toBeHidden();
  await expect(page.getByPlaceholder('Введите никнейм...')).toBeVisible();
  await expect(page.getByText('Игрок готов')).toBeHidden();
  await expect.poll(() => page.evaluate(() => window.localStorage.getItem('player_id'))).toBeNull();
  await expect.poll(() => page.evaluate(() => window.localStorage.getItem('session_token'))).toBeNull();
  await expect.poll(() => page.evaluate(() => window.localStorage.getItem('currentGame'))).toBeNull();
});

test('home terminal event does not treat stale stored player id as winner', async ({ page }) => {
  const stalePlayerID = 'e1e1e1e1-e1e1-e1e1-e1e1-e1e1e1e1e1e1';
  const refreshedPlayerID = 'e2e2e2e2-e2e2-e2e2-e2e2-e2e2e2e2e2e2';
  const sessionToken = '10000000-0000-0000-0000-000000000071';
  const duelID = 'e3e3e3e3-e3e3-e3e3-e3e3-e3e3e3e3e3e3';
  const deadline = inSecondsISO(180);
  const messages: Array<{ type: string; payload?: unknown }> = [];

  await page.addInitScript(({ stalePlayerID, sessionToken }) => {
    window.localStorage.setItem('player_id', stalePlayerID);
    window.localStorage.setItem('session_token', sessionToken);
    window.localStorage.setItem('username', 'alice-old');
  }, { stalePlayerID, sessionToken });

  await page.route('**/api/v1/players/me', async (route) => {
    await route.fulfill({
      status: 200,
      headers: jsonHeaders,
      body: JSON.stringify({
        player: {
          id: refreshedPlayerID,
          username: 'alice',
          status: 'idle',
          created_at: nowISO(),
        },
      }),
    });
  });

  await page.routeWebSocket((url) => url.pathname === '/ws', (ws) => {
    expectWebSocketURLDoesNotLeakSession(ws.url());
    ws.onMessage((raw) => {
      const message = JSON.parse(String(raw)) as { type: string; payload?: unknown };
      messages.push(message);
      if (message.type !== 'join_queue') {
        return;
      }
      ws.send(JSON.stringify({
        type: 'match_found',
        payload: {
          duel_id: duelID,
          opponent_username: 'alice-old',
          duel: {
            id: duelID,
            player1_id: refreshedPlayerID,
            player2_id: stalePlayerID,
            status: 'active',
            deadline,
            started_at: nowISO(),
          },
        },
      }));
      ws.send(JSON.stringify({
        type: 'duel_finished',
        payload: {
          duel_id: duelID,
          winner_id: stalePlayerID,
          winner_username: 'alice-old',
          your_solved: false,
          opponent_solved: true,
          duel: {
            id: duelID,
            player1_id: refreshedPlayerID,
            player2_id: stalePlayerID,
            status: 'finished',
            winner_id: stalePlayerID,
            deadline,
            started_at: nowISO(),
            finished_at: nowISO(),
          },
        },
      }));
    });
  });

  await page.goto('/');
  await expect(page.getByText('Игрок готов')).toBeVisible();
  await page.getByRole('button', { name: /ИГРАТЬ/ }).click();
  await expect.poll(() => messages.some((message) => message.type === 'join_queue')).toBe(true);

  await expect(page.getByText('Вы проиграли. Попробуйте снова!')).toBeVisible();
  await expect(page.getByText('Поздравляем! Вы выиграли!')).toBeHidden();
  await expect(page.getByPlaceholder('Введите никнейм...')).toBeVisible();
  await expect(page.getByText('Игрок готов')).toBeHidden();
  await expect.poll(() => page.evaluate(() => window.localStorage.getItem('player_id'))).toBeNull();
  await expect.poll(() => page.evaluate(() => window.localStorage.getItem('session_token'))).toBeNull();
  await expect.poll(() => page.evaluate(() => window.localStorage.getItem('currentGame'))).toBeNull();
});

test('home queue terminal event ignores late websocket error', async ({ page }) => {
  await installWebSocketErrorProbe(page);

  const playerID = 'c1c1c1c1-c1c1-c1c1-c1c1-c1c1c1c1c1c1';
  const sessionToken = '10000000-0000-0000-0000-000000000014';
  const duelID = 'c2c2c2c2-c2c2-c2c2-c2c2-c2c2c2c2c2c2';
  const opponentID = 'c3c3c3c3-c3c3-c3c3-c3c3-c3c3c3c3c3c3';
  const deadline = inSecondsISO(180);
  const messages: Array<{ type: string; payload?: unknown }> = [];
  let activeSocket: WebSocketRoute | null = null;
  const sendServerEvent = (event: unknown) => {
    if (!activeSocket) {
      throw new Error('WebSocket was not opened');
    }
    activeSocket.send(JSON.stringify(event));
  };
  const duelFinishedEvent = {
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
  };

  await page.route('**/api/v1/players/join', async (route) => {
    await route.fulfill({
      status: 200,
      headers: jsonHeaders,
      body: JSON.stringify({
        player_id: playerID,
        session_token: sessionToken,
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
      }),
    });
  });

  await page.routeWebSocket((url) => url.pathname === '/ws', (ws) => {
    activeSocket = ws;
    ws.onMessage((raw) => {
      const message = JSON.parse(String(raw)) as { type: string; payload?: unknown };
      messages.push(message);
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
    });
  });

  await page.goto('/');
  await page.getByPlaceholder('Введите никнейм...').fill('alice');
  await page.getByRole('button', { name: /ПОДКЛЮЧИТЬСЯ/ }).click();
  await expect(page.getByText('Игрок готов')).toBeVisible();
  await page.getByRole('button', { name: /ИГРАТЬ/ }).click();
  await expect.poll(() => messages.some((message) => message.type === 'join_queue')).toBe(true);

  sendServerEvent(duelFinishedEvent);

  await expect(page.getByText('Поздравляем! Вы выиграли!')).toBeVisible();
  await expect(page.getByRole('button', { name: 'Отменить поиск' })).toBeHidden();

  const dispatched = await page.evaluate(() => {
    const testWindow = window as unknown as WebSocketErrorProbeWindow;
    return testWindow.__dispatchWebSocketErrorAt(0);
  });

  expect(dispatched).toBe(true);
  await page.waitForTimeout(150);
  await expect(page.getByText('Поздравляем! Вы выиграли!')).toBeVisible();
  await expect(page.getByText('Ошибка WebSocket соединения')).toBeHidden();
  expect(await page.evaluate(() => window.localStorage.getItem('currentGame'))).toBeNull();
});

test('home terminal restore event prevents false restore failure on websocket close', async ({ page }) => {
  const playerID = 'b5b5b5b5-b5b5-b5b5-b5b5-b5b5b5b5b5b5';
  const sessionToken = '10000000-0000-0000-0000-000000000015';
  const duelID = 'b6b6b6b6-b6b6-b6b6-b6b6-b6b6b6b6b6b6';
  const wrongDuelID = 'b7b7b7b7-b7b7-b7b7-b7b7-b7b7b7b7b7b7';
  const opponentID = 'b8b8b8b8-b8b8-b8b8-b8b8-b8b8b8b8b8b8';
  const deadline = inSecondsISO(180);
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
  const duelFinishedEvent = (id: string) => ({
    type: 'duel_finished',
    payload: {
      duel_id: id,
      winner_id: opponentID,
      winner_username: 'bob',
      your_solved: false,
      opponent_solved: true,
      duel: {
        id,
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

  await page.addInitScript(({ playerID, sessionToken }) => {
    window.localStorage.setItem('player_id', playerID);
    window.localStorage.setItem('session_token', sessionToken);
    window.localStorage.setItem('username', 'alice');
  }, { playerID, sessionToken });

  await page.route('**/api/v1/players/me', async (route) => {
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
          id: duelID,
          status: 'active',
          deadline,
          started_at: nowISO(),
        },
      }),
    });
  });

  await page.routeWebSocket((url) => url.pathname === '/ws', (ws) => {
    activeSocket = ws;
  });

  await page.goto('/');
  await expect(page.getByText('Игрок готов')).toBeVisible();
  await page.getByRole('button', { name: /ИГРАТЬ/ }).click();
  await expect.poll(() => activeSocket !== null).toBeTruthy();

  sendServerEvent(duelFinishedEvent(wrongDuelID));
  await page.waitForTimeout(150);
  await expect(page.getByRole('button', { name: 'Отменить поиск' })).toBeVisible();
  await expect(page.getByText('Вы проиграли. Попробуйте снова!')).toBeHidden();

  sendServerEvent(duelFinishedEvent(duelID));
  await closeActiveSocket();

  await expect(page.getByText('Вы проиграли. Попробуйте снова!')).toBeVisible();
  await expect(page.getByText('Не удалось восстановить активную дуэль. Обновите страницу.')).toBeHidden();
  await expect(page.getByRole('button', { name: 'Отменить поиск' })).toBeHidden();
  await expect(page).toHaveURL(/\/$/);
  expect(await page.evaluate(() => window.localStorage.getItem('currentGame'))).toBeNull();
});

test('home restore task transition ignores late websocket error', async ({ page }) => {
  await installWebSocketErrorProbe(page);

  const playerID = 'c4c4c4c4-c4c4-c4c4-c4c4-c4c4c4c4c4c4';
  const sessionToken = '10000000-0000-0000-0000-000000000016';
  const duelID = 'c5c5c5c5-c5c5-c5c5-c5c5-c5c5c5c5c5c5';
  const deadline = inSecondsISO(180);
  const task = {
    id: 'c6c6c6c6-c6c6-c6c6-c6c6-c6c6c6c6c6c6',
    title: 'Restore Late Error Task',
    description: 'Late socket errors must not override a successful restore.',
    category: 'web',
    difficulty: 'easy',
    time_limit: 180,
    time_limit_seconds: 180,
    task_url: 'https://example.com/task',
    source_url: 'https://files.example/source.zip',
    hint_schedule: [],
  };
  let activeSocket: WebSocketRoute | null = null;
  const sendServerEvent = (event: unknown) => {
    if (!activeSocket) {
      throw new Error('WebSocket was not opened');
    }
    activeSocket.send(JSON.stringify(event));
  };

  await page.addInitScript(({ playerID, sessionToken }) => {
    window.localStorage.setItem('player_id', playerID);
    window.localStorage.setItem('session_token', sessionToken);
    window.localStorage.setItem('username', 'alice');
  }, { playerID, sessionToken });

  await page.route('**/api/v1/players/me', async (route) => {
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
          id: duelID,
          status: 'active',
          deadline,
          started_at: nowISO(),
        },
      }),
    });
  });

  await page.routeWebSocket((url) => url.pathname === '/ws', (ws) => {
    activeSocket = ws;
  });

  await page.goto('/');
  await expect(page.getByText('Игрок готов')).toBeVisible();
  await page.getByRole('button', { name: /ИГРАТЬ/ }).click();
  await expect.poll(() => activeSocket !== null).toBeTruthy();

  sendServerEvent({
    type: 'duel_resume',
    payload: {
      duel_id: duelID,
      deadline,
      opponent_disconnected: false,
      task,
    },
  });

  await expect.poll(() => page.evaluate(() => window.localStorage.getItem('currentGame'))).toContain(duelID);
  const dispatched = await page.evaluate(() => {
    const testWindow = window as unknown as WebSocketErrorProbeWindow;
    return testWindow.__dispatchWebSocketErrorAt(0);
  });

  expect(dispatched).toBe(true);
  await expect(page).toHaveURL(/\/task$/);
  await expect(page.getByRole('heading', { name: 'Restore Late Error Task' })).toBeVisible();
  await expect(page.getByText('Не удалось восстановить активную дуэль. Обновите страницу.')).toBeHidden();
  await expect(page.getByText('Ошибка WebSocket соединения')).toBeHidden();
});

test('stale player session is cleared before opening websocket', async ({ page }) => {
  const playerID = 'abababab-abab-abab-abab-abababababab';
  const sessionToken = '10000000-0000-0000-0000-000000000017';
  let websocketOpened = false;

  await page.addInitScript(({ playerID, sessionToken }) => {
    window.localStorage.setItem('player_id', playerID);
    window.localStorage.setItem('session_token', sessionToken);
    window.localStorage.setItem('username', 'alice');
  }, { playerID, sessionToken });

  await page.route('**/api/v1/players/me', async (route) => {
    await route.fulfill({
      status: 401,
      headers: jsonHeaders,
      body: JSON.stringify({
        type: 'about:blank',
        title: 'unauthorized',
        status: 401,
        detail: 'invalid session token',
      }),
    });
  });

  await page.routeWebSocket((url) => url.pathname === '/ws', () => {
    websocketOpened = true;
  });

  await page.goto('/');

  await expect(page.getByText('Сессия истекла. Введите никнейм заново.')).toBeVisible();
  await expect(page.getByPlaceholder('Введите никнейм...')).toBeVisible();
  await expect.poll(() => websocketOpened).toBe(false);
  await expect.poll(() => page.evaluate(() => window.localStorage.getItem('session_token'))).toBeNull();
});

test('malformed player me response surfaces contract error without clearing session', async ({ page }) => {
  const playerID = 'acacacac-acac-acac-acac-acacacacacac';
  const sessionToken = '10000000-0000-0000-0000-000000000018';
  let websocketOpened = false;

  await page.addInitScript(({ playerID, sessionToken }) => {
    window.localStorage.setItem('player_id', playerID);
    window.localStorage.setItem('session_token', sessionToken);
    window.localStorage.setItem('username', 'alice');
  }, { playerID, sessionToken });

  await page.route('**/api/v1/players/me', async (route) => {
    await route.fulfill({
      status: 200,
      headers: jsonHeaders,
      body: JSON.stringify({
        player: {
          id: playerID,
          username: 'alice',
        },
      }),
    });
  });

  await page.routeWebSocket((url) => url.pathname === '/ws', () => {
    websocketOpened = true;
  });

  await page.goto('/');

  await expect(page.getByText('Не удалось проверить сессию. Попробуйте ещё раз.')).toBeVisible();
  await expect.poll(() => websocketOpened).toBe(false);
  await expect.poll(() => page.evaluate(() => window.localStorage.getItem('player_id'))).toBe(playerID);
  await expect.poll(() => page.evaluate(() => window.localStorage.getItem('session_token'))).toBe(sessionToken);
  await expect.poll(() => page.evaluate(() => window.localStorage.getItem('username'))).toBe('alice');
});

test('player me response with non-uuid player id surfaces contract error without clearing session', async ({ page }) => {
  const playerID = 'adadadad-adad-adad-adad-adadadadadad';
  const sessionToken = 'adadadad-0000-0000-0000-000000000001';
  let websocketOpened = false;

  await page.addInitScript(({ playerID, sessionToken }) => {
    window.localStorage.setItem('player_id', playerID);
    window.localStorage.setItem('session_token', sessionToken);
    window.localStorage.setItem('username', 'alice');
  }, { playerID, sessionToken });

  await page.route('**/api/v1/players/me', async (route) => {
    await route.fulfill({
      status: 200,
      headers: jsonHeaders,
      body: JSON.stringify({
        player: {
          id: 'not-a-uuid',
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

  await page.goto('/');

  await expect(page.getByText('Не удалось проверить сессию. Попробуйте ещё раз.')).toBeVisible();
  await expect.poll(() => websocketOpened).toBe(false);
  await expect.poll(() => page.evaluate(() => window.localStorage.getItem('player_id'))).toBe(playerID);
  await expect.poll(() => page.evaluate(() => window.localStorage.getItem('session_token'))).toBe(sessionToken);
  await expect.poll(() => page.evaluate(() => window.localStorage.getItem('username'))).toBe('alice');
});

test('malformed active_duel in player me surfaces contract error without clearing session', async ({ page }) => {
  const playerID = 'bcbcbcbc-bcbc-bcbc-bcbc-bcbcbcbcbcbc';
  const sessionToken = '10000000-0000-0000-0000-000000000019';
  let websocketOpened = false;

  await page.addInitScript(({ playerID, sessionToken }) => {
    window.localStorage.setItem('player_id', playerID);
    window.localStorage.setItem('session_token', sessionToken);
    window.localStorage.setItem('username', 'alice');
  }, { playerID, sessionToken });

  await page.route('**/api/v1/players/me', async (route) => {
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
          id: 'cdcdcdcd-cdcd-cdcd-cdcd-cdcdcdcdcdcd',
          status: 'active',
          deadline: 'not-a-date',
          started_at: nowISO(),
        },
      }),
    });
  });

  await page.routeWebSocket((url) => url.pathname === '/ws', () => {
    websocketOpened = true;
  });

  await page.goto('/');

  await expect(page.getByText('Не удалось проверить сессию. Попробуйте ещё раз.')).toBeVisible();
  await expect.poll(() => websocketOpened).toBe(false);
  await expect.poll(() => page.evaluate(() => window.localStorage.getItem('player_id'))).toBe(playerID);
  await expect.poll(() => page.evaluate(() => window.localStorage.getItem('session_token'))).toBe(sessionToken);
  await expect.poll(() => page.evaluate(() => window.localStorage.getItem('username'))).toBe('alice');
});

test('player me response with non-uuid active duel surfaces contract error without clearing session', async ({ page }) => {
  const playerID = 'bebebebe-bebe-bebe-bebe-bebebebebebe';
  const sessionToken = 'bebebebe-0000-0000-0000-000000000001';
  let websocketOpened = false;

  await page.addInitScript(({ playerID, sessionToken }) => {
    window.localStorage.setItem('player_id', playerID);
    window.localStorage.setItem('session_token', sessionToken);
    window.localStorage.setItem('username', 'alice');
  }, { playerID, sessionToken });

  await page.route('**/api/v1/players/me', async (route) => {
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
          id: 'not-a-uuid',
          status: 'active',
          deadline: inSecondsISO(120),
          started_at: nowISO(),
        },
      }),
    });
  });

  await page.routeWebSocket((url) => url.pathname === '/ws', () => {
    websocketOpened = true;
  });

  await page.goto('/');

  await expect(page.getByText('Не удалось проверить сессию. Попробуйте ещё раз.')).toBeVisible();
  await expect.poll(() => websocketOpened).toBe(false);
  await expect.poll(() => page.evaluate(() => window.localStorage.getItem('player_id'))).toBe(playerID);
  await expect.poll(() => page.evaluate(() => window.localStorage.getItem('session_token'))).toBe(sessionToken);
  await expect.poll(() => page.evaluate(() => window.localStorage.getItem('username'))).toBe('alice');
});
