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
  test.setTimeout(60_000);
  const contexts = await Promise.all([
    browser.newContext({
      baseURL: 'http://localhost:5173',
      viewport: { width: 390, height: 844 },
      isMobile: true,
      hasTouch: true,
    }),
    browser.newContext({ baseURL: 'http://localhost:5173' }),
    browser.newContext({ baseURL: 'http://localhost:5173' }),
    browser.newContext({
      baseURL: 'http://localhost:5173',
      viewport: { width: 390, height: 844 },
      isMobile: true,
      hasTouch: true,
    }),
  ]);
  try {
    const [admin, bob, carol, lateViewer] = await Promise.all(
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
          page.getByRole('region', { name: 'False Poker game table' }),
        ).toBeVisible(),
      ),
    );
    await lateViewer.goto(admin.url());
    await lateViewer.getByLabel('Your display name').fill('Dee');
    await lateViewer.getByRole('button', { name: /^Join room/ }).click();
    await expect(
      lateViewer.getByRole('region', { name: 'False Poker game table' }),
    ).toBeVisible();
    await expect(lateViewer.getByText('Watching the table')).toBeVisible();
    const lateTable = lateViewer.getByRole('region', {
      name: 'False Poker game table',
    });
    await expect(lateTable.getByRole('img')).toHaveCount(0);
    const individualPeek = lateViewer.getByRole('button', {
      name: /Bob, 1 card.*Hold to peek/,
    });
    await individualPeek.dispatchEvent('pointerdown', {
      button: 0,
      pointerId: 1,
      pointerType: 'touch',
      isPrimary: true,
    });
    await expect(lateTable.getByRole('img')).toHaveCount(1);
    await individualPeek.dispatchEvent('pointerup', {
      button: 0,
      pointerId: 1,
      pointerType: 'touch',
      isPrimary: true,
    });
    await expect(lateTable.getByRole('img')).toHaveCount(0);
    await individualPeek.focus();
    await lateViewer.keyboard.down('Space');
    await expect(lateTable.getByRole('img')).toHaveCount(1);
    await lateViewer.keyboard.up('Space');
    await expect(lateTable.getByRole('img')).toHaveCount(0);
    await individualPeek.locator('.opponent-fan').hover();
    await lateViewer.mouse.down();
    await lateViewer.mouse.move(5, 5, { steps: 3 });
    await expect(lateTable.getByRole('img')).toHaveCount(1);
    await lateViewer.mouse.up();
    await expect(lateTable.getByRole('img')).toHaveCount(0);
    const peekAllLate = lateViewer.getByRole('button', {
      name: 'Hold to peek at all hands',
    });
    await peekAllLate.dispatchEvent('pointerdown', {
      button: 0,
      pointerId: 2,
      pointerType: 'touch',
      isPrimary: true,
    });
    await expect(lateTable.getByRole('img')).toHaveCount(2);
    await peekAllLate.dispatchEvent('pointerup', {
      button: 0,
      pointerId: 2,
      pointerType: 'touch',
      isPrimary: true,
    });
    await expect(lateTable.getByRole('img')).toHaveCount(0);
    await peekAllLate.focus();
    await lateViewer.keyboard.down('Space');
    await expect(lateTable.getByRole('img')).toHaveCount(2);
    await lateViewer.keyboard.up('Space');
    await expect(lateTable.getByRole('img')).toHaveCount(0);
    const started = await admin.evaluate(
      async () =>
        (
          await (
            await fetch(`/api/v1/rooms/${location.pathname.split('/').at(-1)}`)
          ).json()
        ).state,
    );
    expect(
      started.game.players.map((p: { display_name: string }) => p.display_name),
    ).toEqual(['Carol', 'Bob']);
    const bobID = started.game.players.find(
      (p: { display_name: string }) => p.display_name === 'Bob',
    ).player_id;
    expect(started.game.starter_id).toBe(bobID);
    await expect(admin.getByText('Watching the table')).toBeVisible();
    await expect(bob.getByText('Your hand')).toBeVisible();
    await contexts[1].setOffline(true);
    await expect(bob.getByText('Offline', { exact: true })).toBeVisible();
    await expect(
      bob.getByRole('button', { name: 'Make a claim' }),
    ).toBeDisabled();
    await contexts[1].setOffline(false);
    await expect(bob.getByText('Connected', { exact: true })).toBeVisible();
    await expect(
      bob.getByRole('button', { name: 'Make a claim' }),
    ).toBeEnabled();
    await expect(
      admin
        .getByRole('region', { name: 'False Poker game table' })
        .getByRole('img'),
    ).toHaveCount(0);
    const peekAll = admin.getByRole('button', {
      name: 'Hold to peek at all hands',
    });
    await peekAll.dispatchEvent('pointerdown', {
      button: 0,
      pointerId: 1,
      pointerType: 'mouse',
      isPrimary: true,
    });
    await expect(
      admin
        .getByRole('region', { name: 'False Poker game table' })
        .getByRole('img'),
    ).toHaveCount(2);
    await peekAll.dispatchEvent('pointerup', {
      button: 0,
      pointerId: 1,
      pointerType: 'mouse',
      isPrimary: true,
    });
    await expect(
      admin
        .getByRole('region', { name: 'False Poker game table' })
        .getByRole('img'),
    ).toHaveCount(0);
    await expect(
      bob.getByRole('button', { name: 'Make a claim' }),
    ).toBeEnabled();
    await bob.getByRole('button', { name: 'Make a claim' }).click();
    await bob.getByRole('button', { name: /Place claim/ }).click();
    await expect(
      carol.getByRole('button', { name: 'Challenge' }),
    ).toBeEnabled();
    await carol.getByRole('button', { name: 'Challenge' }).click();
    await Promise.all(
      [admin, bob, carol].map((page) =>
        expect(
          page.getByRole('heading', { name: /The claim was (true|false)/ }),
        ).toBeVisible(),
      ),
    );
    const resolved = await carol.evaluate(
      async () =>
        (
          await (
            await fetch(`/api/v1/rooms/${location.pathname.split('/').at(-1)}`)
          ).json()
        ).state,
    );
    const starterName = resolved.game.players.find(
      (p: { player_id: string }) =>
        p.player_id === resolved.game.next_starter_id,
    ).display_name;
    const starterPage = starterName === 'Bob' ? bob : carol;
    await starterPage.getByRole('button', { name: 'Next round' }).click();
    await Promise.all(
      [bob, carol].map((page) =>
        expect(page.getByText('ROUND 2', { exact: true })).toBeVisible(),
      ),
    );
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
    await expect(bob.getByText('Your hand')).toBeVisible();
    await bob.getByRole('button', { name: 'Open game settings' }).click();
    await bob.getByRole('button', { name: 'Leave room' }).click();
    await expect(bob.getByRole('dialog')).toBeVisible();
    await bob.getByRole('button', { name: 'Stay in room' }).click();
    await expect(bob.getByRole('dialog')).not.toBeVisible();
    await bob.getByRole('button', { name: 'Open game settings' }).click();
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

    await Promise.all(
      [admin, carol].map((page) =>
        expect(page.getByRole('heading', { name: 'Carol wins' })).toBeVisible(),
      ),
    );
    await Promise.all(
      [admin, carol].map((page) =>
        page.getByRole('button', { name: 'Exit to room' }).click(),
      ),
    );
    await admin.getByRole('button', { name: 'I’m ready' }).click();
    await carol.getByRole('button', { name: 'I’m ready' }).click();
    await admin.getByLabel('First bidder').selectOption({ label: 'Alice' });
    await admin
      .getByRole('button', { name: 'Confirm updated choices' })
      .click();
    await admin.getByRole('button', { name: /Start game/ }).click();
    await expect(
      admin.getByRole('region', { name: 'False Poker game table' }),
    ).toBeVisible();
    await expect(admin.getByText('Your hand')).toBeVisible();
    await expect(
      admin
        .getByRole('region', { name: 'False Poker game table' })
        .getByRole('img'),
    ).toHaveCount(1);
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

