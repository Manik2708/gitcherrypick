// The thing the landing page used to spend four prose steps describing: five
// merged pull requests go in, each comes back judged against a claimed skill,
// and the fifth survivor promotes that skill from secondary to primary.
//
// Motion is driven from state rather than CSS keyframes because the sequence
// has to stay in step with numbers that count up, and because a reduced-motion
// visitor should be shown the finished state — not a faster version of the
// journey. The widget is decorative: it is aria-hidden, and the sentence above
// it in the section carries the same facts for anyone who cannot see it.

import type { FlowProps } from "./flowMotion";
import { useCountUp, useFlowTimeline } from "./flowMotion";

const SKILL = "Go";
const DIMS = ["substance", "complexity", "review", "craft", "specificity"];

const PRS = [
  { repo: "kubernetes/kubernetes", title: "scheduler: fix preemption race", score: 84.2, dims: [88, 79, 74, 86, 92] },
  { repo: "grpc/grpc-go", title: "balancer: drain on shutdown", score: 79.6, dims: [76, 82, 71, 80, 88] },
  { repo: "etcd-io/etcd", title: "raft: bound the proposal queue", score: 88.1, dims: [91, 87, 83, 89, 90] },
  { repo: "prometheus/prometheus", title: "tsdb: cut head compaction lock", score: 81.4, dims: [83, 85, 70, 78, 86] },
  { repo: "hashicorp/vault", title: "core: retry seal migration", score: 86.3, dims: [87, 84, 88, 85, 84] },
];

const OVERALL = 82;
const GENERALIST = 137;

// 0 idle · odd = a PR lands · even = it comes back judged · 11 promotes the
// skill · 12 reveals the two user-level numbers, then the loop restarts.
const HOLD = [600, 460, 840, 460, 840, 460, 840, 460, 840, 460, 840, 1300, 2600];

export function ClaimFlow({ active = true, onCycleEnd }: FlowProps) {
  const { step, host } = useFlowTimeline(HOLD, { active, onCycleEnd });

  const landed = Math.min(PRS.length, Math.ceil(step / 2));
  const judged = Math.min(PRS.length, Math.floor(step / 2));
  const focus = Math.max(1, landed);
  const pr = PRS[focus - 1];
  const verdict = step > 0 && judged >= focus;
  const promoted = step >= 11;
  const scored = step >= 12;

  const overall = useCountUp(OVERALL, scored);
  const generalist = useCountUp(GENERALIST, scored);

  return (
    <div className="flow" ref={host} aria-hidden="true">
      <ol className="flow__prs">
        {PRS.map((p, i) => (
          <li
            key={p.repo}
            className={`flow__pr flow__pr--${i < judged ? "done" : i < landed ? "live" : "empty"}`}
          >
            <span className="flow__pr-n">PR {i + 1}</span>
            <span className="flow__pr-body">
              <span className="flow__pr-repo">{p.repo}</span>
              <span className="flow__pr-title">{p.title}</span>
            </span>
            <span className="flow__pr-tick">{i < judged ? "✓" : ""}</span>
          </li>
        ))}
      </ol>

      <i className="flow__arrow" />

      <div className="flow__judge">
        <p className="flow__cap">
          {step === 0 ? "waiting for evidence" : `judging PR ${focus} against ${SKILL}`}
        </p>
        <ul className="flow__dims">
          {DIMS.map((d, i) => (
            <li key={d}>
              <span className="flow__dim-name">{d}</span>
              <span className="flow__track">
                <span
                  className="flow__fill"
                  style={{
                    width: verdict && pr ? `${pr.dims[i] ?? 0}%` : "0%",
                    transitionDelay: `${i * 60}ms`,
                  }}
                />
              </span>
            </li>
          ))}
        </ul>
        <p className={`flow__score${verdict ? " is-in" : ""}`}>
          {verdict && pr ? pr.score.toFixed(1) : "—"}
        </p>
      </div>

      <i className="flow__arrow" />

      <div className="flow__standing">
        <p className={`flow__badge${promoted ? " flow__badge--primary" : ""}`}>
          {SKILL} — {promoted ? "primary" : "secondary"}
        </p>
        <ul className="flow__pips">
          {PRS.map((p, i) => (
            <li
              key={p.repo}
              className={`flow__pip${i < judged ? (promoted ? " is-primary" : " is-part") : ""}`}
            />
          ))}
        </ul>
        <p className="flow__pip-note">
          {promoted ? "ranked and searchable" : `${judged} of 5 surviving`}
        </p>
        <div className={`flow__nums${scored ? " is-in" : ""}`}>
          <p>
            <span className="flow__num">{overall}</span>
            <span className="flow__num-cap">Overall — depth, capped at 100</span>
          </p>
          <p>
            <span className="flow__num flow__num--alt">{generalist}</span>
            <span className="flow__num-cap">Generalist — breadth, unbounded</span>
          </p>
        </div>
      </div>
    </div>
  );
}
