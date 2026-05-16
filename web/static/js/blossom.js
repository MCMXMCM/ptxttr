import { getSession } from "./session.js";
import { signEventDraft } from "./signer.js";
import { getBlossomServerURLs } from "./sort-prefs.js";

const BUD_AUTH_KIND = 24242;
const UPLOAD_VERB = "upload";
const AUTH_TTL_SEC = 600;

/** Lowercase hostname for BUD-11 `server` tag (domain only). */
export function blossomServerTagDomain(baseUrl) {
  try {
    return new URL(baseUrl).hostname.toLowerCase();
  } catch {
    return "";
  }
}

function joinUploadURL(base) {
  const root = String(base || "").trim().replace(/\/+$/, "");
  return `${root}/upload`;
}

function base64UrlEncodeJson(obj) {
  const json = JSON.stringify(obj);
  const bytes = new TextEncoder().encode(json);
  let bin = "";
  for (let i = 0; i < bytes.length; i++) bin += String.fromCharCode(bytes[i]);
  const b64 = btoa(bin);
  return b64.replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/u, "");
}

export async function sha256HexOfBlob(blob) {
  const buf = await crypto.subtle.digest("SHA-256", await blob.arrayBuffer());
  return [...new Uint8Array(buf)].map((b) => b.toString(16).padStart(2, "0")).join("");
}

function buildUploadAuthUnsigned({ sha256Hex, serverDomain, nowSec = Math.floor(Date.now() / 1000) }) {
  return {
    kind: BUD_AUTH_KIND,
    created_at: nowSec,
    tags: [
      ["t", UPLOAD_VERB],
      ["expiration", String(nowSec + AUTH_TTL_SEC)],
      ["server", serverDomain],
      ["x", sha256Hex],
    ],
    content: "Upload image",
  };
}

function parseBlobDescriptor(json) {
  if (!json || typeof json !== "object") return null;
  const url = typeof json.url === "string" ? json.url.trim() : "";
  if (!url || !/^https?:\/\//i.test(url)) return null;
  const sha256 = typeof json.sha256 === "string" ? json.sha256.trim() : "";
  const size = typeof json.size === "number" ? json.size : 0;
  const type = typeof json.type === "string" ? json.type.trim() : "application/octet-stream";
  const uploaded = typeof json.uploaded === "number" ? json.uploaded : 0;
  return { url, sha256, size, type, uploaded };
}

function readXReason(response) {
  try {
    return response.headers.get("X-Reason")?.trim() || "";
  } catch {
    return "";
  }
}

/**
 * Upload a single image (or any blob) to a Blossom host (BUD-02 PUT /upload, BUD-11 auth).
 * @returns {{ descriptor: { url: string, sha256: string, type: string }, imetaTag: string[] }}
 */
export async function blossomUploadBlob(file, options = {}) {
  const blob = file instanceof Blob ? file : new Blob([file]);
  const mime = (file && file.type) || blob.type || "application/octet-stream";
  const servers = Array.isArray(options.servers) && options.servers.length > 0 ? options.servers : getBlossomServerURLs();
  const sha256Hex = await sha256HexOfBlob(blob);
  const session = options.session ?? getSession();
  let lastErr = "Upload failed.";

  for (const base of servers) {
    const domain = blossomServerTagDomain(base);
    if (!domain) continue;
    const unsigned = buildUploadAuthUnsigned({ sha256Hex, serverDomain: domain });
    let authJson;
    try {
      authJson = await signEventDraft(unsigned, session);
    } catch (e) {
      lastErr = e instanceof Error ? e.message : String(e);
      break;
    }
    const authHeader = `Nostr ${base64UrlEncodeJson(authJson)}`;
    const uploadUrl = joinUploadURL(base);
    let response;
    try {
      response = await fetch(uploadUrl, {
        method: "PUT",
        headers: {
          Authorization: authHeader,
          "Content-Type": mime,
          "Content-Length": String(blob.size),
          "X-SHA-256": sha256Hex,
        },
        body: blob,
      });
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e);
      lastErr = /failed to fetch|networkerror|load failed|cors/i.test(msg)
        ? "Network error (CORS or offline). Try another Blossom host in Settings."
        : msg;
      continue;
    }
    const reason = readXReason(response);
    if (response.status === 401 || response.status === 403) {
      lastErr = reason || "This host rejected the upload (check account policy or try another host).";
      continue;
    }
    if (response.status === 409) {
      lastErr = reason || "SHA-256 mismatch.";
      break;
    }
    if (!response.ok) {
      lastErr = reason || `HTTP ${response.status} from ${domain}`;
      if (response.status >= 500 || response.status === 429) continue;
      continue;
    }
    let payload;
    try {
      payload = await response.json();
    } catch {
      lastErr = "Invalid JSON from Blossom host.";
      continue;
    }
    const descriptor = parseBlobDescriptor(payload);
    if (!descriptor) {
      lastErr = "Blossom response missing url.";
      continue;
    }
    const imetaParts = [`url ${descriptor.url}`, `m ${descriptor.type || mime}`];
    if (descriptor.sha256) imetaParts.push(`x ${descriptor.sha256}`);
    const imetaTag = ["imeta", ...imetaParts];
    return { descriptor, imetaTag };
  }
  throw new Error(lastErr);
}