test('gameplay reconnect retries a lost bid outcome with the exact original command', async ({
  browser,
}) => {
  test.setTimeout(60_000);
  const contexts = await Promise.all(
    Array.from({ length: 3 }, () =>
      browser.newContext({ baseURL: 'http://localhost:5173' }),
    ),
  );
  const [admin, bob, carol] = await Promise.all(
    contexts.map((context) => context.newPage()),
  );
  const bidCommands: string[] = [];
  const bidAcks: boolean[] = [];
  let bidRequestID: string | undefined;
  let connections = 0;
  let droppedFirstAck = false;
  try {
    await contexts[1].addInitScript(() => {
      const NativeWebSocket = window.WebSocket;
      const sockets: WebSocket[] = [];
      (window as unknown as { testSockets: WebSocket[] }).testSockets = sockets;
      window.WebSocket = class extends NativeWebSocket {
        constructor(url: string | URL, protocols?: string | string[]) {
          super(url, protocols);
          sockets.push(this);
        }
      };
    });
    await contexts[1].routeWebSocket('**/api/v1/rooms/*/ws?*', (route) => {
      connections++;
      const server = route.connectToServer();
      route.onMessage((message) => {
        const raw = String(message);
        const decoded = JSON.parse(raw) as {
          type: string;
          action?: string;
          request_id?: string;
        };
        if (decoded.type === 'command' && decoded.action === 'bid') {
          bidCommands.push(raw);
          bidRequestID ??= decoded.request_id;
        }
        server.send(message);
      });
      server.onMessage((message) => {
        const raw = String(message);
        const decoded = JSON.parse(raw) as {
          type: string;
          request_id?: string;
          replayed?: boolean;
        };
        if (decoded.type === 'ack' && decoded.request_id === bidRequestID) {
          bidAcks.push(decoded.replayed === true);
          if (!droppedFirstAck) {
            droppedFirstAck = true;
            return;
          }
        }
        route.send(message);
      });
    });

    await admin.goto('/');
    await enter(admin, 'Alice');
    await Promise.all([bob.goto(admin.url()), carol.goto(admin.url())]);
    await enter(bob, 'Bob');
    await enter(carol, 'Carol');
    await bob.getByRole('button', { name: 'I’m ready' }).click();
    await carol.getByRole('button', { name: 'I’m ready' }).click();
    await admin.getByLabel('First bidder').selectOption({ label: 'Bob' });
    await admin.getByRole('button', { name: /Start game/ }).click();
    await expect(
      bob.getByRole('button', { name: 'Make a claim' }),
    ).toBeVisible();

    const before = await bob.evaluate(async () => {
      const response = await fetch(
        `/api/v1/rooms/${location.pathname.split('/').at(-1)}`,
      );
      return (await response.json()).state;
    });
    const originalHand = await bob
      .locator('.self-card-fan [role="img"]')
      .evaluateAll((cards) =>
        cards.map((card) => card.getAttribute('aria-label')),
      );
    await bob.getByRole('button', { name: 'Make a claim' }).click();
    await bob.getByRole('button', { name: /Place claim/ }).click();

    await expect.poll(() => droppedFirstAck).toBe(true);
    await expect(bob.getByText(/High card: 9/)).toBeVisible();
    await bob.evaluate(() => {
      const sockets = (window as unknown as { testSockets: WebSocket[] })
        .testSockets;
      const socket = sockets.find(
        (candidate) =>
          candidate.url.includes('/api/v1/rooms/') &&
          candidate.readyState === WebSocket.OPEN,
      );
      if (!socket) throw new Error('No active gameplay WebSocket');
      socket.close(1000, 'disconnect after dropping bid outcome');
    });
    await expect.poll(() => connections).toBeGreaterThanOrEqual(2);
    await expect.poll(() => bidCommands).toHaveLength(2);
    await expect.poll(() => bidAcks).toEqual([false, true]);
    await expect(bob.getByText('Connected', { exact: true })).toBeVisible();
    await expect(bob.getByText(/High card: 9/)).toBeVisible();
    expect(bidRequestID).toMatch(/^[a-f0-9]{32}$/);
    expect(bidCommands[1]).toBe(bidCommands[0]);
    const after = await bob.evaluate(async () => {
      const response = await fetch(
        `/api/v1/rooms/${location.pathname.split('/').at(-1)}`,
      );
      return (await response.json()).state;
    });
    expect(BigInt(after.revision) - BigInt(before.revision)).toBe(1n);
    expect(
      await bob
        .locator('.self-card-fan [role="img"]')
        .evaluateAll((cards) =>
          cards.map((card) => card.getAttribute('aria-label')),
        ),
    ).toEqual(originalHand);

    const currentID = after.game.current_player_id;
    const currentName = after.game.players.find(
      (player: { player_id: string }) => player.player_id === currentID,
    ).display_name;
    const currentPage = currentName === 'Carol' ? carol : admin;
    await currentPage.getByRole('button', { name: 'Challenge' }).click();
    await expect(
      admin.getByRole('heading', { name: /The claim was (true|false)/ }),
    ).toBeVisible();
  } finally {
    await Promise.all(contexts.map((context) => context.close()));
  }
});

