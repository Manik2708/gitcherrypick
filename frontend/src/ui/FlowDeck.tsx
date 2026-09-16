// The two walkthroughs share one slot and take turns in it.
//
// They used to be two sections a visitor had to scroll between, which meant the
// hirer half was never seen by anyone who stopped reading at the contributor
// half. Now whichever one is running hands over when its loop completes, and
// the deck slides sideways to the other.
//
// The visitor drives it by dragging the panel sideways. Doing so stops the
// automatic handover for good — a carousel that pulls itself out from under
// someone who has just taken hold of it is worse than one that never moved. The
// walkthrough on screen keeps looping either way; only the jump to the other one
// stops.
//
// The two dots under the panel are the position indicator. They are real
// buttons rather than decoration because dragging is a pointer gesture, and a
// keyboard visitor would otherwise have no way across at all; left/right on the
// focused panel does the same job.
//
// Both slides stay mounted at all times. That is deliberate: the section links
// in the footer point at /#how and /#hirers, and a hash can only find a heading
// the document actually contains. The hash also picks the slide, and counts as
// taking control, so arriving at /#hirers does not slide away a moment later.

import { useCallback, useEffect, useRef, useState } from "react";
import type { PointerEvent as ReactPointerEvent, KeyboardEvent as ReactKeyboardEvent } from "react";
import { useLocation } from "react-router-dom";
import { ClaimFlow } from "./ClaimFlow";
import { HireFlow } from "./HireFlow";

const SLIDES = [
  { id: "how", title: "How a claim becomes a standing", label: "Show the contributor walkthrough" },
  { id: "hirers", title: "For people hiring", label: "Show the hirer walkthrough" },
];

/** Past this much horizontal travel, a release lands on the next slide. */
const THROW = 90;

export function FlowDeck() {
  const [slide, setSlide] = useState(0);
  const [manual, setManual] = useState(false);
  const [drag, setDrag] = useState(0);
  const [dragging, setDragging] = useState(false);
  const startX = useRef(0);
  const viewport = useRef<HTMLDivElement>(null);
  const { hash } = useLocation();

  const go = useCallback((i: number) => {
    setManual(true);
    setSlide(Math.max(0, Math.min(SLIDES.length - 1, i)));
  }, []);

  useEffect(() => {
    const i = SLIDES.findIndex((s) => `#${s.id}` === hash);
    if (i >= 0) go(i);
  }, [hash, go]);

  const handOver = (next: number) => {
    if (!manual) setSlide(next);
  };

  function onDown(e: ReactPointerEvent<HTMLDivElement>) {
    if (e.pointerType === "mouse" && e.button !== 0) return;
    startX.current = e.clientX;
    setDragging(true);
    setDrag(0);
    e.currentTarget.setPointerCapture?.(e.pointerId);
  }

  function onMove(e: ReactPointerEvent<HTMLDivElement>) {
    if (!dragging) return;
    let dx = e.clientX - startX.current;
    // Resist rather than tear off at the ends, so the edge is felt, not hit.
    if ((slide === 0 && dx > 0) || (slide === SLIDES.length - 1 && dx < 0)) dx /= 3;
    setDrag(dx);
  }

  function onUp(e: ReactPointerEvent<HTMLDivElement>) {
    if (!dragging) return;
    const width = viewport.current?.clientWidth ?? 0;
    const throwBy = width ? Math.min(THROW, width * 0.15) : THROW;
    if (drag <= -throwBy) go(slide + 1);
    else if (drag >= throwBy) go(slide - 1);
    setDragging(false);
    setDrag(0);
    if (e.currentTarget.hasPointerCapture?.(e.pointerId)) {
      e.currentTarget.releasePointerCapture?.(e.pointerId);
    }
  }

  function onKey(e: ReactKeyboardEvent<HTMLDivElement>) {
    if (e.key !== "ArrowLeft" && e.key !== "ArrowRight") return;
    e.preventDefault();
    go(slide + (e.key === "ArrowRight" ? 1 : -1));
  }

  return (
    <section className="lp-band deck">
      {/* The scroll targets sit outside the sliding track, so a hash never
          moves the viewport to something translated off screen. */}
      <span className="deck__anchor" id="how" />
      <span className="deck__anchor" id="hirers" />

      <div
        className={`deck__viewport${dragging ? " is-dragging" : ""}`}
        ref={viewport}
        role="group"
        aria-roledescription="carousel"
        aria-label="Walkthroughs"
        tabIndex={0}
        onKeyDown={onKey}
        onPointerDown={onDown}
        onPointerMove={onMove}
        onPointerUp={onUp}
        onPointerCancel={onUp}
      >
        <div
          className={`deck__track${dragging ? " is-dragging" : ""}`}
          style={{ transform: `translateX(calc(${-slide * 100}% + ${drag}px))` }}
        >
          <div className="deck__slide">
            <h2 className="section__title">{SLIDES[0]?.title}</h2>
            <p className="lp-lede">
              Five merged pull requests, judged one at a time against every skill you claim. The
              fifth survivor promotes that skill from secondary to primary, and two numbers fall out
              of it — depth, capped at 100, and breadth, which is not.
            </p>
            <ClaimFlow active={slide === 0} onCycleEnd={() => handOver(1)} />
          </div>

          <div className="deck__slide">
            <h2 className="section__title">{SLIDES[1]?.title}</h2>
            <p className="lp-lede">
              Verified accounts only, because a scorecard names a person. Filter the ranked pool on
              standing, score and recency. A shortlist discloses nothing — confirming cannot be
              undone, and an address reaches you only from someone who said yes.
            </p>
            <HireFlow active={slide === 1} onCycleEnd={() => handOver(0)} />
          </div>
        </div>
      </div>

      <div className="deck__dots">
        {SLIDES.map((sl, i) => (
          <button
            key={sl.id}
            type="button"
            className={`deck__dot${i === slide ? " is-on" : ""}`}
            aria-label={sl.label}
            aria-current={i === slide ? "true" : undefined}
            onClick={() => go(i)}
          />
        ))}
      </div>
    </section>
  );
}
