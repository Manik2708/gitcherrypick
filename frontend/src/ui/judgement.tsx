// The verdict, rendered.
//
// This is the screen the platform exists for. A contributor is looking at what
// their evidence was judged to be worth, and the number alone is not an answer —
// the dimension scores and the model's remark are the reasoning, and they are
// what makes a judgement arguable rather than merely announced (ADR-0007 §6).
// So the remark is body text and the score is a label beside it, never the
// other way round.
//
// A dropped PR says so, and says why. Silently omitting it would leave someone
// comparing five submitted against four scored with nothing to explain the gap.

import type { ReactNode } from "react";
import { useState } from "react";
import type { ClaimSkill, Dimension, JudgedPR, PREvidence, Suggestion } from "../contract";
import {
  bestScore,
  dimensionLabel,
  isJudged,
  orderedDimensions,
  rejectionReason,
} from "../logic/claims";
import * as format from "../logic/format";
import { Button, Card, Empty, Icon, Pill } from "../ui/primitives";

/** A dimension's score as a filled ring with the number inside it.
 *
 * A bar plus a separate number said the same thing twice and needed two
 * columns to do it. The ring carries both: how full it is IS the score.
 *
 * The old bar multiplied the score by ten before clamping to 100 — dimensions
 * are scored 0-100 (ADR-0005), so every bar rendered full regardless of the
 * verdict. The ring uses the score as the percentage it is.
 */
export function ScoreDial({ score }: { score: number }) {
  const pct = Math.max(0, Math.min(100, score));
  return (
    <div className="dial" style={{ ["--dial" as string]: `${pct}` }}>
      <span className="dial__n mono">{Math.round(score)}</span>
    </div>
  );
}

function DimensionRow({ name, value }: { name: string; value: Dimension }) {
  return (
    <li className="dim">
      <div className="dim__label">{dimensionLabel(name)}</div>
      <ScoreDial score={value.score} />
      <p className="rationale">{value.remark}</p>
    </li>
  );
}

/** One judged pull request: its score always, its reasoning on request.
 *
 * Shared by the contributor's claim detail and the hirer's scorecard, which
 * had grown two different rows for the same thing — one always-expanded with
 * no padding (dials clipped at the card edge), one collapsible with cards.
 * Same evidence, same control.
 *
 * The toggle and the action are SIBLINGS, not nested: a button inside a button
 * is invalid, and clicking "Dispute" must not also expand the row.
 */
export function EvidenceRow({
  title,
  href,
  meta,
  score,
  dimensions,
  action,
}: {
  title: ReactNode;
  href?: string;
  meta?: ReactNode;
  score: number | null;
  dimensions?: Record<string, Dimension>;
  action?: ReactNode;
}) {
  const [open, setOpen] = useState(false);
  const dims = dimensions ? orderedDimensions(dimensions) : [];

  return (
    <li className={`evrow${open ? " is-open" : ""}`}>
      <div className="evrow__head">
        <button
          type="button"
          className="evrow__toggle"
          aria-expanded={open}
          disabled={dims.length === 0}
          onClick={() => setOpen((was) => !was)}
        >
          <span className="evrow__pr mono">{title}</span>
          {meta ? <span className="evrow__meta">{meta}</span> : null}
          <span className="evrow__score mono">{score === null ? "—" : format.n1(score)}</span>
          {dims.length > 0 ? (
            <span className="evrow__chev" aria-hidden="true">
              <Icon name="chev" size="sm" />
            </span>
          ) : null}
        </button>
        {/* The link sits outside the toggle: opening the PR and expanding the
            verdict are different intents, and an anchor inside a button is
            invalid besides. */}
        {href ? (
          <a
            className="evrow__link"
            href={href}
            target="_blank"
            rel="noreferrer noopener"
            title={href}
          >
            Open on GitHub
            <Icon name="chev" size="sm" />
          </a>
        ) : null}
        {action ? <span className="evrow__action">{action}</span> : null}
      </div>

      {open && dims.length > 0 ? (
        <ul className="dims dims--cards">
          {dims.map(([name, value]) => (
            <DimensionRow key={name} name={name} value={value} />
          ))}
        </ul>
      ) : null}
    </li>
  );
}

/** owner/name#number when the repo is known, the bare number when it is not. */
function prLabel(pr: JudgedPR): string {
  return pr.repo_owner && pr.repo_name
    ? `${pr.repo_owner}/${pr.repo_name}#${pr.pr_number}`
    : `PR #${pr.pr_number}`;
}

function ScoredPR({ pr, onDispute }: { pr: JudgedPR; onDispute?: (pr: JudgedPR) => void }) {
  return (
    <EvidenceRow
      title={prLabel(pr)}
      href={pr.url}
      meta={
        pr.scores?.length ? (
          <span className="facts">
            {pr.scores.map((s) => (
              <span className="fact" key={s.skill}>
                <span className="mono">{s.skill}</span>
                <span className="mono">{format.n1(s.score)}</span>
              </span>
            ))}
          </span>
        ) : null
      }
      score={bestScore(pr)}
      dimensions={pr.dimensions}
      action={
        onDispute ? (
          <button type="button" className="btn btn--ghost btn--sm" onClick={() => onDispute(pr)}>
            Dispute this verdict
          </button>
        ) : null
      }
    />
  );
}

