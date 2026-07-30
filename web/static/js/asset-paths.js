function documentAssetBasePath() {
  return document?.documentElement?.dataset?.assetBase || "/static";
}

export function assetBasePath() {
  return documentAssetBasePath().replace(/\/+$/, "");
}

export function assetURL(relPath) {
  const trimmed = String(relPath || "").replace(/^\/+/, "");
  if (!trimmed) return assetBasePath();
  return `${assetBasePath()}/${trimmed}`;
}
