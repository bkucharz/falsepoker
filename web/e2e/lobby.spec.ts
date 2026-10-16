import { expect, test, type Page } from '@playwright/test';

async function enter(page: Page, name: string) {
  await page.getByLabel('Your display name').fill(name);
  await page
    .getByRole('button', { name: /Create private room|^Join room/ })
    .click();
  await expect(
    page.getByRole('heading', { name: /The gang’s gathering/ }),
  ).toBeVisible();
}
test('real browser cookies, HTTP proxy, and native WebSocket snapshots', async ({
  browser,
}) => {
  const alice = await browser.newContext({ baseURL: 'http://localhost:5173' }),
    bob = await browser.newContext({ baseURL: 'http://localhost:5173' });
  try {
    const a = await alice.newPage(),
      b = await bob.newPage();
    await Promise.all([a.goto('/'), b.goto('/')]);
    await Promise.all([
      expect(a.getByLabel('Your display name')).toBeVisible(),
      expect(b.getByLabel('Your display name')).toBeVisible(),
    ]);
    const membership = await a.evaluate(async () => {
      const bytes = crypto.getRandomValues(new Uint8Array(16));
      const key = Array.from(bytes, (x) =>
        x.toString(16).padStart(2, '0'),
      ).join('');
      const response = await fetch('/api/v1/rooms', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', 'Idempotency-Key': key },
        body: JSON.stringify({ display_name: 'Alice' }),
      });
      if (!response.ok) throw new Error('Create failed');
      return response.json();
    });
    await b.evaluate(
      async ({ code }) => {
        const key = Array.from(
          crypto.getRandomValues(new Uint8Array(16)),
          (x) => x.toString(16).padStart(2, '0'),
        ).join('');
        const response = await fetch(`/api/v1/rooms/${code}/join`, {
          method: 'POST',
          headers: {
            'Content-Type': 'application/json',
            'Idempotency-Key': key,
          },
          body: JSON.stringify({ display_name: 'Bob' }),
        });
        if (!response.ok) throw new Error('Join failed');
      },
      { code: membership.room_code },
    );
    const snapshot = (page: Page) =>
      page.evaluate(
        ({ code, roomID }) =>
          new Promise<{
            type: string;
            state: { recipient_id: string; members: unknown[] };
          }>((resolve, reject) => {
            const socket = new WebSocket(
              `ws://${location.host}/api/v1/rooms/${code}/ws?room_id=${roomID}`,
              'falsepoker.v1',
            );
            const timeout = setTimeout(() => {
              socket.close();
              reject(new Error('Snapshot timeout'));
            }, 5000);
            socket.onmessage = (event) => {
              clearTimeout(timeout);
              resolve(JSON.parse(event.data));
              socket.close();
            };
            socket.onerror = () => {
              clearTimeout(timeout);
              reject(new Error('Upgrade failed'));
            };
          }),
        { code: membership.room_code, roomID: membership.room_id },
      );
    const [av, bv] = await Promise.all([snapshot(a), snapshot(b)]);
    expect(av.type).toBe('snapshot');
    expect(bv.type).toBe('snapshot');
    expect(av.state.members).toHaveLength(2);
    expect(bv.state.members).toHaveLength(2);
    expect(av.state.recipient_id).not.toBe(bv.state.recipient_id);
    const cookie = (await alice.cookies()).find(
      (cookie) => cookie.name === 'falsepoker_session',
    )!;
    expect(cookie.httpOnly).toBe(true);
    expect(cookie.sameSite).toBe('Strict');
    expect(cookie.secure).toBe(false);
    expect(await a.evaluate(() => document.cookie)).not.toContain(
      'falsepoker_session',
    );
  } finally {
    await alice.close();
    await bob.close();
  }
});

