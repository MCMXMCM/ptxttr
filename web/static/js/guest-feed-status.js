import { wireAvatarImageFallbacks } from "./layout.js";
import { initViewMore } from "./notes.js";

const POLL_INTERVAL_MS = 120_000;
const GUEST_FEED_CACHE_PREFIX = "ptxt_guest_feed_dom_v1:";
const GUEST_FEED_CACHE_MAX_AGE_MS = 30 * 60_000;

let checkInFlight = null;
let preparedBatch = null;
let batchPreparation = null;

function isVisibleGuestFeed() {
	return document.visibilityState === "visible" &&
		isGuestFeedRoute();
}

function isGuestFeedRoute() {
	return (window.location.pathname === "/" || window.location.pathname === "/feed") &&
		Boolean(document.body?.dataset?.guestV2);
}

function guestFeedSort() {
	return String(document.querySelector("[data-feed-sort-select]")?.value || "recent");
}

function guestFeedCacheKey() {
	return `${GUEST_FEED_CACHE_PREFIX}${guestFeedSort()}`;
}

function visibleFeedCursor() {
	const notes = [...document.querySelectorAll("#feed .note[id^='note-'][data-created-at]")];
	let cursor = 0;
	let cursorID = "";
	for (const note of notes) {
		const createdAt = Number(note.dataset.createdAt || 0);
		const id = String(note.id || "").replace(/^note-/, "").toLowerCase();
		if (createdAt > cursor || (createdAt === cursor && id > cursorID)) {
			cursor = createdAt;
			cursorID = id;
		}
	}
	return { cursor, cursorID };
}

function cacheVisibleGuestFeed() {
	if (!isGuestFeedRoute()) return false;
	const feed = document.querySelector("#feed[data-feed]");
	if (!(feed instanceof HTMLElement) || !feed.querySelector(".note[id^='note-']")) return false;
	const loadMore = document.querySelector('[data-load-more][data-feed-url="/feed"]');
	try {
		sessionStorage.setItem(guestFeedCacheKey(), JSON.stringify({
			feedHTML: feed.innerHTML,
			loadMoreHTML: loadMore?.outerHTML || "",
			generation: Number(document.body.dataset.visibleGuestGeneration || document.body.dataset.guestGeneration || 0),
			savedAt: Date.now(),
		}));
		return true;
	} catch {
		return false;
	}
}

function restoreCachedGuestFeed() {
	if (!isVisibleGuestFeed()) return false;
	const feed = document.querySelector("#feed[data-feed]");
	if (!(feed instanceof HTMLElement) || !feed.querySelector("[data-feed-loader]")) return false;
	let cached = null;
	try {
		cached = JSON.parse(sessionStorage.getItem(guestFeedCacheKey()) || "null");
	} catch {
		return false;
	}
	if (!cached?.feedHTML || Date.now() - Number(cached.savedAt || 0) > GUEST_FEED_CACHE_MAX_AGE_MS) return false;
	feed.innerHTML = cached.feedHTML;
	const loadMore = document.querySelector('[data-load-more][data-feed-url="/feed"]');
	if (loadMore instanceof HTMLButtonElement && cached.loadMoreHTML) {
		const template = document.createElement("template");
		template.innerHTML = cached.loadMoreHTML;
		const cachedButton = template.content.querySelector('[data-load-more][data-feed-url="/feed"]');
		if (cachedButton instanceof HTMLButtonElement) loadMore.replaceWith(cachedButton);
	}
	document.body.dataset.visibleGuestGeneration = String(Number(cached.generation || 0));
	return true;
}

function preloadImageURL(url) {
	if (!url || typeof Image !== "function") return Promise.resolve();
	return new Promise((resolve) => {
		const image = new Image();
		let settled = false;
		const finish = () => {
			if (settled) return;
			settled = true;
			resolve();
		};
		image.onload = finish;
		image.onerror = finish;
		image.src = url;
		window.setTimeout(finish, 3000);
	});
}

function noteIsNewerThanCursor(note, cursor, cursorID) {
	const createdAt = Number(note?.dataset?.createdAt || 0);
	const id = String(note?.id || "").replace(/^note-/, "").toLowerCase();
	return createdAt > cursor || (createdAt === cursor && Boolean(cursorID) && id > cursorID);
}

async function prepareGuestFeedBatch(status) {
	const generation = Number(status?.generation || 0);
	if (preparedBatch?.generation === generation) return preparedBatch;
	if (batchPreparation?.generation === generation) return batchPreparation.promise;
	const { cursor, cursorID } = visibleFeedCursor();
	const promise = (async () => {
		const documentURL = new URL("/", window.location.origin);
		documentURL.searchParams.set("generation", String(generation));
		const response = await fetch(documentURL, {
			headers: {
				Accept: "text/html",
				"X-Ptxt-Prefetch": "1",
			},
			credentials: "same-origin",
			priority: "low",
		}).catch(() => null);
		if (!response?.ok || !isVisibleGuestFeed()) return null;
		const html = await response.text();
		const parsed = new DOMParser().parseFromString(html, "text/html");
		const notes = [...parsed.querySelectorAll("#feed .note[id^='note-'][data-created-at]")]
			.filter((note) => noteIsNewerThanCursor(note, cursor, cursorID));
		if (!notes.length) return null;
		const avatarURLs = [...new Set(notes.flatMap((note) => (
			[...note.querySelectorAll("img")].map((image) => (
				image.dataset.ptxtAvatarOriginalSrc || image.getAttribute("src") || ""
			))
		)).filter(Boolean))];
		await Promise.all(avatarURLs.map(preloadImageURL));
		const current = visibleFeedCursor();
		if (current.cursor !== cursor || current.cursorID !== cursorID || !isVisibleGuestFeed()) return null;
		preparedBatch = {
			generation,
			cursor,
			cursorID,
			notes,
			loadMore: parsed.querySelector('[data-load-more][data-feed-url="/feed"]'),
		};
		return preparedBatch;
	})();
	batchPreparation = { generation, promise };
	try {
		return await promise;
	} finally {
		if (batchPreparation?.promise === promise) batchPreparation = null;
	}
}

