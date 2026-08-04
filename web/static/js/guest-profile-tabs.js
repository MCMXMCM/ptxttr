const loaded = new Set(["posts", "identifiers", "relays"]);

function fragmentForInput(input) {
	return String(input?.id || "").replace(/^user-tab-/, "");
}

async function loadPanel(fragment) {
	if (!fragment || loaded.has(fragment)) return;
	const panel = document.querySelector(`#user-panel-${CSS.escape(fragment)}`);
	if (!(panel instanceof HTMLElement)) return;
	const url = new URL(window.location.href);
	url.searchParams.set("fragment", fragment);
	try {
		const response = await fetch(url.pathname + url.search, { headers: { Accept: "text/html" } });
		if (!response.ok) throw new Error(`HTTP ${response.status}`);
		const html = await response.text();
		if (fragment === "replies") {
			const feed = panel.querySelector('[data-profile-feed="replies"]');
			if (!(feed instanceof HTMLElement)) throw new Error("Missing replies feed");
			feed.innerHTML = html.trim() || '<p class="muted">Nothing cached for this tab.</p>';
			const pager = panel.querySelector('[data-load-more][data-fragment="replies"]');
			if (pager instanceof HTMLButtonElement) {
				const hasMore = response.headers.get("X-Ptxt-Has-More") === "1";
				pager.dataset.cursor = response.headers.get("X-Ptxt-Cursor") || "";
				pager.dataset.cursorId = response.headers.get("X-Ptxt-Cursor-Id") || "";
				pager.dataset.hasMore = hasMore ? "1" : "0";
				pager.hidden = !hasMore;
			}
			const { initFeedLoadMore } = await import("./feed.js");
			initFeedLoadMore(panel);
		} else {
			panel.innerHTML = html.trim() || '<p class="muted">Nothing cached for this tab.</p>';
		}
		loaded.add(fragment);
		const { refreshAscii } = await import("./ascii.js");
		refreshAscii(panel);
	} catch {
		panel.innerHTML = '<p class="muted">This tab could not be loaded. <button type="button" data-profile-tab-retry>Retry</button></p>';
		panel.querySelector("[data-profile-tab-retry]")?.addEventListener("click", () => void loadPanel(fragment), { once: true });
	}
}

export function initGuestProfileTabs() {
	// Profile posts are server-rendered, so initialize their scroll sentinel
	// without waiting for a manual Load more click.
	void import("./feed.js").then(({ initFeedLoadMore }) => initFeedLoadMore(document));
	document.querySelectorAll('input[name="user-tab"]').forEach((input) => {
		input.addEventListener("change", () => {
			if (input.checked) void loadPanel(fragmentForInput(input));
		});
	});
	document.querySelectorAll("[data-profile-tab]").forEach((button) => {
		button.addEventListener("click", () => {
			const input = document.getElementById(button.dataset.profileTab || "");
			if (!(input instanceof HTMLInputElement)) return;
			input.checked = true;
			input.dispatchEvent(new Event("change", { bubbles: true }));
		});
	});
}
