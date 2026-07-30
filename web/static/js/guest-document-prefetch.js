const MAX_PREFETCHES = 6;
const prefetched = new Set();

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

export function initGuestDocumentPrefetch() {
	if (!document.body?.dataset?.guestV2 || constrainedConnection()) return;
	if (HTMLScriptElement.supports?.("speculationrules")) {
		const urls = [...document.querySelectorAll("a[href]")]
			.map(eligibleURL).filter(Boolean).slice(0, MAX_PREFETCHES).map((url) => url.pathname + url.search);
		if (urls.length) {
			const script = document.createElement("script");
			script.type = "speculationrules";
			script.textContent = JSON.stringify({ prefetch: [{ source: "list", urls, eagerness: "moderate" }] });
			document.head.appendChild(script);
		}
		return;
	}
	const handler = (event) => prefetch(event.target?.closest?.("a[href]"));
	document.addEventListener("pointerover", handler, { passive: true });
	document.addEventListener("focusin", handler, { passive: true });
}
