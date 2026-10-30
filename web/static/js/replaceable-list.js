import { publishSignedEvent } from "./publish.js";
import { fetchLatestListForWrite } from "./relay-reads.js";
import { normalizePubkey } from "./relay-utils.js";
import { getSession } from "./session.js";
import { signEventDraft } from "./signer.js";

// A replaceable list event (kind 3 contacts, 10000 mutes, 10003 bookmarks)
// replaces the user's whole list on every relay. Building one from an empty,
// stale or truncated snapshot silently deletes entries, so each update starts
// from the newest copy available and changes only the entry being toggled.

const defaultListDeps = {
  fetchLatest: fetchLatestListForWrite,
  sign: (draft) => signEventDraft(draft, getSession()),
  publish: publishSignedEvent,
  now: () => Math.floor(Date.now() / 1000),
};
const listDeps = { ...defaultListDeps };

const writeChains = new Map();
// Relay reads are briefly cached, so remember what this tab just published and
// build the next write on whichever copy is newer.
const lastPublishedLists = new Map();

function tagMatches(row, name, value) {
  return Array.isArray(row)
    && row[0] === name
    && String(row[1] || "").trim().toLowerCase() === value;
}

function ownListEvent(event, pubkey, kind) {
  if (!event || Number(event.kind) !== kind || !Array.isArray(event.tags)) return null;
  return normalizePubkey(event.pubkey) === pubkey ? event : null;
}

/** Newer of two copies of the same list; equal timestamps keep the lower id (NIP-01). */
export function newerListEvent(left, right) {
  if (!left) return right || null;
  if (!right) return left;
  const leftAt = Number(left.created_at) || 0;
  const rightAt = Number(right.created_at) || 0;
  if (leftAt !== rightAt) return rightAt > leftAt ? right : left;
  return String(right.id || "") < String(left.id || "") ? right : left;
}

export function listHasTag(event, name, value) {
  const want = String(value || "").trim().toLowerCase();
  return Array.isArray(event?.tags) && event.tags.some((row) => tagMatches(row, name, want));
}

/**
 * Tags for the replacement list: every existing tag in its original order,
 * with all `[name, value, ...]` rows removed, or `tag` appended when missing.
 * Client metadata tags are dropped because the signer adds this client's own.
 */
export function toggledListTags(event, tag, enabled) {
  const [name, value] = tag;
  const want = String(value || "").trim().toLowerCase();
  const tags = (Array.isArray(event?.tags) ? event.tags : [])
    .filter((row) => Array.isArray(row) && row[0] !== "client")
    .map((row) => [...row]);
  if (!enabled) return tags.filter((row) => !tagMatches(row, name, want));
  if (tags.some((row) => tagMatches(row, name, want))) return tags;
  return [...tags, [...tag]];
}

/** created_at for a replacement: never older than now, always newer than the list it replaces. */
export function nextReplaceableCreatedAt(previous, nowSeconds) {
  return Math.max(nowSeconds, (Number(previous?.created_at) || 0) + 1);
}

async function updateListOnce(key, pubkey, kind, { tag, enabled, confirm, maxTags }) {
  const fetched = ownListEvent(await listDeps.fetchLatest(pubkey, kind), pubkey, kind);
  const current = newerListEvent(fetched, lastPublishedLists.get(key));
  if (listHasTag(current, tag[0], tag[1]) === enabled) return { event: current, changed: false };
  if (!(await confirm(current))) return { event: current, changed: false };
  const tags = toggledListTags(current, tag, enabled);
  if (enabled && maxTags > 0 && tags.length > maxTags) {
    throw new Error("list exceeds tag limit");
  }
  const signed = await listDeps.sign({
    kind,
    created_at: nextReplaceableCreatedAt(current, listDeps.now()),
    tags,
    content: String(current?.content ?? ""),
  });
  // A browser extension can switch accounts underneath the page; publishing
  // this list as another key would overwrite that account's list instead.
  if (normalizePubkey(signed?.pubkey) !== pubkey) {
    throw new Error("The signer used a different account than the one logged in here.");
  }
  await listDeps.publish(signed);
  lastPublishedLists.set(key, signed);
  return { event: signed, changed: true };
}

/**
 * Add (`enabled`) or remove one tag in the viewer's replaceable list and
 * publish the result. `confirm(current)` runs before any publish; `current` is
 * null when no list could be found, so callers can ask before creating one.
 * Updates to the same list run one at a time. Resolves `{ event, changed }`,
 * where `event` is the list now in effect (null when none exists).
 */
export function updateReplaceableList({ pubkey, kind, tag, enabled, confirm = () => true, maxTags = 0 }) {
  const owner = normalizePubkey(pubkey);
  if (!owner) return Promise.reject(new Error("Log in first to update this list."));
  const key = `${owner}:${kind}`;
  const options = { tag, enabled: Boolean(enabled), confirm, maxTags };
  const run = (writeChains.get(key) || Promise.resolve())
    .catch(() => {})
    .then(() => updateListOnce(key, owner, kind, options));
  writeChains.set(key, run.catch(() => {}));
  return run;
}

export function setReplaceableListTestHooks(overrides = null) {
  Object.assign(listDeps, defaultListDeps, overrides || {});
  writeChains.clear();
  lastPublishedLists.clear();
}
