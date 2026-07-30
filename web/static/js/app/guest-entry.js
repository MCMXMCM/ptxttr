import "../layout.js";
import { initViewMore } from "../notes.js";
import { initGuestDocumentPrefetch } from "../guest-document-prefetch.js";
import { initGuestFeedStatus } from "../guest-feed-status.js";

initGuestFeedStatus();
initViewMore(document);
initGuestDocumentPrefetch();

// Keep the large transition/router module out of anonymous documents while
// preserving the familiar "click anywhere on the note" behavior before the
// deferred ASCII enhancement runs.
document.addEventListener("click", (event) => {
	if (event.defaultPrevented || (typeof event.button === "number" && event.button !== 0) ||
		event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
	const target = event.target;
	if (!(target instanceof Element) || target.closest("a, button, input, select, textarea, label, video, audio, [role='button'], [contenteditable='true']")) return;
	const reference = target.closest("[data-ascii-ref-select-href]");
	const card = reference || target.closest(".note[data-ascii-select-href], .comment[data-ascii-select-href]");
	if (!(card instanceof HTMLElement)) return;
	const href = reference?.getAttribute("data-ascii-ref-select-href") || card.dataset.asciiSelectHref;
	if (!href || !href.startsWith("/thread/")) return;
	event.preventDefault();
	window.location.assign(href);
});

const enhanceASCII = () => void import("../ascii.js");
if (typeof requestIdleCallback === "function") {
	requestIdleCallback(enhanceASCII, { timeout: 5000 });
} else {
	window.setTimeout(enhanceASCII, 3000);
}

let feedModuleLoaded = false;
document.addEventListener("click", (event) => {
	const button = event.target?.closest?.("[data-load-more]");
	if (!(button instanceof HTMLButtonElement) || feedModuleLoaded) return;
	event.preventDefault();
	event.stopImmediatePropagation();
	void import("../feed.js").then(() => {
		feedModuleLoaded = true;
		button.click();
	});
}, true);

if (document.querySelector("[data-profile-shell]")) {
	void import("../guest-profile-tabs.js").then(({ initGuestProfileTabs }) => initGuestProfileTabs());
}

if (window.location.pathname.startsWith("/thread/")) {
	let threadInteractionsLoaded = false;
	document.addEventListener("click", (event) => {
		if (threadInteractionsLoaded) return;
		const control = event.target?.closest?.([
			"[data-thread-load-more]",
			"[data-thread-view-toggle]",
			"[data-thread-hidden-toggle]",
			"[data-thread-filtered-replies-toggle]",
			"[data-thread-tree-filtered-replies-toggle]",
			"[data-thread-other-replies-toggle]",
			"[data-thread-tree-media-toggle]",
			"[data-thread-tree-collapse]",
			"[data-reply-action]",
		].join(","));
		if (!(control instanceof HTMLElement)) return;
		event.preventDefault();
		event.stopImmediatePropagation();
		void import("../thread.js").then(({ initThreadPage }) => {
			threadInteractionsLoaded = true;
			initThreadPage();
			control.click();
		});
	}, true);
}
