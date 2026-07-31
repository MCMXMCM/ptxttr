import { normalizeRelayList } from "./relay-config.js";
import {
  profileAuthorWriteRelays,
  profileFallbackRelays,
  profileRelayListsMatch,
} from "./profile-relay-hints.js";

export function emptyProfileRelayHints() {
  return { read: [], write: [], any: [] };
}

export function hasAuthoritativeProfileEvent(profile) {
  return Boolean(String(profile?.event_id || "").trim());
}

export function renderableProfileFieldCount(profile) {
  const fields = [
    profile?.display_name,
    profile?.name,
    profile?.about,
    profile?.picture,
    profile?.avatar_url,
    profile?.nip05,
    profile?.website,
    profile?.lud16,
    profile?.lud06,
  ];
  return fields.reduce((count, value) => (String(value || "").trim() ? count + 1 : count), 0);
}

export function hasRenderableProfileMetadata(profile) {
  return renderableProfileFieldCount(profile) > 0;
}

export function shouldPromoteProfileMetadata(currentProfile, nextProfile) {
  if (!nextProfile?.pubkey) return false;
  if (!currentProfile?.pubkey) return true;

  const currentAuthoritative = hasAuthoritativeProfileEvent(currentProfile);
  const nextAuthoritative = hasAuthoritativeProfileEvent(nextProfile);
  if (nextAuthoritative && !currentAuthoritative) return true;
  if (!nextAuthoritative && currentAuthoritative) return false;

  const currentCreatedAt = Number(currentProfile?.created_at || 0);
  const nextCreatedAt = Number(nextProfile?.created_at || 0);
  if (nextCreatedAt > currentCreatedAt) return true;
  if (nextCreatedAt < currentCreatedAt) return false;

  const currentFieldCount = renderableProfileFieldCount(currentProfile);
  const nextFieldCount = renderableProfileFieldCount(nextProfile);
  if (nextFieldCount > currentFieldCount) return true;
  if (nextFieldCount < currentFieldCount) return false;

  return String(nextProfile?.event_id || "").trim() !== String(currentProfile?.event_id || "").trim();
}

export function buildProfileHydrationRelayStages(
  initialRelays = [],
  relayHints = {},
  followRelayHints = null,
  targetPubkey = "",
  fallbackRelays = [],
) {
  const stages = [];
  const pushStage = (relays) => {
    const normalized = normalizeRelayList(relays);
    if (!normalized.length) return;
    if (stages.some((stage) => profileRelayListsMatch(stage, normalized))) return;
    stages.push(normalized);
  };
  pushStage(initialRelays);
  pushStage(profileAuthorWriteRelays(relayHints));
  pushStage(profileFallbackRelays({ read: [], write: [], any: [] }, followRelayHints, targetPubkey));
  pushStage(fallbackRelays);
  return stages;
}

export async function fetchInitialProfilePostsAcrossRelayStages(
  pubkey,
  relayStages = [],
  {
    fetchPosts,
    kinds = [],
  } = {},
) {
  const stages = (Array.isArray(relayStages) ? relayStages : [])
    .map((relays) => normalizeRelayList(relays))
    .filter((relays) => relays.length > 0);
  const results = await Promise.allSettled(
    stages.map(async (relays) => ({
      relays,
      posts: await fetchPosts(pubkey, { relays, kinds }),
    })),
  );
  const byID = new Map();
  const relaysWithPosts = [];
  for (const result of results) {
    if (result.status !== "fulfilled" || !Array.isArray(result.value.posts)) continue;
    if (result.value.posts.length) relaysWithPosts.push(...result.value.relays);
    for (const event of result.value.posts) {
      const id = String(event?.id || "").trim();
      if (!id) continue;
      const current = byID.get(id);
      if (!current || Number(event?.created_at || 0) >= Number(current?.created_at || 0)) {
        byID.set(id, event);
      }
    }
  }
  const posts = [...byID.values()].sort((a, b) => (
    Number(b?.created_at || 0) - Number(a?.created_at || 0) ||
    String(b?.id || "").localeCompare(String(a?.id || ""))
  ));
  return {
    posts,
    relaysUsed: normalizeRelayList(relaysWithPosts.length ? relaysWithPosts : stages.at(-1) || []),
  };
}

export async function fetchProfileFollowGraphAcrossRelayStages(
  pubkey,
  relayStages = [],
  {
    fetchFollowGraph,
    followerLimit = 250,
  } = {},
) {
  const stages = Array.isArray(relayStages) ? relayStages : [];
  let relaysUsed = normalizeRelayList(stages[0] || []);
  let latestFollowEvent = null;
  let latestFollowing = [];
  let latestRelayHints = new Map();
  const followers = new Set();

  const normalizedStages = stages.map((relays) => normalizeRelayList(relays)).filter((relays) => relays.length > 0);
  const results = await Promise.allSettled(normalizedStages.map(async (relays) => ({
    relays,
    graph: await fetchFollowGraph(pubkey, {
      relays,
      followerLimit,
      includeViewerRelays: false,
    }),
  })));

  for (const result of results) {
    if (result.status !== "fulfilled") continue;
    const normalized = result.value.relays;
    const graph = result.value.graph;
    relaysUsed = normalized;
    const followEvent = graph?.followEvent || null;
    if (
      followEvent &&
      (
        !latestFollowEvent ||
        Number(followEvent.created_at || 0) >= Number(latestFollowEvent.created_at || 0)
      )
    ) {
      latestFollowEvent = followEvent;
      latestFollowing = Array.isArray(graph?.following) ? graph.following : [];
      latestRelayHints = graph?.relayHints instanceof Map ? graph.relayHints : new Map();
    }
    for (const follower of Array.isArray(graph?.followers) ? graph.followers : []) {
      if (follower) followers.add(follower);
    }
  }

  return {
    pubkey,
    following: latestFollowing,
    followers: [...followers],
    followEvent: latestFollowEvent,
    relayHints: latestRelayHints,
    relaysUsed,
  };
}
