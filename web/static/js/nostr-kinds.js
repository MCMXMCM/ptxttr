/** Nostr event kinds used by PTN (mirrors internal/nostrx/types.go). */
export const KIND_PROFILE = 0;
export const KIND_NOTE = 1;
export const KIND_COMMENT = 1111;
export const KIND_FOLLOW = 3;
export const KIND_REPOST = 6;
export const KIND_REACTION = 7;
export const KIND_POLL_RESPONSE = 1018;
export const KIND_POLL = 1068;
export const KIND_ZAP_REQUEST = 9734;
export const KIND_ZAP_RECEIPT = 9735;
export const KIND_MUTE = 10000;
export const KIND_RELAY_LIST = 10002;
export const KIND_BOOKMARK = 10003;
export const KIND_LONG_FORM = 30023;

export const REPLACEABLE_KINDS = new Set([
  KIND_PROFILE,
  KIND_FOLLOW,
  KIND_RELAY_LIST,
]);

export const DEFAULT_QUERY_LIMIT = 50;
export const MAX_QUERY_LIMIT = 200;
export const MAX_RELAYS = 8;
