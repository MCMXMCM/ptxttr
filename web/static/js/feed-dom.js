export function homeFeedElement(root) {
  return root?.querySelector("#feed[data-feed]") || root?.querySelector(".feed-column [data-feed]");
}
