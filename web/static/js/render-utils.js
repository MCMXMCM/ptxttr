export function escapeHTML(value) {
  return String(value || "").replace(/[&<>"']/g, (ch) => ({
    "&": "&amp;",
    "<": "&lt;",
    ">": "&gt;",
    '"': "&quot;",
    "'": "&#39;",
  })[ch]);
}

export function createElement(tagName, { className = "", text = null, attrs = null, dataset = null } = {}) {
  const node = document.createElement(tagName);
  if (className) node.className = className;
  if (text != null) node.textContent = String(text);
  if (attrs && typeof attrs === "object") {
    Object.entries(attrs).forEach(([key, value]) => {
      if (value == null) return;
      node.setAttribute(key, String(value));
    });
  }
  if (dataset && typeof dataset === "object") {
    Object.entries(dataset).forEach(([key, value]) => {
      if (value == null) return;
      node.dataset[key] = String(value);
    });
  }
  return node;
}

export function createLink(href, text, { className = "", attrs = null, dataset = null } = {}) {
  return createElement("a", {
    className,
    text,
    attrs: { href, ...(attrs || {}) },
    dataset,
  });
}

export function trustedHTMLFragment(html) {
  const template = document.createElement("template");
  template.innerHTML = String(html || "").trim();
  return template.content;
}
