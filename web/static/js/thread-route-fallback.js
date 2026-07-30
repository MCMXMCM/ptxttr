export function relayNativeThreadMissingBundleAction({
  previewRendered = false,
  previewComplete = false,
  serverRendered = false,
} = {}) {
  if (serverRendered || previewRendered || previewComplete) return "keep-rendered";
  return "not-found";
}