test('two players can finish a complete game through the gameplay UI', async ({
  browser,
}) => {
  test.setTimeout(90_000);
  const contexts = await Promise.all(
    Array.from({ length: 3 }, (_, index) =>
      browser.newContext({
        baseURL: 'http://localhost:5173',
        ...(index === 1
          ? {
              viewport: { width: 390, height: 844 },
              isMobile: true,
              hasTouch: true,
            }
          : {}),
      }),
    ),
  );
  const [admin, bob, carol] = await Promise.all(
    contexts.map((context) => context.newPage()),
  );
  const fetchState = (page: Page) =>
    page.evaluate(async () => {
      const response = await fetch(
        `/api/v1/rooms/${location.pathname.split('/').at(-1)}`,
      );
      return (await response.json()).state;
    });
  try {
    await admin.goto('/');
    await enter(admin, 'Alice');
    await Promise.all([bob.goto(admin.url()), carol.goto(admin.url())]);
    await enter(bob, 'Bob');
    await enter(carol, 'Carol');
    await bob.getByRole('button', { name: 'I’m ready' }).click();
    await carol.getByRole('button', { name: 'I’m ready' }).click();
    await admin.getByLabel('First bidder').selectOption({ label: 'Bob' });
    await admin.getByRole('button', { name: /Start game/ }).click();

    for (let round = 1; round <= 7; round++) {
      const state = await fetchState(admin);
      expect(state.game.round_id).toBe(String(round));
      const bidderID = state.game.current_player_id;
      const bidder = state.game.players.find(
        (player: { player_id: string }) => player.player_id === bidderID,
      );
      const bidderPage = bidder.display_name === 'Bob' ? bob : carol;
      const falseClaim = round === 2 || round >= 4;
      await bidderPage.getByRole('button', { name: 'Make a claim' }).click();
      if (falseClaim) {
        const rankCounts = new Map<string, number>(
          ['9', '10', 'J', 'Q', 'K', 'A'].map((rank) => [rank, 0]),
        );
        for (const player of state.game.players) {
          for (const card of player.hand ?? [])
            rankCounts.set(card.rank, (rankCounts.get(card.rank) ?? 0) + 1);
        }
        const missingFourOfAKind = [...rankCounts].find(
          ([, count]) => count < 4,
        )?.[0];
        expect(missingFourOfAKind).toBeDefined();
        await bidderPage
          .getByLabel('Combination')
          .selectOption('four_of_a_kind');
        await bidderPage.getByLabel('Rank').selectOption(missingFourOfAKind!);
      } else {
        const ownCard = await bidderPage
          .locator('.self-card-fan [role="img"]')
          .first()
          .getAttribute('aria-label');
        const ownRank = ownCard?.split(' of ')[0];
        expect(ownRank).toBeTruthy();
        await bidderPage.getByLabel('Combination').selectOption('high_card');
        await bidderPage.getByLabel('Rank').selectOption(ownRank!);
      }
      await bidderPage.getByRole('button', { name: /Place claim/ }).click();

      const bidState = await fetchState(admin);
      const challenger = bidState.game.players.find(
        (player: { player_id: string }) =>
          player.player_id === bidState.game.current_player_id,
      );
      const challengerPage = challenger.display_name === 'Bob' ? bob : carol;
      await challengerPage.getByRole('button', { name: 'Challenge' }).click();
      await expect
        .poll(async () => (await fetchState(admin)).game.round_phase)
        .toBe('resolution');
      const resultState = await fetchState(admin);
      expect(resultState.game.last_result.claim_satisfied).toBe(!falseClaim);
      expect(resultState.game.last_result.loser_id).toBe(
        falseClaim ? bidderID : challenger.player_id,
      );
      if (round < 7) {
        const starter = resultState.game.players.find(
          (player: { player_id: string }) =>
            player.player_id === resultState.game.next_starter_id,
        );
        await (starter.display_name === 'Bob' ? bob : carol)
          .getByRole('button', { name: 'Next round' })
          .click();
        if (round === 1) {
          expect(
            await bob.evaluate(
              () => document.documentElement.scrollHeight <= innerHeight,
            ),
          ).toBe(true);
          await bob.screenshot({
            path: 'test-results/phone-complete-game.png',
          });
        }
      }
    }

    const finished = await fetchState(admin);
    expect(finished.game.phase).toBe('finished');
    expect(finished.game.winner_id).toBe(
      finished.game.players.find(
        (player: { display_name: string }) => player.display_name === 'Carol',
      ).player_id,
    );
    await expect(
      admin.getByRole('heading', { name: 'Carol wins' }),
    ).toBeVisible();
  } finally {
    await Promise.all(contexts.map((context) => context.close()));
  }
});

