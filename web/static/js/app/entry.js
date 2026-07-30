import { initializeAppBootstrap } from "./bootstrap.js";
import { initAppLifecycle } from "./lifecycle.js";

initializeAppBootstrap();
initAppLifecycle();

const feedShell = document.body?.classList?.contains("feed-shell");

if (feedShell) {
	if (document.body?.dataset?.guestV2) {
		void import("../guest-document-prefetch.js").then(({ initGuestDocumentPrefetch }) => initGuestDocumentPrefetch());
		void import("../guest-feed-status.js").then(({ initGuestFeedStatus }) => initGuestFeedStatus());
	}
  const { initRouteLifecycle } = await import("./route-lifecycle.js");
  initRouteLifecycle();
  const { initDocumentRouter } = await import("./document-router.js");
  initDocumentRouter();
} else {
  await Promise.all([
    import("../session.js"),
    import("../notes.js"),
    import("../ascii.js"),
  ]);
}