function revealPreparedBatch(button) {
	const batch = preparedBatch;
	const feed = document.querySelector("#feed[data-feed]");
	if (!(feed instanceof HTMLElement) || !batch) return false;
	const current = visibleFeedCursor();
	if (current.cursor !== batch.cursor || current.cursorID !== batch.cursorID) return false;
	const knownIDs = new Set([...feed.querySelectorAll(".note[id^='note-']")].map((note) => note.id));
	const fragment = document.createDocumentFragment();
	for (const note of batch.notes) {
		if (knownIDs.has(note.id)) continue;
		knownIDs.add(note.id);
		fragment.append(document.importNode(note, true));
	}
	const firstNote = feed.querySelector(".note[id^='note-']");
	if (!fragment.childNodes.length) return false;
	if (firstNote) feed.insertBefore(fragment, firstNote);
	else feed.prepend(fragment);
	const currentLoadMore = document.querySelector('[data-load-more][data-feed-url="/feed"]');
	if (currentLoadMore instanceof HTMLButtonElement && batch.loadMore instanceof HTMLButtonElement) {
		currentLoadMore.replaceWith(document.importNode(batch.loadMore, true));
	}
	document.body.dataset.visibleGuestGeneration = String(batch.generation);
	document.body.dataset.guestGeneration = String(batch.generation);
	preparedBatch = null;
	button.hidden = true;
	initViewMore(feed);
	wireAvatarImageFallbacks(feed);
	cacheVisibleGuestFeed();
	window.scrollTo({ top: 0, behavior: "auto" });
	return true;
}

function setNewNotesButton(button, count, generation) {
	const normalizedCount = Math.max(0, Number(count) || 0);
	if (normalizedCount < 1) {
		button.hidden = true;
		return;
	}
	const countNode = document.createElement("span");
	countNode.dataset.newNotesCount = "";
	countNode.textContent = String(normalizedCount);
	button.replaceChildren("Load ", countNode, ` new note${normalizedCount === 1 ? "" : "s"}`);
	button.hidden = false;
	button.onclick = () => {
		if (preparedBatch?.generation === Number(generation) && revealPreparedBatch(button)) return;
		button.hidden = true;
		void prepareGuestFeedBatch({ generation }).then((batch) => {
			if (batch) {
				setNewNotesButton(button, batch.notes.length, batch.generation);
				revealPreparedBatch(button);
			}
		});
	};
}

export async function checkGuestFeedStatus() {
	if (!isVisibleGuestFeed()) return;
	if (guestFeedSort() !== "recent") return;
	if (checkInFlight) return checkInFlight;
	const run = async () => {
		const current = Number(document.body.dataset.visibleGuestGeneration || document.body.dataset.guestGeneration || 0);
		const { cursor, cursorID } = visibleFeedCursor();
		const statusURL = new URL("/api/guest-feed-status", window.location.origin);
		if (cursor > 0) statusURL.searchParams.set("since", String(cursor));
		if (cursorID) statusURL.searchParams.set("since_id", cursorID);
		const response = await fetch(statusURL, {
			headers: { Accept: "application/json" },
			credentials: "same-origin",
		}).catch(() => null);
		if (!response?.ok) return;
		const status = await response.json().catch(() => null);
		if (!status || Number(status.generation || 0) <= current) return;
		const button = document.querySelector("[data-new-notes]");
		if (!(button instanceof HTMLButtonElement)) return;
		if (Number(status.new_count || 0) > 0) {
			// Keep the control hidden until the new document and its identity assets
			// are fully staged. Clicking is then only a DOM commit—never a request.
			button.hidden = true;
			const batch = await prepareGuestFeedBatch(status);
			if (!batch || Number(status.generation || 0) !== batch.generation) return;
			setNewNotesButton(button, batch.notes.length, batch.generation);
		} else {
			setNewNotesButton(button, 0, status.generation);
		}
		if (Number(status.new_count || 0) < 1) {
			document.body.dataset.visibleGuestGeneration = String(Number(status.generation || current));
		}
	};
	checkInFlight = run().finally(() => { checkInFlight = null; });
	return checkInFlight;
}

export function initGuestFeedStatus() {
	if (!document.body?.dataset?.guestV2) return;
	restoreCachedGuestFeed();
	cacheVisibleGuestFeed();
	void checkGuestFeedStatus();
	window.setInterval(() => void checkGuestFeedStatus(), POLL_INTERVAL_MS);
	window.addEventListener("pagehide", cacheVisibleGuestFeed);
	document.addEventListener("click", (event) => {
		const link = event.target?.closest?.("a[data-feed-home]");
		if (!(link instanceof HTMLAnchorElement) || !isVisibleGuestFeed()) return;
		event.preventDefault();
		window.scrollTo({ top: 0, behavior: "auto" });
		void checkGuestFeedStatus();
	});
	document.addEventListener("visibilitychange", () => {
		if (document.visibilityState === "visible") void checkGuestFeedStatus();
	});
}
