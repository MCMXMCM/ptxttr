import { displayName } from "./profile-parse.js";
import { normalizePubkey, profilePath } from "./relay-utils.js";
import { createElement, createLink } from "./render-utils.js";

export function briefBioText(about, maxWords = 12) {
  const words = String(about || "").trim().split(/\s+/).filter(Boolean);
  if (!words.length) return "";
  if (!Number.isFinite(maxWords) || maxWords <= 0 || words.length <= maxWords) {
    return words.join(" ");
  }
  return `${words.slice(0, maxWords).join(" ")}...`;
}

function appendThreadAge(head, href, ageText) {
  const age = createElement("span", { className: "hn-age muted" });
  age.append(" ");
  age.append(createLink(href, ageText, { dataset: { relayAware: "" } }));
  head.append(age);
}

export function createThreadComhead(profile, pubkey, noteHref, ageText, options = {}) {
  const {
    className = "hn-comhead",
    collapseId = "",
  } = options;
  const head = createElement("div", { className });
  head.append(
    createLink(profilePath(normalizePubkey(pubkey)), displayName(profile), {
      dataset: { relayAware: "" },
    }),
  );
  appendThreadAge(head, noteHref, ageText);
  if (collapseId) {
    const navs = createElement("span", { className: "hn-navs hn-collapse-nav" });
    navs.append(" ");
    navs.append(
      createElement("button", {
        className: "link-button",
        text: "[-]",
        attrs: {
          type: "button",
          "data-thread-tree-collapse": collapseId,
          "aria-expanded": "true",
        },
      }),
    );
    head.append(navs);
  }
  return head;
}

export function createThreadReplyLink(rootID) {
  const reply = createElement("div", { className: "hn-reply" });
  const font = createElement("font", { className: "hn-reply-font" });
  const underline = document.createElement("u");
  underline.append(createLink(`/thread/${rootID}#note-${rootID}`, "reply", { dataset: { relayAware: "" } }));
  font.append(underline);
  reply.append(font);
  return reply;
}

export function createThreadParticipantMeta(profile) {
  const meta = createElement("div", { className: "thread-person-meta" });
  meta.append(createElement("strong", { text: displayName(profile) }));
  const about = briefBioText(profile?.about);
  if (about) {
    meta.append(createElement("span", {
      className: "muted thread-person-about",
      text: about,
    }));
  }
  return meta;
}
