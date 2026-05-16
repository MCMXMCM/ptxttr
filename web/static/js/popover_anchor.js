/** Fixed positioning for floating panels anchored to a DOM rect (relay info, login-required follow, etc.). */
export function positionPopoverNearAnchor(anchor, pop, options = {}) {
  const margin = options.margin ?? 8;
  const cap = options.maxWidth ?? 360;
  const fallbackHeight = options.fallbackHeight ?? 48;
  const maxW = Math.min(cap, window.innerWidth - 2 * margin);
  pop.style.maxWidth = `${maxW}px`;
  void pop.offsetWidth;
  const rect = anchor.getBoundingClientRect();
  const ph = pop.offsetHeight || fallbackHeight;
  let top = rect.bottom + margin;
  if (top + ph > window.innerHeight - margin && rect.top - ph - margin > margin) {
    top = rect.top - ph - margin;
  }
  top = Math.max(margin, Math.min(top, window.innerHeight - ph - margin));
  let left = rect.left;
  if (left + maxW > window.innerWidth - margin) {
    left = window.innerWidth - maxW - margin;
  }
  left = Math.max(margin, left);
  pop.style.setProperty("position", "fixed");
  pop.style.setProperty("top", `${top}px`);
  pop.style.setProperty("left", `${left}px`);
}
