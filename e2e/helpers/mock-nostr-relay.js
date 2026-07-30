/**
 * Playwright mock Nostr relay over routeWebSocket.
 * Handles REQ / EVENT / OK for relay-native client tests.
 */

/**
 * @typedef {object} NostrEvent
 * @property {string} id
 * @property {string} pubkey
 * @property {number} created_at
 * @property {number} kind
 * @property {string[][]} tags
 * @property {string} content
 * @property {string} sig
 */

/** @param {NostrEvent} event @param {Record<string, unknown>} filter @param {{ omitProfilePostIds?: Set<string> }} [options] */
function matchesFilter(event, filter, options = {}) {
  if (filter.kinds && !filter.kinds.includes(event.kind)) return false;

  if (filter.ids?.length) {
    const ids = filter.ids.map((id) => String(id).toLowerCase());
    if (!ids.includes(String(event.id).toLowerCase())) return false;
  }

  if (filter.authors?.length) {
    const authors = filter.authors.map((pk) => String(pk).toLowerCase());
    if (!authors.includes(String(event.pubkey).toLowerCase())) return false;
  }

  if (filter["#e"]?.length) {
    const want = new Set(filter["#e"].map((id) => String(id).toLowerCase()));
    const tagged = (event.tags || [])
      .filter((tag) => Array.isArray(tag) && tag[0] === "e" && tag[1])
      .map((tag) => String(tag[1]).toLowerCase());
    if (!tagged.some((id) => want.has(id))) return false;
  }

  if (filter["#p"]?.length) {
    const want = new Set(filter["#p"].map((id) => String(id).toLowerCase()));
    const tagged = (event.tags || [])
      .filter((tag) => Array.isArray(tag) && tag[0] === "p" && tag[1])
      .map((tag) => String(tag[1]).toLowerCase());
    if (!tagged.some((id) => want.has(id))) return false;
  }

  if (typeof filter.since === "number" && event.created_at <= filter.since) return false;
  if (typeof filter.until === "number" && event.created_at >= filter.until) return false;
  if (
    filter.authors?.length
    && !filter.ids?.length
    && options.omitProfilePostIds?.has(String(event.id).toLowerCase())
  ) {
    return false;
  }

  return true;
}

/** @param {NostrEvent[]} events @param {Record<string, unknown>[]} filters @param {{ omitProfilePostIds?: Set<string> }} [options] */
function queryEvents(events, filters, options = {}) {
  const filterList = filters.length ? filters : [{}];
  let matched = events.filter((event) => filterList.every((filter) => matchesFilter(event, filter, options)));
  matched.sort((a, b) => {
    const delta = Number(b.created_at) - Number(a.created_at);
    if (delta !== 0) return delta;
    return String(b.id).localeCompare(String(a.id));
  });
  const limits = filterList.map((filter) => Number(filter.limit)).filter((n) => Number.isFinite(n) && n > 0);
  const limit = limits.length ? Math.min(...limits) : 500;
  return matched.slice(0, limit);
}

function attachMockRelayHandler(ws, store, options = {}) {
  const responseDelayMs = Math.max(0, Number(options.responseDelayMs) || 0);
  const omitProfilePostIds = new Set((options.omitProfilePostIds || []).map((id) => String(id).toLowerCase()));
  ws.onMessage((raw) => {
    let message;
    try {
      message = JSON.parse(String(raw));
    } catch {
      return;
    }
    if (!Array.isArray(message) || !message.length) return;

    const [cmd, ...rest] = message;
    if (cmd === "REQ") {
      const [subId, ...filters] = rest;
      const results = queryEvents(store, filters, { omitProfilePostIds });
      const sendResults = () => {
        for (const event of results) {
          ws.send(JSON.stringify(["EVENT", subId, event]));
        }
        ws.send(JSON.stringify(["EOSE", subId]));
      };
      if (responseDelayMs > 0) {
        setTimeout(sendResults, responseDelayMs);
      } else {
        sendResults();
      }
      return;
    }

    if (cmd === "EVENT") {
      const [signed] = rest;
      if (!signed?.id) return;
      const index = store.findIndex((event) => event.id === signed.id);
      if (index >= 0) store[index] = signed;
      else store.push(signed);
      ws.send(JSON.stringify(["OK", signed.id, true, ""]));
    }
  });
}

/**
 * @param {import('@playwright/test').Page} page
 * @param {{ events?: NostrEvent[], relayURL?: string, responseDelayMs?: number, omitProfilePostIds?: string[] }} [options]
 * @returns {Promise<NostrEvent[]>}
 */
