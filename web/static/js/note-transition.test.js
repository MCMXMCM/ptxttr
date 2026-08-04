import assert from "node:assert/strict";
import { describe, it } from "node:test";

class FakeElement {
  constructor(id = "", queryMap = null) {
    this.id = id;
    this.isConnected = true;
    this.dataset = {};
    this.style = {};
    this.queryMap = queryMap;
    this.attributes = {};
    this.parent = null;
    const classSet = new Set();
    this.classList = {
      add: (...names) => names.forEach((name) => classSet.add(name)),
      remove: (...names) => names.forEach((name) => classSet.delete(name)),
      contains: (name) => classSet.has(name),
    };
  }

  closest(selector) {
    if (selector === ".note, .comment" || selector === ".note, .comment, [data-thread-tree-note]") return this;
    if (selector === "[data-profile-relays]") return this.parent;
    return null;
  }

  querySelector(selector) {
    if (this.queryMap) {
      const key = selector;
      if (Object.prototype.hasOwnProperty.call(this.queryMap, key)) {
        return this.queryMap[key];
      }
    }
    return null;
  }

  getAttribute(name) {
    return Object.prototype.hasOwnProperty.call(this.attributes, name) ? this.attributes[name] : null;
  }

  setAttribute(name, value) {
    this.attributes[name] = String(value);
  }

  cloneNode() {
    const queryMap = this.queryMap
      ? Object.fromEntries(Object.entries(this.queryMap).map(([key, value]) => [
        key,
        value instanceof FakeElement ? value.cloneNode(true) : value,
      ]))
      : this.queryMap;
    const copy = new FakeElement(this.id, queryMap);
    copy.dataset = { ...this.dataset };
    copy.attributes = { ...this.attributes };
    copy.parent = this.parent;
    return copy;
  }
}

function transitionableNote(id = "note-abc", extras = {}) {
  const body = extras.body || new FakeElement(`${id}-body`);
  const content = extras.content || new FakeElement(`${id}-content`);
  const actions = extras.actions || new FakeElement(`${id}-actions`);
  body.queryMap = {
    ".note-content, .ascii-note-content, .reply-content": content,
    ".ascii-line:has([data-reply-action])": actions,
    ...(body.queryMap || {}),
  };
  const author = extras.author || new FakeElement(`${id}-author`);
  const avatarImg = extras.avatarImg || new FakeElement(`${id}-avatar-img`);
  const avatarHost = extras.avatarHost || new FakeElement(`${id}-avatar-host`, { img: avatarImg });
  const note = new FakeElement(id, {
    ":scope > .note-avatar, :scope > .comment-avatar, :scope .note-feed-avatar": avatarHost,
    ":scope > pre.ascii-card, :scope > pre.ascii-reply": body,
    ":scope .ascii-line-feed-header a[href^='/u/'], :scope .ascii-reply > .ascii-line:first-child a[href^='/u/'], :scope .hn-comhead a[href^='/u/']": author,
    ...(extras.queryMap || {}),
  });
  return { note, body, content, actions, author, avatarHost, avatarImg };
}

globalThis.Element = FakeElement;
globalThis.HTMLElement = FakeElement;
globalThis.window ??= {};
globalThis.window.location = {
  pathname: "/",
  href: "http://localhost/",
  origin: "http://localhost",
};
globalThis.window.addEventListener ??= () => {};
globalThis.window.removeEventListener ??= () => {};
const storage = new Map();
const storageAPI = {
  getItem(key) {
    return storage.has(key) ? storage.get(key) : null;
  },
  setItem(key, value) {
    storage.set(key, String(value));
  },
  removeItem(key) {
    storage.delete(key);
  },
};
globalThis.localStorage ??= storageAPI;
globalThis.sessionStorage ??= storageAPI;
globalThis.document ??= {
  documentElement: {
    classList: {
      add() {},
      remove() {},
    },
  },
  addEventListener() {},
  removeEventListener() {},
  querySelector() {
    return null;
  },
  querySelectorAll() {
    return [];
  },
};

const {
  prepareThreadTransition,
  prepareThreadFocusTransition,
  prepareThreadToProfileTransition,
  runNoteViewTransition,
  takeCarriedThreadNote,
  clearThreadTransition,
  clearNoteTransitionNames,
  applyDestinationThreadTransition,
  applyDestinationProfileTransition,
  applyThreadTransitionNames,
} = await import("./note-transition.js");

