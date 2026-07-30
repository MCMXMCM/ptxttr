import { shouldShowFirstLoginBootstrap } from "./first-login-bootstrap.js";

export function shouldPreserveDeferredFeedLoader(feed, notes, viewer = "") {
  if ((notes?.length || 0) > 0) return false;
  if (!feed?.querySelector?.("[data-feed-loader]")) return false;
  const normalizedViewer = String(viewer || "").trim().toLowerCase();
  if (!normalizedViewer) return true;
  return shouldShowFirstLoginBootstrap(normalizedViewer);
}