export async function installMockNostrRelay(page, options = {}) {
  const relayURL = options.relayURL || "wss://mock.ptxt.test";
  const store = [...(options.events || [])];
  const mockHost = relayURL.replace(/^wss?:\/\//, "").replace(/\/+$/, "");

  await page.routeWebSocket(/wss?:\/\//i, (ws) => {
    const url = ws.url();
    if (!url.includes(mockHost)) {
      ws.close();
      return;
    }
    attachMockRelayHandler(ws, store, options);
  });

  return store;
}

/** Block Go read APIs so failures surface if the client falls back. */
export async function blockServerReadAPI(page) {
  await page.route(/\/api\//, (route) =>
    route.fulfill({
      status: 503,
      contentType: "text/plain",
      body: "relay-native e2e: server API blocked",
    }),
  );
}

/** Keep server feed fragments from racing ahead of relay-native hydration. */
export async function blockServerFeedFragments(page) {
  await page.route((url) => {
    const parsed = new URL(url);
    if (parsed.searchParams.get("fragment") !== "1") return false;
    return parsed.pathname === "/feed" || parsed.pathname === "/";
  }, async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "text/html; charset=utf-8",
      headers: {
        "X-Ptxt-Has-More": "0",
        "X-Ptxt-Cursor": "0",
        "X-Ptxt-Cursor-Id": "",
      },
      body: '<p class="muted">relay-native e2e placeholder</p>',
    });
  });
}

/**
 * @param {import('@playwright/test').Page} page
 * @param {{ relayURL?: string, viewerPubkey?: string, npub?: string, wotEnabled?: boolean, wotSeed?: string }} [options]
 */
export async function installRelayNativePrefs(page, options = {}) {
  const relayURL = options.relayURL || "wss://mock.ptxt.test";
  const viewerPubkey = options.viewerPubkey || "";
  const npub = options.npub || "npub1relaynativee2e000000000000000000000000000000000000000000000000000000";
  const wotEnabled = options.wotEnabled === true;
  const wotSeed = String(options.wotSeed || "").trim();

  await page.addInitScript(
    ({ relayURL, viewerPubkey, npub, wotEnabled, wotSeed }) => {
      localStorage.setItem("ptxt_direct_relays", "1");
      localStorage.setItem("ptxt_direct_relays_fallback", "0");
      localStorage.setItem("ptxt_relays", JSON.stringify([relayURL]));
      localStorage.setItem("ptxt_wot_enabled", wotEnabled ? "1" : "0");
      if (wotSeed) {
        localStorage.setItem("ptxt_wot_seed_pubkey", wotSeed);
      } else {
        localStorage.removeItem("ptxt_wot_seed_pubkey");
      }
      localStorage.setItem("ptxt_feed_sort", "recent");
      if (viewerPubkey) {
        localStorage.setItem(
          "ptxt_nostr_session",
          JSON.stringify({
            method: "readonly",
            pubkey: viewerPubkey,
            npub,
            canSign: false,
          }),
        );
      } else {
        localStorage.removeItem("ptxt_nostr_session");
      }
    },
    { relayURL, viewerPubkey, npub, wotEnabled, wotSeed },
  );
}

/**
 * @param {import('@playwright/test').Page} page
 * @param {{ events?: NostrEvent[], relayURL?: string, viewerPubkey?: string, wotEnabled?: boolean, responseDelayMs?: number, omitProfilePostIds?: string[] }} [options]
 */
export async function installRelayNativeE2E(page, options = {}) {
  await blockServerReadAPI(page);
  await blockServerFeedFragments(page);
  // WoT is now always enabled. Anchor relay-native fixtures to one of their
  // own authors so these tests exercise direct reads without depending on the
  // production Gigi graph being present in the isolated e2e database.
  const fixtureSeed = String(
    options.wotSeed || options.events?.find((event) => event?.kind === 1)?.pubkey || "",
  ).trim();
  await installRelayNativePrefs(page, {
    ...options,
    // Guest scope is deliberately pinned to Gigi. Relay-native fixture tests
    // use a read-only fixture viewer so their isolated author graph can be the
    // active WoT without weakening that production guest invariant.
    viewerPubkey: options.viewerPubkey || fixtureSeed,
    wotSeed: fixtureSeed,
  });
  return await installMockNostrRelay(page, options);
}

/** Navigate to a thread through the same document link path a feed card uses. */
export async function navigateToThreadFromFeed(page, rootID) {
  const path = new URL(page.url()).pathname;
  if (path !== "/feed" && path !== "/") {
    await page.goto("/feed");
  }
  await page.locator("#feed[data-relay-native-feed='1']").waitFor({ timeout: 30_000 });
  await page.evaluate((threadID) => {
    const link = document.createElement("a");
    link.href = `/thread/${threadID}`;
    link.setAttribute("data-relay-aware", "");
    document.body.append(link);
    link.click();
  }, rootID);
}
