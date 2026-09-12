// The hirer's side of the same story, and the counterpart to ClaimFlow: filter
// the ranked pool, shortlist quietly, confirm once, and receive an address only
// from someone who said yes.
//
// Three details here are load-bearing rather than decorative, because each is a
// rule the backend enforces and a visitor will check us on:
//   · the result ranks are #3 #7 #12 #18 #26 — positions in the whole pool, so
//     a filtered list has gaps, and pretending otherwise would be a lie;
//   · the shortlist column stays grey until confirm, because building one
//     discloses nothing;
//   · one of the three requests is never answered and expires, because silence
//     is not consent.
//
// Decorative in the accessibility sense only: aria-hidden, with the same facts
// carried by the sentence above it in the section.

import type { FlowProps } from "./flowMotion";
import { useCountUp, useFlowTimeline } from "./flowMotion";

const FILTERS = ["skill · Go at primary", "Overall ≥ 80", "evidence under 12 months"];
const POOL = [2481, 412, 96, 5];

const RESULTS = [
  { rank: 3, handle: "maya-ff", overall: 91 },
  { rank: 7, handle: "t-carroll", overall: 88 },
  { rank: 12, handle: "s-ibarra", overall: 85 },
  { rank: 18, handle: "devnull-0", overall: 83 },
  { rank: 26, handle: "q-lindqvist", overall: 81 },
];

// The three that get shortlisted, and how each one answers.
const PICKS = [0, 1, 2];
const ANSWERS = ["accepted", "accepted", "expired"] as const;

// 0 idle · 1-3 a filter lands · 4 results resolve · 5-7 picks join the
// shortlist · 8 shortlist holds, still silent · 9 confirm, one-way ·
// 10-12 answers come back · 13 addresses released, then the loop restarts.
const HOLD = [700, 520, 520, 520, 820, 420, 420, 420, 900, 1200, 560, 560, 700, 2600];

export function HireFlow({ active = true, onCycleEnd }: FlowProps) {
  const { step, host } = useFlowTimeline(HOLD, { active, onCycleEnd });

  const filtersOn = Math.min(FILTERS.length, step);
  const resolved = step >= 4;
  const picked = step >= 5 ? Math.min(PICKS.length, step - 4) : 0;
  const confirmed = step >= 9;
  const answered = Math.max(0, Math.min(ANSWERS.length, step - 9));
  const released = step >= 13;

  const accepted = ANSWERS.slice(0, answered).filter((a) => a === "accepted").length;
  const count = POOL[filtersOn] ?? POOL[0] ?? 0;
  const addresses = useCountUp(accepted, released, 600);

  return (
    <div className="flow" ref={host} aria-hidden="true">
      <div className="flow__panel">
        <p className="flow__cap">filters</p>
        <ul className="hire__filters">
          {FILTERS.map((f, i) => (
            <li key={f} className={`hire__filter${i < filtersOn ? " is-on" : ""}`}>
              {f}
            </li>
          ))}
        </ul>
        <p className="hire__pool">
          <span className="hire__pool-n">{count.toLocaleString("en-GB")}</span>
          <span className="hire__pool-cap">
            {filtersOn === FILTERS.length ? "match every filter" : "in the pool"}
          </span>
        </p>
      </div>

      <i className="flow__arrow" />

      <div className="flow__panel">
        <p className="flow__cap">{resolved ? "ranked results" : "searching"}</p>
        <ol className="hire__results">
          {RESULTS.map((r, i) => (
            <li
              key={r.handle}
              className={`hire__row${resolved ? " is-in" : ""}${i < picked ? " is-picked" : ""}`}
            >
              <span className="hire__rank">#{r.rank}</span>
              <span className="hire__handle">{r.handle}</span>
              <span className="hire__overall">{r.overall}</span>
            </li>
          ))}
        </ol>
        <p className="hire__gap-note">positions are pool-wide — the gaps are real</p>
      </div>

      <i className="flow__arrow" />

      <div className="flow__panel">
        <p className={`flow__badge hire__state${confirmed ? " hire__state--sent" : ""}`}>
          {confirmed
            ? "confirmed — cannot be undone"
            : picked === 0
              ? "shortlist — nobody is told"
              : `${picked} shortlisted — nobody is told`}
        </p>
        <ul className="hire__reqs">
          {PICKS.map((p, i) => {
            const r = RESULTS[p];
            const answer = i < answered ? ANSWERS[i] : null;
            return (
              <li
                key={r?.handle ?? i}
                className={`hire__req${i < picked ? " is-in" : ""}${
                  confirmed ? " is-sent" : ""
                }${answer ? ` is-${answer}` : ""}`}
              >
                <span className="hire__handle">{r?.handle ?? ""}</span>
                <span className="hire__answer">
                  {answer === "accepted"
                    ? "accepted"
                    : answer === "expired"
                      ? "no answer — expired"
                      : confirmed
                        ? "notified"
                        : "not told"}
                </span>
              </li>
            );
          })}
        </ul>
        <p className={`hire__released${released ? " is-in" : ""}`}>
          <span className="hire__released-n">{addresses}</span>
          <span className="hire__released-cap">
            {addresses === 1 ? "address released to you" : "addresses released to you"}
          </span>
        </p>
      </div>
    </div>
  );
}