describe("note-transition carried note handling", () => {
  it("applies lightweight transition names to note parts", () => {
    const { note: source, avatarImg, avatarHost, body, content, actions, author } = transitionableNote("note-abc");

    const transition = prepareThreadTransition(source, "/thread/abc");
    assert.equal(transition?.selectedNoteID, "abc");
    assert.equal(source.style.viewTransitionName, undefined);
    assert.equal(avatarImg.style.viewTransitionName, "ptxt-note-avatar-abc");
    assert.equal(avatarHost.style.viewTransitionName, undefined);
    assert.equal(author.style.viewTransitionName, "ptxt-note-author-abc");
    assert.equal(body.style.viewTransitionName, "ptxt-note-chrome-abc");
    assert.equal(content.style.viewTransitionName, "ptxt-note-content-abc");
    assert.equal(actions.style.viewTransitionName, "ptxt-note-actions-abc");

    clearThreadTransition("abc");
  });

  it("clones the carried note instead of removing the source feed card", () => {
    const source = new FakeElement("note-abc");

    const transition = prepareThreadTransition(source, "/thread/abc");
    assert.equal(transition?.selectedNoteID, "abc");

    const carried = takeCarriedThreadNote("abc");
    assert.ok(carried instanceof FakeElement);
    assert.notEqual(carried, source);
    assert.equal(source.isConnected, true);

    clearThreadTransition("abc");
  });

  it("clears source transition names without touching the carried clone", () => {
    const { note: source, body: sourceBody } = transitionableNote("note-abc");
    prepareThreadTransition(source, "/thread/abc");
    const carried = takeCarriedThreadNote("abc");
    applyThreadTransitionNames(carried, "abc");
    const carriedBody = carried.querySelector(":scope > pre.ascii-card, :scope > pre.ascii-reply");
    assert.equal(source.style.viewTransitionName, undefined);
    assert.equal(sourceBody.style.viewTransitionName, "ptxt-note-chrome-abc");
    assert.equal(carriedBody.style.viewTransitionName, "ptxt-note-chrome-abc");

    clearNoteTransitionNames(source);

    assert.equal(sourceBody.style.viewTransitionName, "");
    assert.equal(sourceBody.dataset.ptxtViewTransitionName, undefined);
    assert.equal(carriedBody.style.viewTransitionName, "ptxt-note-chrome-abc");

    clearThreadTransition("abc");
  });

  it("carries profile relay hints into thread transitions", () => {
    const profileShell = new FakeElement("profile-shell");
    profileShell.setAttribute("data-profile-relays", "wss://profile.example,wss://backup.example");
    const source = new FakeElement("note-abc");
    source.setAttribute("data-ascii-relay", "wss://note.example");
    source.parent = profileShell;

    const transition = prepareThreadTransition(source, "/thread/abc");
    assert.deepEqual(transition?.relayHints, [
      "wss://note.example",
      "wss://profile.example",
      "wss://backup.example",
    ]);

    clearThreadTransition("abc");
  });

  it("skips feed carry-over transitions when the source route is already thread", () => {
    globalThis.window.location.pathname = "/thread/root123";
    const source = new FakeElement("note-reply123");

    const transition = prepareThreadTransition(source, "/thread/reply123");
    assert.equal(transition, null);
    assert.equal(takeCarriedThreadNote("reply123"), null);

    globalThis.window.location.pathname = "/";
  });

  it("names both notes during an in-thread focus promotion and demotion", () => {
    globalThis.window.location.pathname = "/thread/root";
    const { note: parent, body: parentChrome } = transitionableNote("note-parent");
    const { note: selectedReply, body: replyChrome } = transitionableNote("note-reply");
    const root = {
      querySelector(selector) {
        return String(selector).includes("#thread-focus") ? selectedReply : null;
      },
    };

    const transition = prepareThreadFocusTransition(parent, "/thread/parent", root);
    assert.equal(transition?.selectedNoteID, "parent");
    assert.equal(transition?.previousSelectedNoteID, "reply");
    assert.deepEqual(transition?.noteIDs, ["parent", "reply"]);
    assert.equal(parentChrome.style.viewTransitionName, "ptxt-note-chrome-parent");
    assert.equal(replyChrome.style.viewTransitionName, "ptxt-note-chrome-reply");

    clearThreadTransition("parent");
    globalThis.window.location.pathname = "/";
  });

  it("moves the promoted note into focus and the prior selection into replies", () => {
    globalThis.window.location.pathname = "/thread/root";
    const { note: parent } = transitionableNote("note-parent");
    const { note: selectedReply } = transitionableNote("note-reply");
    const { note: focusedParent, body: focusedChrome } = transitionableNote("note-parent");
    const { note: demotedReply, body: demotedChrome } = transitionableNote("note-reply");
    const sourceRoot = {
      querySelector(selector) {
        return String(selector).includes("#thread-focus") ? selectedReply : null;
      },
    };
    prepareThreadFocusTransition(parent, "/thread/parent", sourceRoot);
    const destinationRoot = {
      querySelector(selector) {
        const value = String(selector);
        if (value.includes("#thread-focus") && value.includes("note-parent")) return focusedParent;
        if (value.includes("#thread-replies") && value.includes("note-reply")) return demotedReply;
        return null;
      },
      querySelectorAll() {
        return [];
      },
    };

    const destination = applyDestinationThreadTransition(destinationRoot, "parent");
    assert.equal(destination, focusedParent);
    assert.equal(focusedChrome.style.viewTransitionName, "ptxt-note-chrome-parent");
    assert.equal(demotedChrome.style.viewTransitionName, "ptxt-note-chrome-reply");

    clearThreadTransition("parent");
    globalThis.window.location.pathname = "/";
  });

  it("keeps the current child paired as the parent when focusing its child", () => {
    globalThis.window.location.pathname = "/thread/root";
    globalThis.window.location.href = "http://localhost/thread/parent";
    const { note: child } = transitionableNote("note-child");
    const { note: currentParent } = transitionableNote("note-parent");
    const { note: focusedChild, body: childChrome } = transitionableNote("note-child");
    const { note: demotedParent, body: parentChrome } = transitionableNote("note-parent");
    const sourceRoot = {
      querySelector(selector) {
        return String(selector).includes("#thread-focus") ? currentParent : null;
      },
    };
    prepareThreadFocusTransition(child, "/thread/child", sourceRoot);
    const destinationRoot = {
      querySelector(selector) {
        const value = String(selector);
        if (value.includes("#thread-focus") && value.includes("note-child")) return focusedChild;
        if (value.includes("#thread-focus") && value.includes("note-parent")) return demotedParent;
        return null;
      },
      querySelectorAll() {
        return [];
      },
    };

    const destination = applyDestinationThreadTransition(destinationRoot, "child");
    assert.equal(destination, focusedChild);
    assert.equal(childChrome.style.viewTransitionName, "ptxt-note-chrome-child");
    assert.equal(parentChrome.style.viewTransitionName, "ptxt-note-chrome-parent");

    clearThreadTransition("child");
    globalThis.window.location.pathname = "/";
    globalThis.window.location.href = "http://localhost/";
  });

  it("skips carried-note transitions for thread links outside note cards", () => {
    const link = new FakeElement("plain-link");
    link.closest = (selector) => {
      if (selector === ".note, .comment, [data-thread-tree-note]") return null;
      if (selector === "[data-profile-relays]") return null;
      return null;
    };

    const transition = prepareThreadTransition(link, "/thread/abc");
    assert.equal(transition, null);
    assert.equal(takeCarriedThreadNote("abc"), null);
  });

  it("names destination thread notes for view transitions", () => {
    const feedNote = new FakeElement("note-abc");
    const { note: threadNote, body } = transitionableNote("note-abc");
    globalThis.document.querySelector = (selector) => {
      const sel = String(selector || "");
      if (sel.includes("#thread-focus") && sel.includes("note-abc")) return threadNote;
      if (selector === "#note-abc") return feedNote;
      return null;
    };

    const matched = applyDestinationThreadTransition(globalThis.document, "abc");
    assert.equal(matched, threadNote);
    assert.equal(threadNote.style.viewTransitionName, undefined);
    assert.equal(body.style.viewTransitionName, "ptxt-note-chrome-abc");
    assert.equal(feedNote.style.viewTransitionName, undefined);

    clearThreadTransition("abc");
  });

  it("prefers the focused thread destination over the feed copy", () => {
    const feedNote = new FakeElement("note-abc");
    const { note: threadNote, body } = transitionableNote("note-abc");
    globalThis.document.querySelector = (selector) => {
      const sel = String(selector || "");
      if (sel.includes("#thread-focus") && sel.includes("note-abc")) return threadNote;
      if (selector === "#note-abc") return feedNote;
      return null;
    };

    const matched = applyDestinationThreadTransition(globalThis.document, "abc");
    assert.equal(matched, threadNote);
    assert.equal(threadNote.style.viewTransitionName, undefined);
    assert.equal(body.style.viewTransitionName, "ptxt-note-chrome-abc");
    assert.equal(feedNote.style.viewTransitionName, undefined);

    clearThreadTransition("abc");
  });

  it("captures the focused thread note when preparing a profile transition", () => {
    globalThis.window.location.pathname = "/thread/abc";
    const { note: threadNote, body } = transitionableNote("note-abc");
    globalThis.document.querySelector = (selector) => {
      if (String(selector || "").includes("#thread-focus")) return threadNote;
      return null;
    };

    const transition = prepareThreadToProfileTransition(globalThis.document, `/u/author`);
    assert.equal(transition?.selectedNoteID, "abc");
    assert.equal(transition?.sourceRoute, "thread");
    assert.equal(threadNote.style.viewTransitionName, undefined);
    assert.equal(body.style.viewTransitionName, "ptxt-note-chrome-abc");

    clearThreadTransition("abc");
    globalThis.window.location.pathname = "/";
  });

  it("captures a feed note when preparing a feed to profile transition", () => {
    globalThis.window.location.pathname = "/feed";
    const { note: feedNote, body } = transitionableNote("note-abc");

    const transition = prepareThreadToProfileTransition(feedNote, `/u/author`);
    assert.equal(transition?.selectedNoteID, "abc");
    assert.equal(transition?.sourceRoute, "feed");
    assert.equal(transition?.sourceList, "feed");
    assert.equal(feedNote.style.viewTransitionName, undefined);
    assert.equal(body.style.viewTransitionName, "ptxt-note-chrome-abc");

    const carried = takeCarriedThreadNote("abc");
    assert.notEqual(carried, null);

    clearThreadTransition("abc");
    globalThis.window.location.pathname = "/";
  });

  it("names destination profile posts for view transitions", () => {
    const threadNote = new FakeElement("note-abc");
    const { note: profileNote, body } = transitionableNote("note-abc");
    globalThis.document.querySelector = (selector) => {
      const sel = String(selector || "");
      if (sel.includes("#user-panel-posts") && sel.includes("note-abc")) return profileNote;
      if (sel.includes("#thread-focus") && sel.includes("note-abc")) return threadNote;
      return null;
    };
    globalThis.document.querySelectorAll = (selector) => {
      const sel = String(selector || "");
      if (sel.includes("#thread-focus") && sel.includes("note-abc")) return [threadNote];
      return [];
    };

    const matched = applyDestinationProfileTransition(globalThis.document, "abc");
    assert.equal(matched, profileNote);
    assert.equal(profileNote.style.viewTransitionName, undefined);
    assert.equal(body.style.viewTransitionName, "ptxt-note-chrome-abc");
    assert.equal(threadNote.style.viewTransitionName, undefined);

    clearThreadTransition("abc");
  });

  it("keeps the view transition open until async navigation work finishes", async () => {
    const calls = [];
    globalThis.document.startViewTransition = (callback) => {
      const updateCallbackDone = Promise.resolve().then(() => callback());
      return {
        ready: Promise.resolve(),
        updateCallbackDone,
        finished: updateCallbackDone,
      };
    };

    await runNoteViewTransition({ sharedElement: true }, async () => {
      calls.push("update:start");
      await Promise.resolve();
      calls.push("update:done");
    });

    assert.deepEqual(calls, ["update:start", "update:done"]);
  });

  it("can start the transition before async navigation work settles", async () => {
    const calls = [];
    let resolveUpdate;
    const pendingUpdate = new Promise((resolve) => {
      resolveUpdate = resolve;
    });
    globalThis.document.startViewTransition = (callback) => {
      callback();
      calls.push("transition:callback");
      return {
        ready: Promise.resolve(),
        updateCallbackDone: Promise.resolve(),
        finished: Promise.resolve().then(() => {
          calls.push("transition:finished");
        }),
      };
    };

    const work = runNoteViewTransition(
      { sharedElement: true },
      async () => {
        calls.push("update:start");
        await pendingUpdate;
        calls.push("update:done");
      },
      { awaitUpdate: false },
    );
    calls.push("after:run");
    resolveUpdate();
    await work;

    assert.deepEqual(calls, [
      "update:start",
      "transition:callback",
      "after:run",
      "transition:finished",
      "update:done",
    ]);
  });
});
