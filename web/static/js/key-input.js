import { nip19 } from "../lib/nostr-tools.js";

export function pubkeyFromInput(value) {
  value = String(value || "").trim();
  if (value.startsWith("npub")) {
    return nip19.decode(value).data;
  }
  if (/^[0-9a-fA-F]{64}$/.test(value)) {
    return value.toLowerCase();
  }
  throw new Error("Expected npub or 64-character hex public key");
}

export function secretFromInput(value) {
  value = String(value || "").trim();
  if (value.startsWith("nsec")) {
    return nip19.decode(value).data;
  }
  if (/^[0-9a-fA-F]{64}$/.test(value)) {
    return Uint8Array.from(value.match(/.{1,2}/g).map((byte) => Number.parseInt(byte, 16)));
  }
  throw new Error("Expected nsec or 64-character hex private key");
}
