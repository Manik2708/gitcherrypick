// Shared motion for the two landing-page walkthroughs.
//
// Both are looping, state-driven sequences rather than CSS keyframes: the steps
// have to stay in step with numbers that count, and the global reduced-motion
// rule in styles.css only shortens durations — which would turn either of these
// into a flicker. So reduced motion is handled here instead, by pinning the
// sequence to its finished state and never starting a timer.

import { useEffect, useRef, useState } from "react";

/**
 * What the deck hands a walkthrough that shares a slot: whose turn it is, and
 * how to yield. Both are optional, because a walkthrough standing on its own
 * is always its own turn and has nobody to hand over to.
 */
export type FlowProps = { active?: boolean; onCycleEnd?: () => void };

export function reducedMotion() {
  return (
    typeof window !== "undefined" &&
    window.matchMedia?.("(prefers-reduced-motion: reduce)").matches === true
  );
}

/** Counts 0 → target once `run` goes true. Instant for reduced motion. */
export function useCountUp(target: number, run: boolean, ms = 900) {
  const [n, setN] = useState(0);
  useEffect(() => {
    if (!run) {
      setN(0);
      return;
    }
    if (reducedMotion() || typeof requestAnimationFrame === "undefined") {
      setN(target);
      return;
    }
    let raf = 0;
    const started = performance.now();
    const tick = (now: number) => {
      const p = Math.min(1, (now - started) / ms);
      setN(Math.round(target * (1 - Math.pow(1 - p, 3))));
      if (p < 1) raf = requestAnimationFrame(tick);
    };
    raf = requestAnimationFrame(tick);
    return () => cancelAnimationFrame(raf);
  }, [target, run, ms]);
  return n;
}

/**
 * Walks 0 → hold.length-1, holding each step for hold[step] ms. Runs only while
 * the returned ref is on screen AND `active` is true; on reaching the last step
 * it resets to 0 and calls `onCycleEnd`, which is how the deck knows one
 * walkthrough has finished and it is the other one's turn.
 *
 * `hold` must be module-level so its identity is stable across renders.
 */
export function useFlowTimeline(
  hold: number[],
  { active = true, onCycleEnd }: { active?: boolean; onCycleEnd?: () => void } = {},
) {
  const reduced = reducedMotion();
  const last = hold.length - 1;
  const [step, setStep] = useState(reduced ? last : 0);
  const [live, setLive] = useState(false);
  const host = useRef<HTMLDivElement>(null);

  // Kept in a ref so a new callback identity does not restart the timer.
  const cycleEnd = useRef(onCycleEnd);
  useEffect(() => {
    cycleEnd.current = onCycleEnd;
  });

  useEffect(() => {
    const el = host.current;
    if (!el || reduced) return;
    if (typeof IntersectionObserver === "undefined") {
      setLive(true);
      return;
    }
    const io = new IntersectionObserver((entries) => setLive(entries[0]?.isIntersecting ?? false), {
      threshold: 0.3,
    });
    io.observe(el);
    return () => io.disconnect();
  }, [reduced]);

  // Leaving the deck rewinds, so the next turn replays from the beginning
  // rather than resuming mid-sequence.
  useEffect(() => {
    if (!active && !reduced) setStep(0);
  }, [active, reduced]);

  useEffect(() => {
    if (!live || !active || reduced) return;
    const t = setTimeout(() => {
      if (step >= last) {
        setStep(0);
        cycleEnd.current?.();
      } else {
        setStep(step + 1);
      }
    }, hold[step] ?? 800);
    return () => clearTimeout(t);
  }, [live, active, reduced, step, hold, last]);

  return { step, host };
}
