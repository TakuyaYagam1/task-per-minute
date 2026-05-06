import { expect, test, type WebSocketRoute } from '@playwright/test';
import { nowISO } from './support/common';
import type { WindowOpenCall } from './support/browser';

test('task external links open with noopener and noreferrer', async ({ page }) => {
  const playerID = '17171717-1717-1717-1717-171717171717';
  const sessionToken = '10000000-0000-0000-0000-000000000034';
  const duelID = '18181818-1818-1818-1818-181818181818';
  const task = {
    id: '19191919-1919-1919-1919-191919191919',
    title: 'External Links',
    description: 'Open task and source links safely.',
    category: 'web',
    difficulty: 'easy',
    time_limit: 120,
    time_limit_seconds: 120,
    task_url: 'https://example.com/task-open',
    source_url: 'https://files.example/source.zip',
    hint_schedule: [],
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

  await page.addInitScript(({ playerID, sessionToken, duelID, task }) => {
    window.localStorage.setItem('player_id', playerID);
    window.localStorage.setItem('session_token', sessionToken);
    window.localStorage.setItem('username', 'alice');
    window.localStorage.setItem('currentGame', JSON.stringify({
      duel_id: duelID,
      deadline: new Date(Date.now() + 120_000).toISOString(),
      time_limit_seconds: 120,
      task,
    }));
  }, { playerID, sessionToken, duelID, task });

  await page.routeWebSocket((url) => url.pathname === '/ws', () => {});

  await page.goto('/task');
  await expect(page.getByRole('heading', { name: 'External Links' })).toBeVisible();

  await page.getByRole('button', { name: /Перейти к заданию/ }).click();
  await page.getByRole('button', { name: /Скачать/ }).click();

  const opened = await page.evaluate(() => {
    const testWindow = window as unknown as Window & { __openedUrls?: WindowOpenCall[] };
    return testWindow.__openedUrls || [];
  });

  expect(opened).toEqual([
    {
      url: 'https://example.com/task-open',
      target: '_blank',
      features: 'noopener,noreferrer',
    },
    {
      url: 'https://files.example/source.zip',
      target: '_blank',
      features: 'noopener,noreferrer',
    },
  ]);
});

test('task host-port endpoint is copied instead of opened', async ({ page }) => {
  const playerID = '23232323-2323-2323-2323-232323232323';
  const sessionToken = '10000000-0000-0000-0000-000000000035';
  const duelID = '24242424-2424-2424-2424-242424242424';
  const task = {
    id: '25252525-2525-2525-2525-252525252525',
    title: 'Host Port Target',
    description: 'Pwn targets should be copied instead of opened.',
    category: 'pwn',
    difficulty: 'easy',
    time_limit: 120,
    time_limit_seconds: 120,
    task_url: 'pwn.example:31337',
    hint_schedule: [],
  };
  let activeSocket: WebSocketRoute | null = null;
  const closeActiveSocket = async () => {
    if (!activeSocket) {
      throw new Error('WebSocket was not opened');
    }
    await activeSocket.close();
  };

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

  await page.addInitScript(({ playerID, sessionToken, duelID, task }) => {
    window.localStorage.setItem('player_id', playerID);
    window.localStorage.setItem('session_token', sessionToken);
    window.localStorage.setItem('username', 'alice');
    window.localStorage.setItem('currentGame', JSON.stringify({
      duel_id: duelID,
      deadline: new Date(Date.now() + 120_000).toISOString(),
      time_limit_seconds: 120,
      task,
    }));
  }, { playerID, sessionToken, duelID, task });

  await page.routeWebSocket((url) => url.pathname === '/ws', (ws) => {
    activeSocket = ws;
  });

  await page.goto('/task');
  await expect(page.getByRole('heading', { name: 'Host Port Target' })).toBeVisible();
  await expect.poll(() => activeSocket !== null).toBeTruthy();
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

  expect(browserState).toEqual({
    opened: [],
    copied: ['pwn.example:31337'],
  });

  await closeActiveSocket();
  await expect(page.getByRole('heading', { name: 'Host Port Target' })).toBeVisible();
  await page.waitForTimeout(3200);
  await expect(page.getByRole('heading', { name: 'Host Port Target' })).toBeVisible();
});

test('unsafe mixed-content task url without clipboard is not marked as opened', async ({ page }) => {
  const playerID = '56565656-5656-5656-5656-565656565656';
  const sessionToken = '10000000-0000-0000-0000-000000000156';
  const duelID = '57575757-5757-5757-5757-575757575757';
  const task = {
    id: '58585858-5858-5858-5858-585858585858',
    title: 'Unsafe Mixed Content',
    description: 'Unsafe HTTP targets should not look opened when no fallback works.',
    category: 'web',
    difficulty: 'easy',
    time_limit: 120,
    time_limit_seconds: 120,
    task_url: 'http://example.com/insecure-task',
    hint_schedule: [],
  };

  await page.addInitScript(() => {
    const testWindow = window as unknown as Window & {
      __openedUrls: WindowOpenCall[];
      __taskPerMinuteLocationProtocol: string;
    };
    testWindow.__taskPerMinuteLocationProtocol = 'https:';
    testWindow.__openedUrls = [];
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
      value: undefined,
    });
  });

  await page.addInitScript(({ playerID, sessionToken, duelID, task }) => {
    window.localStorage.setItem('player_id', playerID);
    window.localStorage.setItem('session_token', sessionToken);
    window.localStorage.setItem('username', 'alice');
    window.localStorage.setItem('currentGame', JSON.stringify({
      duel_id: duelID,
      deadline: new Date(Date.now() + 120_000).toISOString(),
      time_limit_seconds: 120,
      task,
    }));
  }, { playerID, sessionToken, duelID, task });

  await page.routeWebSocket((url) => url.pathname === '/ws', () => {});

  await page.goto('/task');
  await expect(page.getByRole('heading', { name: 'Unsafe Mixed Content' })).toBeVisible();

  await page.getByRole('button', { name: /Перейти к заданию/ }).click();

  await expect(page.getByText('URL задания небезопасен (mixed content). Откройте вручную')).toBeVisible();
  await expect(page.getByRole('button', { name: /Перейти к заданию/ })).toBeVisible();
  await expect(page.getByRole('button', { name: /Задание открыто/ })).toBeHidden();

  const opened = await page.evaluate(() => {
    const testWindow = window as unknown as Window & { __openedUrls?: WindowOpenCall[] };
    return testWindow.__openedUrls || [];
  });

  expect(opened).toEqual([]);
});

