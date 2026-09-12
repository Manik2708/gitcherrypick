// One pull request that does not survive judging, shown to the hirer who wants
// to know what gets thrown out before they trust a number.
//
// The four dimensions are the highest-weighted of the five in
// evaluation/rubric-v1.md, and the scores are the worked example from
// evaluation/disqualification.md — a typo fix scoring substance 2, complexity 1,
// conversation 0, craft 10. Real numbers from the rubric rather than invented
// ones, because a hirer who checks should find them consistent.

import type { FlowProps } from "./flowMotion";
import { useFlowTimeline } from "./flowMotion";

const PR = {
  repo: "acme/handbook",
  title: "docs: fix “recieve” → “receive”",
  meta: "+1 −1 · no review thread",
};

// The four that carry the most weight; skill_specificity (×0.10) is the fifth.
const JUDGED = [
  { name: "substance", weight: "0.30", score: 2 },
  { name: "complexity", weight: "0.25", score: 1 },
  { name: "conversation", weight: "0.20", score: 0 },
  { name: "craft", weight: "0.15", score: 10 },
];

// 0 idle · 1 the PR · 2-5 each dimension scored · 6 the verdict ·
// 7 what it costs the claim · 8 holds, then the loop restarts.
const HOLD = [700, 850, 480, 480, 480, 620, 1150, 1000, 2600];

export function RulesFlow({ active = true, onCycleEnd }: FlowProps) {
  const { step, host } = useFlowTimeline(HOLD, { active, onCycleEnd });

  const shown = step >= 1;
  const scored = Math.max(0, Math.min(JUDGED.length, step - 1));
  const rejected = step >= 6;
  const cost = step >= 7;

  return (
    <div className="flow" ref={host} aria-hidden="true">
      <div className="flow__panel">
        <p className="flow__cap">a submitted pull request</p>
        <div className={`rule__card${shown ? " is-in" : ""}`}>
          <p className="rule__repo">{PR.repo}</p>
          <p className="rule__title">{PR.title}</p>
          <p className="rule__meta">{PR.meta}</p>
        </div>
      </div>

      <i className="flow__arrow" />

      <div className="flow__panel">
        <p className="flow__cap">judged on four weighted dimensions</p>
        <ul className="rule__dims">
          {JUDGED.map((d, i) => (
            <li key={d.name}>
              <span className="rule__name">
                {d.name} <span className="rule__weight">×{d.weight}</span>
              </span>
              <span className="flow__track">
                <span
                  className="flow__fill flow__fill--low"
                  style={{ width: i < scored ? `${d.score}%` : "0%" }}
                />
              </span>
              <span className={`rule__score${i < scored ? " is-in" : ""}`}>{d.score}</span>
            </li>
          ))}
        </ul>
      </div>

      <i className="flow__arrow" />

      <div className="flow__panel">
        <p className="flow__cap">the verdict</p>
        <p className={`rule__verdict${rejected ? " is-in" : ""}`}>rejected</p>
        <p className={`rule__reason${rejected ? " is-in" : ""}`}>typo_or_wording</p>
        <div className={`rule__cost${cost ? " is-in" : ""}`}>
          <p className="rule__count">4 of 5 surviving</p>
          <p className="rule__badge">Go — secondary, not ranked</p>
          <p className="rule__note">a rejected PR does not count toward the five</p>
        </div>
      </div>
    </div>
  );
}
