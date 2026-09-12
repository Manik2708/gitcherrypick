// What a share link shows a stranger.
//
// No session, no rank, no id, no email. A published link proves nothing about
// who is reading it, so it carries the contributor's presentation and their
// evidence — not a handle to query them by, and not a position in a pool the
// reader cannot see (ADR-0002).
//
// A revoked link and one that never existed give the same answer, deliberately:
// telling them apart would let anyone probe which tokens were once real.

import { useParams } from "react-router-dom";
import { endpoints } from "../api/endpoints";
import type { Scorecard } from "../contract";
import { useClient } from "../hooks/session";
import { useAsync } from "../hooks/useAsync";
import * as format from "../logic/format";
import { Card, Empty, Loading, Meter, PageHead, Pill } from "../ui/primitives";

export function PublicScorecardPage() {
  const { token } = useParams<{ token: string }>();
  const client = useClient();

  const card = useAsync<Scorecard>(
    (signal) => client.get<Scorecard>(endpoints.public.scorecard(token ?? ""), signal),
    [client, token],
    Boolean(token),
  );

  if (card.loading) return <div className="page page--narrow"><Loading what="this scorecard" /></div>;

  if (card.error || !card.data) {
    return (
      <div className="page page--narrow">
        <Empty title="Not found">
          This link does not lead anywhere. It may have been revoked, or it may never have existed —
          we answer both the same way on purpose.
        </Empty>
      </div>
    );
  }

  const data = card.data;

  return (
    <div className="page page--narrow">
      <PageHead
        eyebrow="Evidence"
        title={data.user.display_name}
        lede={<span className="mono">@{data.user.github_login}</span>}
      />

      <div className="headline-scores">
        <div className="hscore hscore--lead">
          <span className="hscore__label">Overall</span>
          <span className="hscore__n mono">{format.n1(data.overall_score)}</span>
          <span className="hscore__note">Depth, 0–100.</span>
          <Meter value={data.overall_score} />
        </div>
        <div className="hscore">
          <span className="hscore__label">Generalist</span>
          <span className="hscore__n mono">{format.n1(data.generalist_score)}</span>
          <span className="hscore__note">Breadth, unbounded.</span>
        </div>
        <div className="hscore">
          <span className="hscore__label">Rubric</span>
          <span className="hscore__n mono">{data.rubric_version}</span>
        </div>
      </div>

      <Card>
        <div className="section__head">
          <h2 className="section__title">Skills and the evidence behind them</h2>
          <span className="section__note">
            Every number here comes from merged pull requests, judged against the skills this person
            claimed.
          </span>
        </div>

        {data.skills.length === 0 ? (
          <Empty title="No scored skills yet" icon="commit" />
        ) : (
          <ul className="standing">
            {data.skills.map((skill) => (
              <li
                className={
                  skill.standing === "secondary"
                    ? "standing__item standing__item--secondary"
                    : "standing__item"
                }
                key={skill.slug}
              >
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
                </p>
                {skill.evidence.length > 0 ? (
                  <ul className="evlist">
                    {skill.evidence.map((pr) => (
                      <li className="evrow" key={`${pr.repo}#${pr.pr_number}`}>
                        <span className="evrow__pr mono">
                          {pr.repo}#{pr.pr_number}
                        </span>
                        <span className="evrow__meta">Merged {format.date(pr.merged_at)}</span>
                        <span className="evrow__score mono">{format.n1(pr.score)}</span>
                      </li>
                    ))}
                  </ul>
                ) : null}
              </li>
            ))}
          </ul>
        )}
      </Card>
    </div>
  );
}
