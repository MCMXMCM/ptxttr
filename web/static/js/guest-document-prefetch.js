const MAX_PREFETCHES = 6;
const HOVER_DELAY_MS = 150;
const prefetched = new Set();
let pendingAnchor = null;
let pendingTimer = 0;

// Document rules cover every thread and profile link, including ones added
// after load. Chrome speculates after a short hover (on pointerdown for touch)
// and keeps at most two moderate prefetches at a time.
const SPECULATION_RULES = {
	prefetch: [{
		source: "document",
		where: {
			and: [
				{ or: [{ href_matches: "/thread/*" }, { href_matches: "/u/*" }] },
				{ not: { selector_matches: "[target], [download]" } },
			],
		},
		eagerness: "moderate",
	}],
};

// WebKit 26 accepts the speculationrules script type but never prefetches from
// it, so only browsers with the full Chromium implementation (which also
// exposes document.prerendering) get rules; the rest use the hover fallback.
function speculationRulesHonored() {
	return HTMLScriptElement.supports?.("speculationrules") === true && "prerendering" in document;
}

function constrainedConnection() {
	const connection = navigator.connection || navigator.mozConnection || navigator.webkitConnection;
	if (!connection) return false;
	return Boolean(connection.saveData) || /(^|-)2g$/.test(String(connection.effectiveType || ""));
}

function eligibleURL(anchor) {
	if (!(anchor instanceof HTMLAnchorElement) || anchor.target || anchor.hasAttribute("download")) return null;
	let url;
	try {
		url = new URL(anchor.href, window.location.origin);
	} catch {
		return null;
	}
	if (url.origin !== window.location.origin) return null;
	if (!(url.pathname.startsWith("/thread/") || url.pathname.startsWith("/u/"))) return null;
	url.hash = "";
	return url;
}

function prefetch(anchor) {
	if (constrainedConnection() || prefetched.size >= MAX_PREFETCHES) return;
	const url = eligibleURL(anchor);
	if (!url || prefetched.has(url.href)) return;
	prefetched.add(url.href);
	void fetch(url.href, {
		headers: { Accept: "text/html", "X-Ptxt-Prefetch": "1" },
		credentials: "same-origin",
		priority: "low",
	}).catch(() => {});
}

function cancelPendingPrefetch() {
	window.clearTimeout(pendingTimer);
	pendingTimer = 0;
	pendingAnchor = null;
}

// Without speculation rules, only prefetch a link the pointer or focus rests
// on, so sweeping across a list or tapping (which navigates at once) does not
// spend requests.
function schedulePrefetch(event) {
	const anchor = event.target?.closest?.("a[href]");
	if (anchor === pendingAnchor) return;
	cancelPendingPrefetch();
	if (!eligibleURL(anchor)) return;
	pendingAnchor = anchor;
	pendingTimer = window.setTimeout(() => {
		pendingTimer = 0;
		pendingAnchor = null;
		prefetch(anchor);
	}, HOVER_DELAY_MS);
}

function cancelPrefetchOnLeave(event) {
	if (!pendingAnchor || pendingAnchor.contains(event.relatedTarget)) return;
	cancelPendingPrefetch();
}

export function initGuestDocumentPrefetch() {
	if (!document.body?.dataset?.guestV2 || constrainedConnection()) return;
	if (speculationRulesHonored()) {
		const script = document.createElement("script");
		script.type = "speculationrules";
		script.textContent = JSON.stringify(SPECULATION_RULES);
		document.head.appendChild(script);
		return;
	}
	document.addEventListener("pointerover", schedulePrefetch, { passive: true });
	document.addEventListener("pointerout", cancelPrefetchOnLeave, { passive: true });
	document.addEventListener("focusin", schedulePrefetch, { passive: true });
	document.addEventListener("focusout", cancelPrefetchOnLeave, { passive: true });
}
