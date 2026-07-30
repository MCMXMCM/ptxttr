/** Deterministic signed Nostr events for relay-native e2e. */

import { finalizeEvent, getPublicKey } from "../../web/static/lib/nostr-tools.js";

function secretKey(byte) {
  return new Uint8Array(32).fill(byte);
}

const AUTHOR_SK = secretKey(0x22);
const VIEWER_SK = secretKey(0x11);
const REPLY_AUTHOR_SK = secretKey(0x33);

export const AUTHOR_PK = getPublicKey(AUTHOR_SK);
export const REPLY_AUTHOR_PK = getPublicKey(REPLY_AUTHOR_SK);
export const VIEWER_PK = getPublicKey(VIEWER_SK);
export const MOCK_RELAY_URL = "wss://mock.ptxt.test";

const T0 = 1_700_000_000;
const T_FEED = T0 + 100;
const T_ROOT = T0 + 80;
const T_REPLY = T0 + 90;
const T_BOOKMARK_NOTE = T0 + 95;
const T_BOOKMARK_LIST = T0 + 110;
const T_MEDIA_FEED = T0 + 120;
const T_VIDEO_FEED = T0 + 121;
const T_OLDER_PROFILE_NOTE = T0 - 100;

/**
 * @param {object} draft
 * @param {Uint8Array} [secretKeyBytes]
 * @returns {import('./mock-nostr-relay.js').NostrEvent}
 */
function signEvent(draft, secretKeyBytes = AUTHOR_SK) {
  const signed = finalizeEvent(draft, secretKeyBytes);
  return {
    id: signed.id.toLowerCase(),
    pubkey: signed.pubkey.toLowerCase(),
    created_at: signed.created_at,
    kind: signed.kind,
    tags: signed.tags,
    content: signed.content,
    sig: signed.sig,
  };
}

function buildProfileEvent(name, created_at = T0, extra = {}, secretKeyBytes = AUTHOR_SK) {
  return signEvent({
    kind: 0,
    content: JSON.stringify({ name, display_name: name, ...extra }),
    tags: [],
    created_at,
  }, secretKeyBytes);
}

const feedBundle = (() => {
  const profile = buildProfileEvent("Relay Author");
  const note = signEvent({
    kind: 1,
    content: "e2e-relay-native-feed-note",
    tags: [],
    created_at: T_FEED,
  });
  return { profile, note };
})();

const threadBundle = (() => {
  const profile = buildProfileEvent("Thread Author");
  const replyProfile = buildProfileEvent("Reply Author", T0 + 1, {}, REPLY_AUTHOR_SK);
  const follow = signEvent({
    kind: 3,
    content: "",
    tags: [["p", REPLY_AUTHOR_PK]],
    created_at: T0 + 2,
  });
  const root = signEvent({
    kind: 1,
    content: "e2e-relay-native-thread-root",
    tags: [],
    created_at: T_ROOT,
  });
  const reply = signEvent({
    kind: 1,
    content: "e2e-relay-native-thread-reply",
    tags: [
      ["e", root.id, "", "root"],
      ["e", root.id, "", "reply"],
    ],
    created_at: T_REPLY,
  }, REPLY_AUTHOR_SK);
  return { profile, replyProfile, follow, root, reply };
})();

const legacyPositionalThreadBundle = (() => {
  const profile = buildProfileEvent("Legacy Thread Author", T0 + 2);
  const replyProfile = buildProfileEvent("Legacy Reply Author", T0 + 3, {}, REPLY_AUTHOR_SK);
  const root = signEvent({
    kind: 1,
    content: "e2e-legacy-thread-root",
    tags: [],
    created_at: T0 + 130,
  });
  const parent = signEvent({
    kind: 1,
    content: "e2e-legacy-thread-parent",
    tags: [["e", root.id]],
    created_at: T0 + 131,
  }, REPLY_AUTHOR_SK);
  const selected = signEvent({
    kind: 1,
    content: "e2e-legacy-thread-selected",
    tags: [
      ["e", root.id],
      ["e", "f".repeat(64)],
      ["e", parent.id],
      ["p", parent.pubkey],
    ],
    created_at: T0 + 132,
  });
  return { profile, replyProfile, root, parent, selected };
})();

const bookmarkBundle = (() => {
  const profile = buildProfileEvent("Bookmark Author");
  const note = signEvent({
    kind: 1,
    content: "e2e-relay-native-bookmark-note",
    tags: [],
    created_at: T_BOOKMARK_NOTE,
  });
  const list = signEvent(
    {
      kind: 10003,
      content: "",
      tags: [["e", note.id, MOCK_RELAY_URL]],
      created_at: T_BOOKMARK_LIST,
    },
    VIEWER_SK,
  );
  return { profile, note, list };
})();