test('simultaneous first-time tabs share one session and restore membership without rejoining', async ({
  browser,
}) => {
  const context = await browser.newContext({
    baseURL: 'http://localhost:5173',
  });
  try {
    await context.addInitScript(() => {
      const Native = window.WebSocket;
      const sockets: WebSocket[] = [];
      (window as unknown as { testSockets: WebSocket[] }).testSockets = sockets;
      window.WebSocket = class extends Native {
        constructor(url: string | URL, protocols?: string | string[]) {
          super(url, protocols);
          sockets.push(this);
        }
      };
    });
    const identities: { player_id: string; status: number }[] = [];
    const responses: Promise<void>[] = [];
    context.on('response', (response) => {
      if (response.url().endsWith('/api/v1/session'))
        responses.push(
          response.json().then((body) => {
            identities.push({
              player_id: body.player_id,
              status: response.status(),
            });
          }),
        );
    });
    const [a, b] = await Promise.all([context.newPage(), context.newPage()]);
    await Promise.all([a.goto('/'), b.goto('/')]);
    await Promise.all([
      expect(a.getByLabel('Your display name')).toBeVisible(),
      expect(b.getByLabel('Your display name')).toBeVisible(),
    ]);
    await Promise.all(responses);
    expect(new Set(identities.map((value) => value.player_id)).size).toBe(1);
    expect(identities.filter((value) => value.status === 201)).toHaveLength(1);
    await enter(a, 'Alice');
    let joins = 0;
    context.on('request', (request) => {
      if (request.url().endsWith('/join')) joins++;
    });
    await b.goto(a.url());
    await expect(b.getByText('Alice', { exact: false }).first()).toBeVisible();
    await a.getByRole('button', { name: 'I’m ready' }).click();
    await expect(b.getByRole('button', { name: 'Not ready' })).toBeVisible();
    await a.reload();
    await expect(a.getByRole('button', { name: 'Not ready' })).toBeVisible();
    expect(joins).toBe(0);
    await a.evaluate(() => {
      const sockets = (window as unknown as { testSockets: WebSocket[] })
        .testSockets;
      const socket = sockets.find(
        (socket) => socket.readyState === WebSocket.OPEN,
      );
      if (!socket) throw new Error('No live socket to interrupt');
      socket.close(1000);
    });
    await expect
      .poll(() =>
        a.evaluate(() => {
          const sockets = (window as unknown as { testSockets: WebSocket[] })
            .testSockets;
          return sockets.filter(
            (socket) => socket.readyState === WebSocket.OPEN,
          ).length;
        }),
      )
      .toBe(1);
    await expect(
      a.getByText('Your connection: Connected', { exact: true }),
    ).toBeVisible();
    await expect(a.getByRole('button', { name: 'Not ready' })).toBeEnabled();
    expect(joins).toBe(0);
    await b.close();
    await expect(a.getByRole('button', { name: 'Not ready' })).toBeEnabled();
  } finally {
    await context.close();
  }
});

test('phone invitation flow, spectator admin, explicit seats, start, and leave confirmation', async ({
  browser,
}) => {
  const contexts = await Promise.all([
    browser.newContext({
      baseURL: 'http://localhost:5173',
      viewport: { width: 390, height: 844 },
      isMobile: true,
      hasTouch: true,
    }),
    browser.newContext({ baseURL: 'http://localhost:5173' }),
    browser.newContext({ baseURL: 'http://localhost:5173' }),
  ]);
  try {
    const [admin, bob, carol] = await Promise.all(
      contexts.map((context) => context.newPage()),
    );
    await admin.goto('/');
    await enter(admin, 'Alice');
    await Promise.all([bob.goto(admin.url()), carol.goto(admin.url())]);
    await enter(bob, 'Bob');
    await enter(carol, 'Carol');
    await bob.getByRole('button', { name: 'I’m ready' }).click();
    await expect(admin.getByText('1 ready', { exact: true })).toBeVisible();
    await carol.getByRole('button', { name: 'I’m ready' }).click();
    await expect(admin.getByText('2 ready', { exact: true })).toBeVisible();
    await expect(
      admin.getByRole('button', { name: 'I’m ready' }),
    ).toBeVisible();
    await admin
      .getByRole('combobox', { name: 'Seat order', exact: true })
      .selectOption('manual');
    await admin.getByRole('button', { name: 'Move Carol up' }).click();
    await admin.getByLabel('First bidder').selectOption({ label: 'Bob' });
    await admin.getByRole('button', { name: /Start game/ }).click();
    await Promise.all(
      [admin, bob, carol].map((page) =>
        expect(
          page.getByRole('heading', { name: 'The game is on.' }),
        ).toBeVisible(),
      ),
    );
    await expect(admin.getByText(/watching as a spectator/)).toBeVisible();
    await expect(bob.getByText(/You have a seat/)).toBeVisible();
    expect(
      await admin.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth,
      ),
    ).toBe(true);
    await admin.screenshot({
      path: 'test-results/mobile-started.png',
      fullPage: true,
    });
    await bob.reload();
    await expect(bob.getByText(/You have a seat/)).toBeVisible();
    await bob.getByRole('button', { name: 'Leave room' }).click();
    await expect(bob.getByRole('dialog')).toBeVisible();
    await bob.getByRole('button', { name: 'Stay in room' }).click();
    await expect(bob.getByRole('dialog')).not.toBeVisible();
    await bob.getByRole('button', { name: 'Leave room' }).click();
    await bob.getByRole('button', { name: 'Leave game', exact: true }).click();
    await expect(
      bob.getByRole('heading', { name: 'Take a seat.' }),
    ).toBeVisible();
    const state = await admin.evaluate(
      async () =>
        (
          await (
            await fetch(`/api/v1/rooms/${location.pathname.split('/').at(-1)}`)
          ).json()
        ).state,
    );
    expect(
      state.game.players.map((p: { display_name: string }) => p.display_name),
    ).toEqual(['Carol', 'Bob']);
    const bobID = state.game.players.find(
      (p: { display_name: string }) => p.display_name === 'Bob',
    ).player_id;
    expect(state.game.starter_id).toBe(bobID);
  } finally {
    await Promise.all(contexts.map((context) => context.close()));
  }
});

