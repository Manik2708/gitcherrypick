// One contributor, in full.
//
// Two rules this screen exists to honour:
//
//   Score and standing are ORTHOGONAL. A secondary skill scoring 81 renders
//   unranked beside a primary scoring 62. No `skills=` filter would ever have
//   found that secondary one; the scorecard shows it anyway, because standing
//   gates searchability and not disclosure (ADR-0008 §10).
//
//   The address is what this page does NOT show by default. An email appears
//   only once the contributor accepted a contact request — interest from one
//   side is not consent from the other.

import { Link, useParams } from "react-router-dom";
import { useScorecard } from "../hooks/hirer";
import { EvidenceRow } from "../ui/judgement";
import * as format from "../logic/format";
import {
  Avatar,
  Banner,
  Card,
  Empty,
  Failure,
  Icon,
  Loading,
  Meter,
  Pill,
} from "../ui/primitives";

export function ScorecardPage() {
  const { userId } = useParams<{ userId: string }>();
  const card = useScorecard(userId);

  if (card.loading) return <div className="page"><Loading what="the scorecard" /></div>;
  if (card.error) {
    return (
      <div className="page page--narrow">
        <Failure message={card.error.message} onRetry={card.reload} />
      </div>
    );
  }
  if (!card.data) {
    return (
      <div className="page page--narrow">
        <Empty title="No scorecard">Nothing to show for this contributor.</Empty>
      </div>
    );
  }

  const data = card.data;
  const primary = data.skills.filter((s) => s.standing === "primary");
  const secondary = data.skills.filter((s) => s.standing === "secondary");

  return (
    <div className="page">
      <Link className="crumb" to="/search">
        <Icon name="back" size="sm" />
        Back to results
      </Link>

      <div className="sc-head">
        <Avatar name={data.user.display_name} size="lg" tint={3} />
        <div className="sc-ident">
          <h1 className="sc-name display">{data.user.display_name}</h1>
          <p className="sc-login mono">@{data.user.github_login}</p>
          {data.user.availability ? (
            <Pill tone={data.user.active === false ? "stale" : "primary"}>
              {format.availability(data.user.availability.status)}
            </Pill>
          ) : null}
        </div>
      </div>

      <div className="headline-scores">
        <div className="hscore hscore--lead">
          <span className="hscore__label">Overall</span>
          <span className="hscore__n mono">{format.n1(data.overall_score)}</span>
          <span className="hscore__note">Depth, capped at 100.</span>
          <Meter value={data.overall_score} />
        </div>
        <div className="hscore">
          <span className="hscore__label">Generalist</span>
          <span className="hscore__n mono">{format.n1(data.generalist_score)}</span>
          <span className="hscore__note">Breadth, unbounded — not comparable with Overall.</span>
        </div>
        <div className="hscore">
          <span className="hscore__label">Rubric</span>
          <span className="hscore__n mono">{data.rubric_version}</span>
          <span className="hscore__note">The version every number here was judged under.</span>
        </div>
      </div>

      {data.email ? (
        <Banner icon="mail">
          Contact: <b className="mono">{data.email}</b> — released because they accepted your
          organisation's request.
        </Banner>
      ) : (
        <Banner icon="lock">
          No contact address. It is released only when the contributor accepts a request, which a
          confirmed shortlist raises.
        </Banner>
      )}

      <Card>
        <div className="section__head">
          <h2 className="section__title">Ranked skills</h2>
          <span className="section__note">
            Five distinct surviving pull requests make a skill primary.
          </span>
        </div>
        {primary.length === 0 ? (
          <Empty title="No ranked skills">
            Nothing here reaches five distinct surviving pull requests yet.
          </Empty>
        ) : (
          <ul className="standing">
            {primary.map((skill) => (
              <SkillBlock key={skill.slug} skill={skill} />
            ))}
          </ul>
        )}
      </Card>

      {secondary.length > 0 ? (
        <Card>
          <div className="section__head">
            <h2 className="section__title">Counted, never ranked</h2>
            <span className="section__note">
              No skills filter would have found these — they are shown because standing gates
              searchability, not disclosure.
            </span>
          </div>
          <ul className="standing">
            {secondary.map((skill) => (
              <SkillBlock key={skill.slug} skill={skill} />
            ))}
          </ul>
        </Card>
      ) : null}
    </div>
  );
}

function SkillBlock({ skill }: { skill: NonNullable<ReturnType<typeof useScorecard>["data"]>["skills"][number] }) {
  return (
    <li className={skill.standing === "secondary" ? "standing__item standing__item--secondary" : "standing__item"}>
      <div className="standing__head">
        <span className="standing__name">{skill.name}</span>
        <Pill tone={skill.standing === "primary" ? "primary" : "secondary"}>
          {format.standing(skill.standing)}
        </Pill>
        <span className="standing__right mono">{format.n1(skill.score)}</span>
      </div>

      <Meter value={skill.score} />

      <p className="standing__meta">
        {skill.distinct_pr_count} distinct{" "}
        {skill.distinct_pr_count === 1 ? "pull request" : "pull requests"}
        {typeof skill.rank === "number" ? ` · ranked ${format.rank(skill.rank)}` : " · unranked"}
      </p>

      {skill.evidence.length > 0 ? (
        <ul className="evlist">
          {skill.evidence.map((pr) => (
            <EvidenceRow
              key={`${pr.repo}#${pr.pr_number}`}
              title={`${pr.repo}#${pr.pr_number}`}
              href={pr.url}
              meta={`Merged ${format.date(pr.merged_at)}`}
              score={pr.score}
              dimensions={pr.dimensions}
            />
          ))}
        </ul>
      ) : null}
    </li>
  );
}