const mediaFeedBundle = (() => {
  const port = Number(process.env.PTXT_E2E_PORT || 18080);
  const imageURL = `http://127.0.0.1:${port}/static/img/ascritch.png`;
  const videoURL = `http://127.0.0.1:${port}/static/missing-video.mp4`;
  const profile = buildProfileEvent("Media Feed Author", T0 + 220);
  const note = signEvent({
    kind: 1,
    content: `e2e-feed-media-note ${imageURL} ${imageURL}?v=2`,
    tags: [],
    created_at: T_MEDIA_FEED,
  });
  const videoNote = signEvent({
    kind: 1,
    content: `e2e-feed-video-note ${videoURL}`,
    tags: [],
    created_at: T_VIDEO_FEED,
  });
  return { profile, note, videoNote };
})();

const olderProfileBundle = (() => {
  const profile = buildProfileEvent("Older Profile Author", T0 + 300);
  const older = signEvent({
    kind: 1,
    content: "e2e-older-profile-thread-root",
    tags: [],
    created_at: T_OLDER_PROFILE_NOTE,
  });
  const newer = Array.from({ length: 26 }, (_, index) => signEvent({
    kind: 1,
    content: `e2e-newer-profile-note-${index + 1}`,
    tags: [],
    created_at: T0 + 301 + index,
  }));
  return { profile, older, newer };
})();

export const FEED_NOTE_ID = feedBundle.note.id;
export const ROOT_ID = threadBundle.root.id;
export const REPLY_ID = threadBundle.reply.id;
export const BOOKMARK_NOTE_ID = bookmarkBundle.note.id;
export const MEDIA_FEED_NOTE_ID = mediaFeedBundle.note.id;
export const VIDEO_FEED_NOTE_ID = mediaFeedBundle.videoNote.id;
export const OLDER_PROFILE_NOTE_ID = olderProfileBundle.older.id;
export const LEGACY_ROOT_ID = legacyPositionalThreadBundle.root.id;
export const LEGACY_PARENT_ID = legacyPositionalThreadBundle.parent.id;
export const LEGACY_SELECTED_ID = legacyPositionalThreadBundle.selected.id;

export function buildFeedFixture() {
  return [feedBundle.profile, feedBundle.note];
}

export function buildThreadFixture() {
  return [threadBundle.profile, threadBundle.replyProfile, threadBundle.follow, threadBundle.root, threadBundle.reply];
}

export function buildReplyOnlyFixture() {
  return [threadBundle.replyProfile, threadBundle.reply];
}

export function buildLegacyPositionalThreadFixture() {
  return [
    legacyPositionalThreadBundle.profile,
    legacyPositionalThreadBundle.replyProfile,
    legacyPositionalThreadBundle.root,
    legacyPositionalThreadBundle.parent,
    legacyPositionalThreadBundle.selected,
  ];
}

export function buildBookmarkFixture() {
  return [bookmarkBundle.profile, bookmarkBundle.note, bookmarkBundle.list];
}

export function buildMediaFeedFixture() {
  return [mediaFeedBundle.profile, mediaFeedBundle.note, mediaFeedBundle.videoNote];
}

export function buildOlderProfileNoteFixture() {
  return [olderProfileBundle.profile, olderProfileBundle.older, ...olderProfileBundle.newer];
}

export function buildCombinedFixture() {
  const byID = new Map();
  for (const event of [...buildFeedFixture(), ...buildThreadFixture(), ...buildMediaFeedFixture()]) {
    byID.set(event.id, event);
  }
  return [...byID.values()];
}

export function buildFreshTrendingFixture() {
  const createdAt = Math.floor(Date.now() / 1000) - 30;
  const profile = buildProfileEvent("Trending Author", createdAt - 1);
  const note = signEvent({
    kind: 1,
    content: "e2e-fresh-trending-thread-root",
    tags: [],
    created_at: createdAt,
  });
  return [profile, note];
}

export function buildBrokenAvatarProfileFixture() {
  const profile = buildProfileEvent("Broken Avatar Author", T0 + 200, {
    picture: "http://127.0.0.1:1/missing-avatar.png",
  });
  return [profile, feedBundle.note];
}

export function buildAvatarThreadFixture() {
  const port = Number(process.env.PTXT_E2E_PORT || 18080);
  const picture = `http://127.0.0.1:${port}/static/img/ascritch.png`;
  const profile = buildProfileEvent("Avatar Thread Author", T0 + 210, {
    picture,
  });
  const replyProfile = buildProfileEvent("Avatar Reply Author", T0 + 211, {
    picture,
  }, REPLY_AUTHOR_SK);
  return [profile, replyProfile, threadBundle.follow, feedBundle.note, threadBundle.root, threadBundle.reply];
}
