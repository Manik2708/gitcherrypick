// The three boards.
//
// Always the whole population, and no include-inactive toggle: a board shows
// everyone it ranks and reports who has gone quiet rather than hiding them
// (ADR-0008 §7). It also names the rubric it ranks by — a board mixing two
// versions would order people by which one happened to judge them.

import { useState } from "react";
import { Link } from "react-router-dom";
import { useLeaderboard } from "../hooks/hirer";
import * as format from "../logic/format";
import { SkillPicker } from "../ui/SkillPicker";
import {
  Avatar,
  Card,
  Empty,
  Failure,
  Loading,
  PageHead,
  Pill,
} from "../ui/primitives";

type Kind = "overall" | "generalist" | "skill";

const NOTE: Record<Kind, string> = {
  overall: "Depth: how good the strongest evidence is.",
  generalist:
    "Breadth: how many skills are evidenced at all. Unbounded, so the numbers are not comparable with Overall.",
  skill: "Everyone who holds this skill at primary standing.",
};

export function LeaderboardPage() {
  const [kind, setKind] = useState<Kind>("overall");
  const [skill, setSkill] = useState("go");

  const board = useLeaderboard(kind, kind === "skill" ? skill : undefined);

  return (
    <div className="page">
      <PageHead
        eyebrow="Standing"
        title="The boards"
        lede="Ranked over the whole scored population under one rubric version. Nobody is hidden; those who went quiet are marked."
      />

      <Card>
        <div className="segmented" role="tablist">
          {(["overall", "generalist", "skill"] as const).map((k) => (
            <button
              key={k}
              type="button"
              role="tab"
              aria-selected={kind === k}
              onClick={() => setKind(k)}
            >
              {k === "skill" ? "By skill" : k === "overall" ? "Overall" : "Generalist"}
            </button>
          ))}
        </div>

        {kind === "skill" ? (
          <SkillPicker
            id="skill"
            label="Which skill?"
            help="The same catalogue the search filters use — a board exists per skill, not per spelling."
            chosen={skill ? [skill] : []}
            // One board at a time, so the last pick replaces rather than adds.
            onChange={(next) => setSkill(next[next.length - 1] ?? "")}
          />
        ) : null}

        <p className="field__help">{NOTE[kind]}</p>
      </Card>

      {/* Nothing chosen yet is a state, not a failure. The stale board from the
          previous skill is dropped too, so the page never shows one skill's
          ranking under another skill's empty picker. */}
      {kind === "skill" && !skill ? (
        <Empty title="Pick a skill" icon="search">
          A board exists per skill. Choose one above and it appears here.
        </Empty>
      ) : (
        <>
          {board.loading ? <Loading what="the board" /> : null}
          {board.error ? <Failure message={board.error.message} onRetry={board.reload} /> : null}
        </>
      )}

      {board.data && !(kind === "skill" && !skill) ? (
        <Card>
          <div className="section__head">
            <h2 className="section__title">
              {board.data.skill ? board.data.skill.name : kind === "overall" ? "Overall" : "Generalist"}
            </h2>
            <span className="section__note mono">rubric {board.data.rubric_version}</span>
          </div>

          {board.data.entries.length === 0 ? (
            <Empty title="Nobody is ranked under the active rubric">
              If a sweep is running, the board fills as re-evaluation drains.
            </Empty>
          ) : (
            <ol className="board">
              {board.data.entries.map((entry) => (
                <li className="entry" key={entry.user_id}>
                  <span className={entry.rank <= 3 ? "rank rank--top" : "rank"}>
                    <span className="rank__n mono">#{entry.rank}</span>
                  </span>
                  <Avatar name={entry.display_name} size="sm" tint={(entry.rank % 6) + 1} />
                  <span className="entry__who">
                    <Link className="entry__name" to={`/contributors/${entry.user_id}`}>
                      {entry.display_name}
                    </Link>
                    <span className="entry__meta mono">@{entry.github_login}</span>
                  </span>
                  {!entry.active ? (
                    <span className="entry__status">
                      <Pill tone="stale">Quiet</Pill>
                    </span>
                  ) : null}
                  <span className="mono">{format.n1(entry.score)}</span>
                </li>
              ))}
            </ol>
          )}
        </Card>
      ) : null}
    </div>
  );
}
