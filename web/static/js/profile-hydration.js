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
  let latest = [];
  let relaysUsed = normalizeRelayList(relayStages[0] || []);
  for (const relays of relayStages) {
    const normalized = normalizeRelayList(relays);
    if (!normalized.length) continue;
    relaysUsed = normalized;
    latest = await fetchPosts(pubkey, { relays: normalized, kinds });
    if (latest.length) {
      return { posts: latest, relaysUsed: normalized };
    }
  }
  return { posts: latest, relaysUsed };
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

  for (const relays of stages) {
    const normalized = normalizeRelayList(relays);
    if (!normalized.length) continue;
    const graph = await fetchFollowGraph(pubkey, {
      relays: normalized,
      followerLimit,
      includeViewerRelays: false,
    });
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
