// A contributor's own standing.
//
// Two user-level numbers with opposite meanings, so they are labelled with what
// they measure rather than left as bare figures: Overall is depth and is capped
// at 100, Generalist is breadth and is unbounded. Presenting them side by side
// without saying which is which invites reading the larger one as the better
// one.
//
// Rank is shown only to the contributor themselves — nobody sees anybody else's
// position (ADR-0005).

import { Link } from "react-router-dom";
import type { UserSkill } from "../contract";
import { useMyProfile, useMyRank, useMySkills } from "../hooks/contributor";
import { promotionDistance } from "../logic/claims";
import * as format from "../logic/format";
import {
  Banner,
  Card,
  Empty,
  Failure,
  Loading,
  Meter,
  PageHead,
  Pill,
  Prompt,
} from "../ui/primitives";

function SkillRow({ skill }: { skill: UserSkill }) {
  const distance = promotionDistance(skill.distinct_pr_count);

  return (
    <li
      className={
        skill.standing === "secondary"
          ? "standing__item standing__item--secondary"
          : "standing__item"
      }
    >
      <div className="standing__head">
        <span className="standing__name">{skill.name ?? skill.slug}</span>
        <Pill tone={skill.standing === "primary" ? "primary" : "secondary"}>
          {format.standing(skill.standing)}
        </Pill>
        {skill.stale ? <Pill tone="stale">Superseded rubric</Pill> : null}
        <span className="standing__right mono">{format.n1(skill.score)}</span>
      </div>

      <Meter value={skill.score} />

      <p className="standing__meta">
        {skill.distinct_pr_count} distinct{" "}
        {skill.distinct_pr_count === 1 ? "pull request" : "pull requests"} ·{" "}
        {format.standingNote(skill.standing)}
      </p>

      {/* Promotion is automatic at the fifth surviving PR, so the distance to
          it is a fact worth counting out rather than burying in a sentence. */}
      {distance.remaining > 0 ? (
        <div className="od-row">
          <span className="od-row" style={{ ["--od-gap" as string]: "4px" }}>
            {Array.from({ length: distance.need }, (_, i) => (
              <span key={i} className={i < distance.have ? "pill__dot" : "pill__dot pill--stale"} />
            ))}
          </span>
          <span className="small muted">
            {distance.have} of {distance.need} — {distance.remaining} more{" "}
            {distance.remaining === 1 ? "makes" : "make"} this primary
          </span>
        </div>
      ) : null}

      {skill.stale ? (
        <p className="small muted">
          Judged under rubric <span className="mono">{skill.rubric_version}</span>, which is no
          longer the active one. This number is not comparable with a current board until it is
          re-judged.
        </p>
      ) : null}
    </li>
  );
}

/**
 * Asks once, on the screen a contributor actually lands on.
 *
 * Signing in with GitHub tells the platform what somebody has BUILT and nothing
 * about what they WANT. Without this, a contributor who never opens "Being
 * found" is invisible to every role and has no reason to suspect it — the nav
 * item gives them no cause to click, and nothing else mentions it.
 *
 * It disappears the moment they answer, including if the answer is "none of
 * these" (ADR-0018). A prompt that came back after being answered would be a
 * nag about a decision they made.
 */
function ProfilePrompt() {
  const profile = useMyProfile();
  if (!profile.data?.needs_attention) return null;

  return (
    <Prompt
      title="Hirers cannot find you yet"
      action={<Link to="/being-found">Say what you are looking for</Link>}
    >
      Your standing comes from your merged pull requests. What kind of work you would take, and
      where you are, is the part only you can tell us — and no role can reach you until you do.
    </Prompt>
  );
}

export function MyStandingPage() {
  const skills = useMySkills();
  const rank = useMyRank();

  if (skills.loading)
    return (
      <div className="page">
        <Loading what="your standing" />
      </div>
    );
  if (skills.error) {
    return (
      <div className="page page--narrow">
        <Failure message={skills.error.message} onRetry={skills.reload} />
      </div>
    );
  }
  if (!skills.data) {
    return (
      <div className="page page--narrow">
        <Empty title="Nothing to show yet" />
      </div>
    );
  }

  const data = skills.data;
  const ranked = data.skills.filter((s) => s.standing === "primary");
  const unranked = data.skills.filter((s) => s.standing === "secondary");
  const optedOut = rank.data?.ranked === false && rank.data.unranked_reason === "opted_out";

  return (
    <div className="page">
      <PageHead
        eyebrow="Standing"
        title="Your standing"
        lede="Derived from merged pull requests a model has read — not from anything you declared about yourself."
      />

      <ProfilePrompt />

      {data.reevaluation_in_progress ? (
        <Banner icon="clock">
          One of your claims is being re-judged. These numbers will change when it finishes.
        </Banner>
      ) : null}

      <div className="headline-scores">
        <div className="hscore hscore--lead">
          <span className="hscore__label">Overall</span>
          <span className="hscore__n mono">{format.n1(data.overall_score)}</span>
          <span className="hscore__note">Depth. 0–100, weighted toward your strongest skills.</span>
          <Meter value={data.overall_score} />
          {rank.data?.ranked ? (
            <span className="hscore__rank mono">
              {format.rank(rank.data.overall.rank)} of {rank.data.overall.out_of}
            </span>
          ) : null}
        </div>

        <div className="hscore">
          <span className="hscore__label">Generalist</span>
          <span className="hscore__n mono">{format.n1(data.generalist_score)}</span>
          <span className="hscore__note">
            Breadth. Unbounded — it grows with each additional skill you evidence.
          </span>
          {rank.data?.ranked ? (
            <span className="hscore__rank mono">
              {format.rank(rank.data.generalist.rank)} of {rank.data.generalist.out_of}
            </span>
          ) : null}
        </div>

        <div className="hscore">
          <span className="hscore__label">Rubric</span>
          <span className="hscore__n mono">{data.rubric_version}</span>
          <span className="hscore__note">
            {data.stale ? "Superseded — a re-judgement is pending." : "Current."}
          </span>
        </div>
      </div>

      {rank.data?.ranked ? (
        <Banner icon="lock">
          Only you can see your rank. There is no public board of positions, and a link you publish
          carries your evidence but never your place in the pool.
        </Banner>
      ) : null}

      {optedOut ? (
        <Banner icon="lock">
          <b>You are not looking</b>, so you are excluded from search and from the ranked
          population. Your scores are unaffected — opting out hides you, it does not unmake what
          your evidence was worth.
        </Banner>
      ) : null}

      <Card>
        <div className="section__head">
          <h2 className="section__title">Ranked skills</h2>
          <span className="section__note">
            Five distinct surviving pull requests make a skill primary. These are what a hirer can
            search for.
          </span>
        </div>
        {ranked.length === 0 ? (
          <Empty title="Nothing ranked yet" icon="commit">
            Skills come from evidence: submit a claim naming the pull requests that show what you
            can do.
          </Empty>
        ) : (
          <ul className="standing">
            {ranked.map((skill) => (
              <SkillRow key={skill.slug} skill={skill} />
            ))}
          </ul>
        )}
      </Card>

      {unranked.length > 0 ? (
        <Card>
          <div className="section__head">
            <h2 className="section__title">Not yet ranked</h2>
            <span className="section__note">
              These count toward your scores and are visible on your scorecard. They are never
              ranked until they reach five.
            </span>
          </div>
          <ul className="standing">
            {unranked.map((skill) => (
              <SkillRow key={skill.slug} skill={skill} />
            ))}
          </ul>
        </Card>
      ) : null}

      <p className="small">
        <Link to="/claims">Your claims</Link>
      </p>
    </div>
  );
}
