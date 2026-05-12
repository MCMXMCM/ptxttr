/**
 * Column counts must match internal/httpx/service.go (42 / 64 / 120, and 82 for
 * profile main column on large viewports). Breakpoints align with app.css
 * mobile (700px) and narrow three-column shell (1023px).
 */
export function asciiWidthHintForFetch(pathname) {
  const vw = window.innerWidth;
  let w;
  if (vw <= 700) {
    w = 42;
  } else if (vw <= 1023) {
    w = 64;
  } else {
    w = 120;
  }
  if (pathname.startsWith("/u/") && w === 120) {
    return 82;
  }
  return w;
}

export function addAsciiWidthHint(params, pathname) {
  if (!(params instanceof URLSearchParams)) return;
  params.set("ascii_w", String(asciiWidthHintForFetch(pathname)));
}
