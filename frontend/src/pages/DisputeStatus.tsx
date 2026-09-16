// Whether a contributor may dispute, and if not why.
//
// This exists so nobody spends a request on a refusal. The cooldown escalates —
// three rejected disputes start one, and it doubles — so "you may try again in
// 28 days" is materially different from "you may not", and the screen says
// which. Acceptance never counts against anybody: a contributor who is
// repeatedly right is never throttled.

import { Link } from "react-router-dom";
import { useMyClaims, useReevaluationStatus } from "../hooks/contributor";
import * as format from "../logic/format";
import { Banner, Card, Empty, Failure, Loading, PageHead } from "../ui/primitives";

export function DisputeStatusPage() {
  const status = useReevaluationStatus();
  // The status endpoint returns claim ids only; the claims list supplies the
  // names, so a row says which claim it is rather than "open this claim".
  const claims = useMyClaims();

  if (status.loading)
    return (
      <div className="page">
        <Loading what="your dispute standing" />
      </div>
    );
  if (status.error) {
    return (
      <div className="page page--narrow">
        <Failure message={status.error.message} onRetry={status.reload} />
      </div>
    );
  }
  if (!status.data)
    return (
      <div className="page page--narrow">
        <Empty title="Nothing to show" />
      </div>
    );

  const d = status.data;
  const byId = new Map((claims.data?.claims ?? []).map((c) => [c.id, c]));

  return (
    <div className="page">
      <PageHead
        eyebrow="Disputes"
        title="Arguing with a verdict"
        lede="A dispute is read by a person. Being wrong once costs nothing — only a run of rejected disputes starts a cooldown."
      />

      <div className="headline-scores">
        <div className="hscore">
          <span className="hscore__label">Rejected so far</span>
          <span className="hscore__n mono">{d.rejection_count} of 3</span>
          <span className="hscore__note">
            Three in a row start a cooldown. Accepted disputes never count.
          </span>
        </div>
        <div className="hscore">
          <span className="hscore__label">Cooldown tier</span>
          <span className="hscore__n mono">{d.tier}</span>
          <span className="hscore__note">
            {d.tier === 0 ? "None yet." : "Each tier doubles the wait: 28, 56, 112, 224 days."}
          </span>
        </div>
        <div className="hscore">
          <span className="hscore__label">May dispute</span>
          <span className="hscore__n mono">{d.can_request ? "Yes" : "No"}</span>
          <span className="hscore__note">
            {d.cooldown_days ? `${d.cooldown_days}-day wait` : "Nothing is blocking a dispute."}
          </span>
        </div>
      </div>

      {d.blocked_by === "cooldown" ? (
        <Banner icon="clock">
          You are in a cooldown until {format.date(d.cooldown_until)} (
          {format.relativeDays(d.cooldown_until, new Date())}). It exists to make systematic
          disputing pointless, not to punish being wrong once.
        </Banner>
      ) : null}

      {d.blocked_by === "pending_request" ? (
        <Banner icon="clock">
          You already have a dispute open. One at a time — a queue of them from one person is a way
          to spend an administrator's attention rather than to be heard.
        </Banner>
      ) : null}

      <Card>
        <div className="section__head">
          <h2 className="section__title">Claims you could dispute</h2>
        </div>
        {d.claims_eligible.length === 0 ? (
          <Empty title={d.can_request ? "Nothing judged yet" : "None while you are blocked"}>
            {d.can_request
              ? "A claim has to carry a verdict before there is anything to argue with."
              : "Listing them would be an invitation to a refusal."}
          </Empty>
        ) : (
          <ul className="rows">
            {d.claims_eligible.map((id) => {
              const claim = byId.get(id);
              return (
                <li className="row" key={id}>
                  <div className="row__mid">
                    <div className="row__ident">
                      <Link className="row__name" to={`/claims/${id}`}>
                        {claim?.nominated_primary ?? "no skill declared"}
                      </Link>
                    </div>
                    {claim ? (
                      <div className="row__meta">
                        <span>
                          {claim.pr_count} {claim.pr_count === 1 ? "pull request" : "pull requests"}
                        </span>
                        {claim.evaluated_at ? (
                          <span>Judged {format.date(claim.evaluated_at)}</span>
                        ) : null}
                      </div>
                    ) : null}
                  </div>
                  <div className="row__right">
                    <div className="row__actions">
                      <Link className="btn btn--ghost btn--sm" to={`/claims/${id}#disagree`}>
                        Dispute this claim
                      </Link>
                    </div>
                  </div>
                </li>
              );
            })}
          </ul>
        )}
      </Card>
    </div>
  );
}