function DroppedPR({ pr }: { pr: JudgedPR }) {
  return (
    <li className="ev">
      <div className="ev__head">
        <span className="ev__repo mono">PR #{pr.pr_number}</span>
        <span className="ev__right">
          <Pill tone={pr.skipped ? "stale" : "cherry"}>
            {pr.skipped ? "Not read" : "Did not count"}
          </Pill>
        </span>
      </div>
      <p className="ev__body">{pr.message ?? rejectionReason(pr.rejection_reason ?? pr.reason)}</p>
      <p className="small muted">
        {pr.skipped
          ? "Nobody has judged this — it could not be read. Check the URL rather than the verdict."
          : "The model read this and it did not count toward any skill."}
      </p>
    </li>
  );
}

export function JudgedEvidence({
  prs,
  skills,
  onDispute,
}: {
  prs: (PREvidence | JudgedPR)[];
  skills?: ClaimSkill[];
  onDispute?: (pr: JudgedPR) => void;
}) {
  const judged = prs.filter(isJudged);

  if (judged.length === 0) {
    return <Empty title="No verdicts yet" icon="clock" />;
  }

  // Grouped by skill, the way the hirer's scorecard reads. A claim is judged
  // per (PR, skill), so a flat list of PRs hides the thing standing is derived
  // from — how many distinct PRs evidence THIS skill.
  const bySkill = new Map<string, { pr: JudgedPR; score: number }[]>();
  for (const pr of judged) {
    for (const s of pr.scores ?? []) {
      const rows = bySkill.get(s.skill) ?? [];
      rows.push({ pr, score: s.score });
      bySkill.set(s.skill, rows);
    }
  }
  for (const rows of bySkill.values()) rows.sort((a, b) => b.score - a.score);

  // A PR the model read but credited to no skill still belongs on the page —
  // dropping it would make the evidence silently shorter than the claim. That
  // includes the rejected and the unreadable, which keep their own rows.
  const uncredited = judged.filter((pr) => (pr.scores ?? []).length === 0);
  const declared = new Map((skills ?? []).map((s) => [s.slug, s]));
  const order = [...bySkill.keys()].sort(
    (a, b) => (declared.get(b)?.score ?? 0) - (declared.get(a)?.score ?? 0) || a.localeCompare(b),
  );

  return (
    <div className="judged">
      {order.map((slug) => {
        const rows = bySkill.get(slug) ?? [];
        const meta = declared.get(slug);
        return (
          <section className="judged__skill" key={slug}>
            <div className="judged__head">
              <span className="judged__name">{slug}</span>
              {meta?.standing ? (
                <Pill tone={meta.standing === "primary" ? "primary" : "secondary"}>
                  {format.standing(meta.standing)}
                </Pill>
              ) : null}
              <span className="judged__count">
                {rows.length} {rows.length === 1 ? "pull request" : "pull requests"}
              </span>
            </div>
            <ul className="evlist">
              {rows.map(({ pr, score }) => (
                <EvidenceRow
                  key={pr.position}
                  title={prLabel(pr)}
                  href={pr.url}
                  score={score}
                  dimensions={pr.dimensions}
                  action={
                    onDispute ? (
                      <button
                        type="button"
                        className="btn btn--ghost btn--sm"
                        onClick={() => onDispute(pr)}
                      >
                        Dispute this verdict
                      </button>
                    ) : null
                  }
                />
              ))}
            </ul>
          </section>
        );
      })}

      {uncredited.length > 0 ? (
        <section className="judged__skill">
          <div className="judged__head">
            <span className="judged__name">Credited to no skill</span>
            <span className="judged__count">
              {uncredited.length} {uncredited.length === 1 ? "pull request" : "pull requests"}
            </span>
          </div>
          <ul className="evlist">
            {uncredited.map((pr) =>
              pr.rejected || pr.skipped ? (
                <DroppedPR key={pr.position} pr={pr} />
              ) : (
                <ScoredPR key={pr.position} pr={pr} onDispute={onDispute} />
              ),
            )}
          </ul>
        </section>
      ) : null}
    </div>
  );
}

export function Suggestions({
  suggestions,
  onDecide,
  pending,
}: {
  suggestions: Suggestion[];
  onDecide: (skillId: string, accept: boolean) => void;
  pending: boolean;
}) {
  const open = suggestions.filter((s) => !s.accepted_at && !s.dismissed_at);
  if (open.length === 0) return null;

  return (
    <Card>
      <div className="section__head">
        <h2 className="section__title">Skills the model noticed</h2>
        <span className="section__note">
          These count toward nothing until you accept one. Accepting makes it an ordinary claimed
          skill, and its already-judged pull requests count immediately.
        </span>
      </div>

      <ul className="dims">
        {open.map((s) => (
          <li className="ev" key={s.skill_id}>
            <div className="ev__head">
              <span className="ev__title">{s.name}</span>
              <span className="ev__right">
                <Pill tone="violet">Inert until accepted</Pill>
              </span>
            </div>
            <p className="rationale">{s.rationale}</p>
            <div className="row__actions">
              <Button
                variant="primary"
                size="sm"
                disabled={pending}
                onClick={() => onDecide(s.skill_id, true)}
              >
                <Icon name="check" size="sm" />
                Accept
              </Button>
              <Button
                variant="quiet"
                size="sm"
                disabled={pending}
                title="Dismissing is permanent"
                onClick={() => onDecide(s.skill_id, false)}
              >
                Dismiss
              </Button>
            </div>
          </li>
        ))}
      </ul>
    </Card>
  );
}