test('eliminated and late-joining viewers can peek at private new-round hands', async ({
  browser,
}) => {
  test.setTimeout(90_000);
  const contexts = await Promise.all(
    Array.from({ length: 14 }, (_, index) =>
      browser.newContext({
        baseURL: 'http://localhost:5173',
        ...(index === 13
          ? {
              viewport: { width: 390, height: 844 },
              isMobile: true,
              hasTouch: true,
            }
          : {}),
      }),
    ),
  );
  const pages = await Promise.all(contexts.map((context) => context.newPage()));
  const names = Array.from(
    { length: 13 },
    (_, index) => `Player ${String(index + 1).padStart(2, '0')}`,
  );
  const fetchState = (page: Page) =>
    page.evaluate(async () => {
      const response = await fetch(
        `/api/v1/rooms/${location.pathname.split('/').at(-1)}`,
      );
      return (await response.json()).state;
    });
  try {
    await pages[0].goto('/');
    await enter(pages[0], names[0]);
    await Promise.all(
      pages.slice(1, 13).map(async (page, index) => {
        await page.goto(pages[0].url());
        await enter(page, names[index + 1]);
      }),
    );
    for (let index = 0; index < 13; index++) {
      await pages[index].getByRole('button', { name: 'I’m ready' }).click();
      await expect(
        pages[0].getByText(`${index + 1} ready`, { exact: true }),
      ).toBeVisible();
    }
    await pages[0].getByLabel('First bidder').selectOption({ label: names[0] });
    await pages[0].getByRole('button', { name: /Start game/ }).click();
    await expect(
      pages[0].getByRole('region', { name: 'False Poker game table' }),
    ).toBeVisible();

    const started = await fetchState(pages[0]);
    expect(started.game.players).toHaveLength(13);
    const pageFor = (id: string) => {
      const player = started.game.players.find(
        (item: { player_id: string }) => item.player_id === id,
      );
      const index = names.indexOf(player.display_name);
      if (index < 0) throw new Error(`Unknown player ${player.display_name}`);
      return pages[index];
    };
    const firstBidder = pageFor(started.game.current_player_id);

    // Join only after the game is active; this identity has no seat this game.
    const lateViewer = pages[13];
    await lateViewer.goto(pages[0].url());
    await lateViewer.getByLabel('Your display name').fill('Late viewer');
    await lateViewer.getByRole('button', { name: /^Join room/ }).click();
    await expect(
      lateViewer.getByRole('region', { name: 'False Poker game table' }),
    ).toBeVisible();

    await firstBidder.getByRole('button', { name: 'Make a claim' }).click();
    await firstBidder
      .getByLabel('Combination')
      .selectOption('royal_straight_flush');
    await firstBidder.getByLabel('Suit').selectOption('hearts');
    await firstBidder.getByRole('button', { name: /Place claim/ }).click();
    await expect
      .poll(async () => (await fetchState(pages[0])).game.current_player_id)
      .not.toBeNull();
    const afterBid = await fetchState(pages[0]);
    const challenger = pageFor(afterBid.game.current_player_id);
    await challenger.getByRole('button', { name: 'Challenge' }).click();
    await expect
      .poll(async () => (await fetchState(pages[0])).game.round_phase)
      .toBe('resolution');
    const resolved = await fetchState(pages[0]);
    const eliminated = resolved.game.players.find(
      (player: { eliminated: boolean }) => player.eliminated,
    );
    expect(eliminated).toBeDefined();
    const eliminatedViewer = pageFor(eliminated.player_id);
    await expect(
      eliminatedViewer.getByText('Watching the table'),
    ).toBeVisible();

    const roundStarter = pageFor(resolved.game.next_starter_id);
    await roundStarter.getByRole('button', { name: 'Next round' }).click();
    await expect(
      eliminatedViewer.getByText('ROUND 2', { exact: true }),
    ).toBeVisible();
    const eliminatedTable = eliminatedViewer.getByRole('region', {
      name: 'False Poker game table',
    });
    await expect(eliminatedTable.getByRole('img')).toHaveCount(0);
    const survivor = resolved.game.players.find(
      (player: { eliminated: boolean }) => !player.eliminated,
    );
    const survivorSeat = eliminatedViewer.getByRole('button', {
      name: new RegExp(`${survivor.display_name}, 1 card.*Hold to peek`),
    });
    await survivorSeat.focus();
    await eliminatedViewer.keyboard.down('Space');
    await expect(eliminatedTable.getByRole('img')).toHaveCount(1);
    await eliminatedViewer.keyboard.up('Space');
    await expect(eliminatedTable.getByRole('img')).toHaveCount(0);

    const peekAll = eliminatedViewer.getByRole('button', {
      name: 'Hold to peek at all hands',
    });
    await peekAll.dispatchEvent('pointerdown', {
      button: 0,
      pointerId: 1,
      pointerType: 'touch',
      isPrimary: true,
    });
    await expect(eliminatedTable.getByRole('img')).toHaveCount(12);
    await peekAll.dispatchEvent('pointerup', {
      button: 0,
      pointerId: 1,
      pointerType: 'touch',
      isPrimary: true,
    });
    await expect(eliminatedTable.getByRole('img')).toHaveCount(0);

    const lateTable = lateViewer.getByRole('region', {
      name: 'False Poker game table',
    });
    await expect(
      lateViewer.getByText('ROUND 2', { exact: true }),
    ).toBeVisible();
    await expect(lateTable.getByRole('img')).toHaveCount(0);
    const lateSeat = lateViewer.getByRole('button', {
      name: new RegExp(`${survivor.display_name}, 1 card.*Hold to peek`),
    });
    await lateSeat.dispatchEvent('pointerdown', {
      button: 0,
      pointerId: 2,
      pointerType: 'touch',
      isPrimary: true,
    });
    await expect(lateTable.getByRole('img')).toHaveCount(1);
    await lateSeat.dispatchEvent('pointerup', {
      button: 0,
      pointerId: 2,
      pointerType: 'touch',
      isPrimary: true,
    });
    await expect(lateTable.getByRole('img')).toHaveCount(0);
    const latePeekAll = lateViewer.getByRole('button', {
      name: 'Hold to peek at all hands',
    });
    await latePeekAll.focus();
    await lateViewer.keyboard.down('Space');
    await expect(lateTable.getByRole('img')).toHaveCount(12);
    await lateViewer.keyboard.up('Space');
    await expect(lateTable.getByRole('img')).toHaveCount(0);
  } finally {
    await Promise.all(contexts.map((context) => context.close()));
  }
});

test('a disconnected final room member expires after the real reconnect grace', async ({
  browser,
}) => {
  test.setTimeout(100_000);
  const context = await browser.newContext({
    baseURL: 'http://localhost:5173',
  });
  try {
    let page = await context.newPage();
    await page.goto('/');
    await enter(page, 'Grace');
    const invitation = page.url();
    await expect(
      page.getByText('Your connection: Connected', { exact: true }),
    ).toBeVisible();
    await page.close();

    // The server owns this timer; browser clock overrides cannot advance it.
    await new Promise((resolve) => setTimeout(resolve, 61_000));

    page = await context.newPage();
    await page.goto(invitation);
    await expect(
      page.getByText('This room or your membership is no longer available.'),
    ).toBeVisible({ timeout: 15_000 });
    await expect(
      page.getByRole('heading', { name: 'Take a seat.' }),
    ).toBeVisible();
    await expect(page.getByLabel('Room code')).toHaveValue(
      invitation.split('/').at(-1)!,
    );
  } finally {
    await context.close();
  }
});