test('320px and desktop layouts keep long rosters and admin controls usable', async ({
  page,
}) => {
  const state = {
    room_id: 'r_visual',
    room_code: 'abcdef123456',
    revision: '1',
    game_id: '0',
    recipient_id: 'p0',
    admin_id: 'p0',
    role: 'spectator',
    game: null,
    members: Array.from({ length: 24 }, (_, i) => ({
      player_id: `p${i}`,
      display_name:
        i === 0 ? 'A-name-with-thirty-two-characters' : `Friend ${i + 1}`,
      ready: true,
    })),
  };
  await page.route('**/api/v1/session', (route) =>
    route.fulfill({ json: { protocol_version: 1, player_id: 'p0' } }),
  );
  await page.route('**/api/v1/rooms/abcdef123456', (route) =>
    route.fulfill({ json: { state } }),
  );
  await page.routeWebSocket('**/api/v1/rooms/abcdef123456/ws?*', (socket) =>
    socket.send(JSON.stringify({ type: 'snapshot', state })),
  );
  await page.goto('/');
  await expect(page.getByLabel('Your display name')).toBeVisible();
  for (const width of [320, 1280]) {
    await page.setViewportSize({ width, height: 844 });
    await page.getByLabel('Your display name').focus();
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth,
      ),
    ).toBe(true);
    await page.screenshot({
      path: `test-results/home-${width}.png`,
      fullPage: true,
    });
  }
  await page.goto('/room/abcdef123456');
  await expect(page.getByRole('button', { name: /Start game/ })).toBeEnabled();
  for (const width of [320, 390, 1280]) {
    await page.setViewportSize({ width, height: 844 });
    await page
      .getByRole('combobox', { name: 'Seat order', exact: true })
      .selectOption('manual');
    await expect(
      page.getByRole('button', { name: 'Move Friend 24 up' }),
    ).toBeVisible();
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= innerWidth,
      ),
    ).toBe(true);
    const controls = page.locator('button, select, input');
    for (const box of await controls.evaluateAll((elements) =>
      elements.map((element) => element.getBoundingClientRect().toJSON()),
    )) {
      expect(box.height).toBeGreaterThanOrEqual(44);
    }
    await page.screenshot({
      path: `test-results/lobby-${width}.png`,
      fullPage: true,
    });
  }
});

test('expired identity requires explicit recovery across simultaneous tabs', async ({
  browser,
}) => {
  const context = await browser.newContext({
    baseURL: 'http://localhost:5173',
  });
  try {
    await context.addCookies([
      {
        name: 'falsepoker_session',
        value: '0'.repeat(64),
        url: 'http://localhost:5173',
        httpOnly: true,
        sameSite: 'Strict',
      },
    ]);
    let sessionRequests = 0;
    context.on('request', (request) => {
      if (request.url().endsWith('/api/v1/session')) sessionRequests++;
    });
    const [a, b] = await Promise.all([context.newPage(), context.newPage()]);
    await Promise.all([a.goto('/'), b.goto('/')]);
    await Promise.all(
      [a, b].map((page) =>
        expect(
          page.getByRole('button', { name: 'Try session again' }),
        ).toBeVisible(),
      ),
    );
    expect(sessionRequests).toBe(1);
    await a.getByRole('button', { name: 'Try session again' }).click();
    await expect(a.getByLabel('Your display name')).toBeVisible();
    expect(sessionRequests).toBe(2);
    await expect(
      b.getByRole('button', { name: 'Try session again' }),
    ).toBeVisible();
    await b.getByRole('button', { name: 'Try session again' }).click();
    await expect(b.getByLabel('Your display name')).toBeVisible();
    expect(sessionRequests).toBe(3);
  } finally {
    await context.close();
  }
});
