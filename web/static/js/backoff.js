/** Shared cap for client-side transient retries (HTTP publish, NIP-07 sign). */
export const DEFAULT_RETRY_ATTEMPTS = 4;

/** Exponential backoff with jitter: baseMs * 2^attempt + random in [0, jitterMs). */
export async function sleepBackoff(attempt, baseMs, jitterMs) {
  const ms = baseMs * (2 ** attempt) + Math.random() * jitterMs;
  await new Promise((resolve) => setTimeout(resolve, ms));
}
