// A contributor's claims.
//
// A claim is the unit of submission: up to five merged pull requests, the skills
// they demonstrate, judged together. Drafts and judged claims are shown apart
// because they afford opposite things — one is edited, the other is argued with.

import { Link, useNavigate } from "react-router-dom";
import type { ClaimSummary } from "../contract";
import { useCreateClaim, useMyClaims, useReevaluationStatus } from "../hooks/contributor";
import { claimStatus, drafts, judged, queued } from "../logic/claims";
import * as format from "../logic/format";
import {
  Button,
  Card,
  Empty,
  Failure,
  Icon,
  Loading,
  PageHead,
  Pill,
} from "../ui/primitives";

function tone(status: ClaimSummary["status"]) {
  switch (status) {
    case "evaluated":
      return "primary" as const;
    case "invalid":
      return "cherry" as const;
    case "queued":
      return "violet" as const;
    default:
      return "secondary" as const;
  }
}

function ClaimRow({ claim, disputable }: { claim: ClaimSummary; disputable: boolean }) {
  return (
    <li className="row">
      <div className="row__mid">
        <div className="row__ident">
          <Link className="row__name" to={`/claims/${claim.id}`}>
            {claim.nominated_primary ?? "no skill declared"}
          </Link>
          <Pill tone={tone(claim.status)}>{claimStatus(claim.status)}</Pill>
        </div>
        <div className="row__meta">
          <span>
            {claim.pr_count} {claim.pr_count === 1 ? "pull request" : "pull requests"}
          </span>
          <span>
            {claim.skill_count} {claim.skill_count === 1 ? "skill" : "skills"}
          </span>
          {claim.evaluated_at ? <span>Judged {format.date(claim.evaluated_at)}</span> : null}
        </div>
      </div>
      <div className="row__right">
        <div className="row__actions">
          {disputable ? (
            <Link className="btn btn--ghost btn--sm" to={`/claims/${claim.id}#disagree`}>
              Dispute
            </Link>
          ) : null}
          <Link className="btn btn--ghost btn--sm" to={`/claims/${claim.id}`}>
            Open
            <Icon name="chev" size="sm" />
          </Link>
        </div>
      </div>
    </li>
  );
}

export function ClaimsPage() {
  const claims = useMyClaims();
  const create = useCreateClaim();
  const reevaluation = useReevaluationStatus();
  const navigate = useNavigate();

  async function start() {
    const claim = await create.run();
    if (claim) navigate(`/claims/${claim.id}`);
  }

  if (claims.loading) return <div className="page"><Loading what="your claims" /></div>;
  if (claims.error) {
    return (
      <div className="page page--narrow">
        <Failure message={claims.error.message} onRetry={claims.reload} />
      </div>
    );
  }

  const all = claims.data?.claims ?? [];
  const open = drafts(all);
  const waiting = queued(all);
  const done = judged(all);
  // A dispute is per claim: the API says which ones are eligible, and a claim
  // that is not — unjudged, already argued, or inside a cooldown — simply does
  // not offer the action rather than offering one that would be refused.
  const eligible = new Set(reevaluation.data?.claims_eligible ?? []);

  return (
    <div className="page">
      <PageHead
        eyebrow="Evidence"
        title="Your claims"
        lede="A claim is up to five merged pull requests and the skills they demonstrate. The evidence is what gets judged — not the claim."
        actions={
          <Button variant="primary" disabled={create.pending} onClick={() => void start()}>
            <Icon name="plus" size="sm" />
            Start a claim
          </Button>
        }
      />

      {create.error ? <Failure message={create.error.message} /> : null}

      <div className="headline-scores">
        <div className="hscore">
          <span className="hscore__label">Drafts</span>
          <span className="hscore__n mono">{open.length}</span>
          <span className="hscore__note">Editable, and judged by nobody yet.</span>
        </div>
        <div className="hscore">
          <span className="hscore__label">Awaiting judgement</span>
          <span className="hscore__n mono">{waiting.length}</span>
          <span className="hscore__note">Queued. Nothing scores until the evaluator reaches it.</span>
        </div>
        <div className="hscore">
          <span className="hscore__label">Judged</span>
          <span className="hscore__n mono">{done.length}</span>
          <span className="hscore__note">Carrying a verdict you can argue with.</span>
        </div>
      </div>

      {all.length === 0 ? (
        <Empty
          title="No claims yet"
          icon="commit"
          actions={
            <Button variant="primary" size="sm" disabled={create.pending} onClick={() => void start()}>
              <Icon name="plus" size="sm" />
              Start your first claim
            </Button>
          }
        >
          A claim is up to five merged pull requests and the skills they demonstrate — the evidence
          is what gets judged, not the claim.
        </Empty>
      ) : null}

      {([
        ["Drafts", open, undefined],
        ["Awaiting judgement", waiting, "Submitted and queued. Nothing here is scored until the evaluator reaches it."],
        ["Judged", done, undefined],
      ] as const).map(([title, list, note]) =>
        list.length > 0 ? (
          <Card key={title}>
            <div className="section__head">
              <h2 className="section__title">{title}</h2>
              {note ? <span className="section__note">{note}</span> : null}
            </div>
            <ul className="rows">
              {list.map((claim) => (
                <ClaimRow key={claim.id} claim={claim} disputable={eligible.has(claim.id)} />
              ))}
            </ul>
          </Card>
        ) : null,
      )}
    </div>
  );
}