test('unsafe mixed-content task url copied to clipboard is not marked as opened', async ({ page }) => {
  const playerID = '56565656-5656-5656-5656-565656565656';
  const sessionToken = '10000000-0000-0000-0000-000000000157';
  const duelID = '57575757-5757-5757-5757-575757575758';
  const task = {
    id: '58585858-5858-5858-5858-585858585859',
    title: 'Copied Mixed Content',
    description: 'Unsafe HTTP targets copied to clipboard are not browser-opened.',
    category: 'web',
    difficulty: 'easy',
    time_limit: 120,
    time_limit_seconds: 120,
    task_url: 'http://example.com/copied-insecure-task',
    hint_schedule: [],
  };

  await page.addInitScript(() => {
    const testWindow = window as unknown as Window & {
      __openedUrls: WindowOpenCall[];
      __copiedTargets: string[];
      __taskPerMinuteLocationProtocol: string;
    };
    testWindow.__taskPerMinuteLocationProtocol = 'https:';
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

  await page.addInitScript(({ playerID, sessionToken, duelID, task }) => {
    window.localStorage.setItem('player_id', playerID);
    window.localStorage.setItem('session_token', sessionToken);
    window.localStorage.setItem('username', 'alice');
    window.localStorage.setItem('currentGame', JSON.stringify({
      duel_id: duelID,
      deadline: new Date(Date.now() + 120_000).toISOString(),
      time_limit_seconds: 120,
      task,
    }));
  }, { playerID, sessionToken, duelID, task });

  await page.route('**/api/v1/players/me', async (route) => {
    expect(route.request().headers()['x-session-token']).toBe(sessionToken);
    await route.fulfill({
      status: 200,
      headers: { 'Content-Type': 'application/json' },
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
  });
  await page.routeWebSocket((url) => url.pathname === '/ws', () => {});

  await page.goto('/task');
  await expect(page.getByRole('heading', { name: 'Copied Mixed Content' })).toBeVisible();

  await page.getByRole('button', { name: /Перейти к заданию/ }).click();

  await expect(page.getByText('URL задания небезопасен (mixed content). Ссылка скопирована')).toBeVisible();
  await expect(page.getByRole('button', { name: /Перейти к заданию/ })).toBeVisible();
  await expect(page.getByRole('button', { name: /Задание открыто/ })).toBeHidden();

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

  expect(browserState).toEqual({
    opened: [],
    copied: ['http://example.com/copied-insecure-task'],
  });
});

test('task page ignores malformed and unknown websocket events', async ({ page }) => {
  const playerID = '20202020-2020-2020-2020-202020202020';
  const sessionToken = '10000000-0000-0000-0000-000000000036';
  const duelID = '21212121-2121-2121-2121-212121212121';
  const task = {
    id: '22222222-2222-2222-2222-222222222222',
    title: 'Malformed WS Guard',
    description: 'Invalid events should not break the task page.',
    category: 'web',
    difficulty: 'easy',
    time_limit: 120,
    time_limit_seconds: 120,
    task_url: 'https://example.com/task',
    hint_schedule: [],
  };
  const pageErrors: string[] = [];
  let activeSocket: WebSocketRoute | null = null;
  const sendRawServerMessage = (raw: string) => {
    if (!activeSocket) {
      throw new Error('WebSocket was not opened');
    }
    activeSocket.send(raw);
  };

  page.on('pageerror', (error) => {
    pageErrors.push(error.message);
  });

  await page.addInitScript(({ playerID, sessionToken, duelID, task }) => {
    window.localStorage.setItem('player_id', playerID);
    window.localStorage.setItem('session_token', sessionToken);
    window.localStorage.setItem('username', 'alice');
    window.localStorage.setItem('currentGame', JSON.stringify({
      duel_id: duelID,
      deadline: new Date(Date.now() + 120_000).toISOString(),
      time_limit_seconds: 120,
      task,
    }));
  }, { playerID, sessionToken, duelID, task });

  await page.route('**/submit-flag**', async (route) => {
    await route.abort();
  });

  await page.routeWebSocket((url) => url.pathname === '/ws', (ws) => {
    activeSocket = ws;
  });

  await page.goto('/task');
  await expect(page.getByRole('heading', { name: 'Malformed WS Guard' })).toBeVisible();
  await expect.poll(() => activeSocket !== null).toBeTruthy();

  sendRawServerMessage('not-json');
  sendRawServerMessage(JSON.stringify({ type: 'game_start', data: { duel_id: duelID } }));
  sendRawServerMessage(JSON.stringify({ payload: { duel_id: duelID } }));
  sendRawServerMessage(JSON.stringify({
    type: 'error',
    code: 123,
    message: { detail: 'bad error shape' },
  }));
  sendRawServerMessage(JSON.stringify({
    type: 'error',
    payload: {
      code: 'duel.paused',
      message: 'payload-shaped error is not the backend contract',
    },
  }));
  sendRawServerMessage(JSON.stringify({
    type: 'duel_resume',
    payload: {
      duel_id: 'not-a-uuid',
      deadline: new Date(Date.now() + 240_000).toISOString(),
      opponent_disconnected: false,
      task: {
        ...task,
        title: 'Bad UUID Resume',
      },
    },
  }));
  sendRawServerMessage(JSON.stringify({
    type: 'hint_unlocked',
    payload: {
      duel_id: duelID,
      task_id: task.id,
      hint_index: 0,
      hint: 'Zero index hint must be ignored',
      unlocked_at: nowISO(),
    },
  }));
  sendRawServerMessage(JSON.stringify({
    type: 'hint_unlocked',
    payload: {
      duel_id: duelID,
      task_id: task.id,
      hint_index: 4,
      hint: 'Out of range hint must be ignored',
      unlocked_at: nowISO(),
    },
  }));
  sendRawServerMessage(JSON.stringify({
    type: 'duel_finished',
    payload: {
      duel_id: duelID,
      winner_id: 'not-a-uuid',
      winner_username: 'alice',
      your_solved: true,
      opponent_solved: false,
      duel: {
        id: duelID,
        player1_id: playerID,
        player2_id: '30303030-3030-3030-3030-303030303030',
        status: 'finished',
        winner_id: 'not-a-uuid',
        deadline: new Date(Date.now() + 120_000).toISOString(),
        started_at: nowISO(),
        finished_at: nowISO(),
      },
    },
  }));

  await expect(page.getByRole('heading', { name: 'Malformed WS Guard' })).toBeVisible();
  await expect(page).toHaveURL(/\/task$/);
  await expect(page.getByText('123')).toBeHidden();
  await expect(page.getByText('[object Object]')).toBeHidden();
  await expect(page.getByRole('heading', { name: 'Bad UUID Resume' })).toBeHidden();
  await expect(page.getByText('Zero index hint must be ignored')).toBeHidden();
  await expect(page.getByText('Out of range hint must be ignored')).toBeHidden();
  await expect(page.getByText('Дуэль на паузе: дождитесь возвращения соперника.')).toBeHidden();
  await expect(page.getByRole('heading', { name: 'СОПЕРНИК ОТКЛЮЧИЛСЯ' })).toBeHidden();
  await expect(page.getByText('ПОБЕДА!')).toBeHidden();
  expect(pageErrors).toEqual([]);
});

test('task page clears submitting state when websocket closes after flag submit', async ({ page }) => {
  const playerID = '14141414-1414-1414-1414-141414141414';
  const sessionToken = '10000000-0000-0000-0000-000000000037';
  const duelID = '15151515-1515-1515-1515-151515151515';
  const task = {
    id: '16161616-1616-1616-1616-161616161616',
    title: 'Close During Submit',
    description: 'Socket closes before flag_result.',
    category: 'web',
    difficulty: 'easy',
    time_limit: 120,
    time_limit_seconds: 120,
    task_url: 'https://example.com/task',
    hint_schedule: [],
  };

  await page.addInitScript(({ playerID, sessionToken, duelID, task }) => {
    window.localStorage.setItem('player_id', playerID);
    window.localStorage.setItem('session_token', sessionToken);
    window.localStorage.setItem('username', 'alice');
    window.localStorage.setItem('currentGame', JSON.stringify({
      duel_id: duelID,
      deadline: new Date(Date.now() + 120_000).toISOString(),
      time_limit_seconds: 120,
      task,
    }));
  }, { playerID, sessionToken, duelID, task });

  await page.routeWebSocket((url) => url.pathname === '/ws', (ws) => {
    ws.onMessage(async (raw) => {
      const message = JSON.parse(String(raw)) as { type: string };
      if (message.type === 'flag_submit') {
        await ws.close();
      }
    });
  });

  await page.goto('/task');
  await expect(page.getByRole('heading', { name: 'Close During Submit' })).toBeVisible();

  await page.getByPlaceholder('ctf{...}').fill('ctf{maybe}');
  await page.getByRole('button', { name: /Отправить/ }).click();

  await expect(page.getByRole('button', { name: /Отправить/ })).toBeEnabled();
});
