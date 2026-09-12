// Hash links, made to work across a route change.
//
// A fragment link is resolved against the CURRENT path, so `href="#how"` from
// /signin navigates to /signin#how — a route with no such section, which looks
// like a dead page. The links therefore carry their path ("/#how"), and this
// does the part the router deliberately does not: move the viewport.
//
// React Router restores no scroll position and honours no fragment, on purpose —
// it cannot know whether the target has rendered yet. So this waits for a frame
// after the route commits, then looks. One retry covers the case where the
// section mounts a beat later; after that, failing quietly is correct, because
// scrolling somewhere arbitrary is worse than not scrolling.

import { useEffect } from "react";
import { useLocation } from "react-router-dom";

export function ScrollToHash() {
  const { pathname, hash } = useLocation();

  useEffect(() => {
    // A new page starts at the top. Without this, following a link from the
    // bottom of a long results list opens the next screen mid-way down.
    if (!hash) {
      // Guarded: scrolling is a nicety, and a host without it (jsdom, some
      // embedded webviews) must not take the screen down with it.
      window.scrollTo?.({ top: 0, behavior: "auto" });
      return;
    }

    const id = decodeURIComponent(hash.slice(1));
    if (!id) return;

    let frame = 0;
    let attempts = 0;

    const settle = () => {
      const target = document.getElementById(id);
      if (target) {
        target.scrollIntoView({ behavior: "smooth", block: "start" });
        // Focus follows the scroll so a keyboard reader lands where the eye
        // does. tabIndex=-1 keeps the section out of the tab order afterwards.
        if (!target.hasAttribute("tabindex")) target.setAttribute("tabindex", "-1");
        target.focus({ preventScroll: true });
        return;
      }
      if (attempts++ < 2) frame = requestAnimationFrame(settle);
    };

    frame = requestAnimationFrame(settle);
    return () => cancelAnimationFrame(frame);
  }, [pathname, hash]);

  return null;
}
