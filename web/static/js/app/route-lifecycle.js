import { stopFeedPolling, stopProfilePostsPolling } from "../route-polling.js";
import { teardownThreadTreeConnector } from "../thread.js";

export function initRouteLifecycle() {
  document.addEventListener("page:load", (event) => {
    const { page } = event.detail || {};
    if (page !== "feed") stopFeedPolling();
    if (page !== "profile") stopProfilePostsPolling();
    if (page !== "thread") teardownThreadTreeConnector();
  });
}
