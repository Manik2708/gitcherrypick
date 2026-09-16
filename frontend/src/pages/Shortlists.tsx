// Hiring rounds.
//
// A round in draft has told nobody. That is the whole design: staging is private
// so that confirming can be irreversible, and the list marks which rounds still
// have people who have not been told (ADR-0008 §3a).

import { useState } from "react";
import { Link } from "react-router-dom";
import type { ShortlistSummary } from "../contract";
import { useCreateShortlist, useOrgID, useRoles, useShortlists } from "../hooks/hirer";
import * as format from "../logic/format";
import {
  Button,
  Card,
  Empty,
  Failure,
  Field,
  Icon,
  Loading,
  PageHead,
  Pill,
} from "../ui/primitives";

function RoundCard({ round }: { round: ShortlistSummary }) {
  const untold = round.unnotified_count ?? 0;

  return (
    <li className="sl-card">
      <div className="sl-card__top">
        <Link className="sl-card__name" to={`/shortlists/${round.id}`}>
          {round.name}
        </Link>
        <Pill
          tone={
            round.status === "closed" ? "stale" : round.status === "draft" ? "secondary" : "primary"
          }
        >
          {round.status}
        </Pill>
      </div>

      <p className="sl-card__desc small muted">
        {round.entry_count ?? 0} {(round.entry_count ?? 0) === 1 ? "candidate" : "candidates"} ·
        result by {format.date(round.tentative_result_date)}
      </p>

      <div className="sl-card__foot">
        {untold > 0 && round.status !== "closed" ? (
          <Pill tone="cherry">{untold} not yet told</Pill>
        ) : (
          <span className="small muted">Everyone here has been told.</span>
        )}
        <Link className="btn btn--ghost btn--sm" to={`/shortlists/${round.id}`}>
          Open
          <Icon name="chev" size="sm" />
        </Link>
      </div>
    </li>
  );
}

export function ShortlistsPage() {
  const rounds = useShortlists();
  const create = useCreateShortlist();

  // Only OPEN roles can be rounded on: a draft is not a commitment, and a
  // closed one is a withdrawn opening.
  const org = useOrgID();
  const roles = useRoles(org.id, "open");
  const openRoles = roles.data?.roles ?? [];

  const [roleId, setRoleId] = useState("");

  const [name, setName] = useState("");
  const [date, setDate] = useState("");
  const [description, setDescription] = useState("");

  if (rounds.loading)
    return (
      <div className="page">
        <Loading what="your rounds" />
      </div>
    );
  if (rounds.error) {
    return (
      <div className="page page--narrow">
        <Failure message={rounds.error.message} onRetry={rounds.reload} />
      </div>
    );
  }

  const all = rounds.data?.shortlists ?? [];
  const drafts = all.filter((r) => r.status === "draft");
  const open = all.filter((r) => r.status === "open");
  const closed = all.filter((r) => r.status === "closed");

  return (
    <div className="page">
      <PageHead
        eyebrow="Rounds"
        title="Shortlists"
        lede="Building a round discloses nothing. Confirming it is what tells people — and that cannot be undone."
      />

      <Card>
        <p className="eyebrow">Start a round</p>

        {/* A round is FOR a job (ADR-0019 §3). One round, one role: wanting the
            same contributor for a second opening means a second round, which
            raises a second, separate contact request — two jobs are two
            decisions. Without this the invitation could only say that somebody
            is interested. */}
        <Field
          label="The job this round is for"
          htmlFor="role"
          help={
            openRoles.length === 0
              ? "You have no open roles. A round can only approach people for a job the organisation has committed to — create one first."
              : "Everybody on this round is approached for this job, and sees its salary, location and process."
          }
        >
          <select
            className="input"
            id="role"
            value={roleId}
            onChange={(event) => setRoleId(event.target.value)}
            disabled={openRoles.length === 0}
          >
            <option value="">Choose a role</option>
            {openRoles.map((role) => (
              <option key={role.id} value={role.id}>
                {role.title}
              </option>
            ))}
          </select>
        </Field>

        <Field label="Name" htmlFor="name">
          <input
            className="input"
            id="name"
            value={name}
            onChange={(event) => setName(event.target.value)}
          />
        </Field>
        <Field
          label="Tentative result date"
          htmlFor="date"
          help="Shown to each candidate, so they know what they are waiting for."
        >
          <input
            className="input"
            id="date"
            type="date"
            value={date}
            onChange={(event) => setDate(event.target.value)}
          />
        </Field>
        <Field label="What this round is for" htmlFor="desc">
          <input
            className="input"
            id="desc"
            value={description}
            onChange={(event) => setDescription(event.target.value)}
          />
        </Field>
        {create.error ? <Failure message={create.error.message} /> : null}
        <Button
          variant="primary"
          disabled={create.pending || !roleId || !name.trim() || !date}
          onClick={() =>
            void create.run(roleId, name, date, description).then((ok) => {
              if (ok) {
                setRoleId("");
                setName("");
                setDate("");
                setDescription("");
                rounds.reload();
              }
            })
          }
        >
          <Icon name="plus" size="sm" />
          Create
        </Button>
      </Card>

      {all.length === 0 ? (
        <Empty title="No rounds yet" icon="bookmark">
          Stage someone from search and a round will have something to hold.
        </Empty>
      ) : null}

      {[
        ["Draft — nobody told yet", drafts],
        ["Open", open],
        ["Closed", closed],
      ].map(([title, list]) =>
        (list as ShortlistSummary[]).length > 0 ? (
          <Card key={title as string}>
            <div className="section__head">
              <h2 className="section__title">{title as string}</h2>
              <span className="section__note mono">{(list as ShortlistSummary[]).length}</span>
            </div>
            <ul className="sl-grid">
              {(list as ShortlistSummary[]).map((round) => (
                <RoundCard key={round.id} round={round} />
              ))}
            </ul>
          </Card>
        ) : null,
      )}
    </div>
  );
}
