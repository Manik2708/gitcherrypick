// jsdom implements no scrolling, so anything that moves the viewport logs a
// "Not implemented" error from the virtual console. Stubbing it keeps the
// output honest: a real failure stands out instead of hiding in the noise.

import { vi } from "vitest";

window.scrollTo = vi.fn() as unknown as typeof window.scrollTo;
Element.prototype.scrollIntoView = vi.fn();
