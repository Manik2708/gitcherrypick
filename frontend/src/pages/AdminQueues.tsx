// The four things only an admin does.
//
// Each is a decision that reaches somebody: verification decides whether an
// organisation can hire at all, a skill request changes the catalogue
// permanently, a dispute is a person arguing with a score, and a sweep
// invalidates every number on the platform.
//
// A rejection always carries a reason, because "rejected" with no explanation
// gives the applicant nothing to fix — and the API refuses one without it.

import { useState } from "react";
import {
  useDecideReevaluation,
  useDecideSkillRequest,
  useDecideVerification,
  useReevaluationQueue,
  useSkillRequestQueue,
  useSweep,
  useVerificationQueue,
} from "../hooks/admin";
import * as format from "../logic/format";
import {
  Banner,
  Button,
  Card,
  Empty,
  Failure,
  Field,
  Loading,
  Notice,
  PageHead,
  Pill,
} from "../ui/primitives";

const CATEGORIES = ["language", "framework", "platform", "database", "practice", "tool"] as const;

function VerificationQueue() {
  const queue = useVerificationQueue();
  const decide = useDecideVerification();
  const [reasons, setReasons] = useState<Record<string, string>>({});
  const [payment, setPayment] = useState<Record<string, boolean>>({});

  const requests = queue.data?.requests ?? [];

  return (
    <Card>
      <div className="section__head">
        <h2 className="section__title">Hiring accounts awaiting review</h2>
        <span className="section__note mono">{requests.length}</span>
      </div>

      <p className="field__help">
        Approving lets this organisation search and shortlist. Payment capability is a separate
        judgement — a studio can be demonstrably real and still have nothing but self-reported
        payment history, and a contributor deciding whether to release their address is told which
        of the two was established.
      </p>

      {queue.loading ? <Loading what="verifications" /> : null}
      {queue.error ? <Failure message={queue.error.message} onRetry={queue.reload} /> : null}
      {decide.error ? <Failure message={decide.error.message} /> : null}
      {!queue.loading && requests.length === 0 ? <Empty title="Nothing waiting" icon="check" /> : null}

      <ul className="rows">
        {requests.map((r) => (
          <li className="row" key={r.id}>
            <div className="row__mid">
              <div className="row__ident">
                <span className="row__name">{r.subject.display_name}</span>
                <Pill tone="secondary">{r.subject.kind}</Pill>
                <span className="row__login mono">{Math.round(r.age_hours)}h waiting</span>
              </div>
              {r.organization ? <p className="row__meta">{r.organization.name}</p> : null}
              {r.hirer ? <p className="row__meta mono">{r.hirer.email}</p> : null}

              {r.proofs.length > 0 ? (
                <ul className="facts">
                  {r.proofs.map((p, i) => (
                    <li className="fact" key={i}>
                      <Pill tone="violet">{p.kind}</Pill>
                      <span>{p.notes ?? p.value}</span>
                      {p.attachment_url ? (
                        <a href={p.attachment_url} target="_blank" rel="noreferrer">
                          attachment
                        </a>
                      ) : null}
                    </li>
                  ))}
                </ul>
              ) : (
                <Banner icon="warn">No proofs attached.</Banner>
              )}

              <Field
                label="Reason"
                htmlFor={`reason-${r.id}`}
                help="Required to reject; shown to the applicant."
              >
                <input
                  className="input"
                  id={`reason-${r.id}`}
                  value={reasons[r.id] ?? ""}
                  onChange={(e) => setReasons({ ...reasons, [r.id]: e.target.value })}
                />
              </Field>

              <label className="checkline">
                <input
                  type="checkbox"
                  checked={payment[r.id] ?? false}
                  onChange={(e) => setPayment({ ...payment, [r.id]: e.target.checked })}
                />
                <span className="checkline__text">Payment capability established</span>
              </label>
            </div>

            <div className="row__right">
              <div className="row__actions">
                <Button
                  variant="primary"
                  size="sm"
                  disabled={decide.pending}
                  onClick={() =>
                    void decide
                      .run(r.id, true, reasons[r.id] ?? "", payment[r.id] ?? false)
                      .then(queue.reload)
                  }
                >
                  Approve
                </Button>
                <Button
                  variant="quiet"
                  size="sm"
                  disabled={decide.pending || !(reasons[r.id] ?? "").trim()}
                  onClick={() =>
                    void decide.run(r.id, false, reasons[r.id] ?? "", false).then(queue.reload)
                  }
                >
                  Reject
                </Button>
              </div>
            </div>
          </li>
        ))}
      </ul>
    </Card>
  );
}

function SkillRequestQueue() {
  const queue = useSkillRequestQueue();
  const decide = useDecideSkillRequest();
  const [slug, setSlug] = useState<Record<string, string>>({});
  const [category, setCategory] = useState<Record<string, string>>({});
  const [reason, setReason] = useState<Record<string, string>>({});

  const requests = (queue.data?.requests ?? []).filter((r) => r.status === "pending");

  return (
    <Card>
      <div className="section__head">
        <h2 className="section__title">Catalogue proposals</h2>
        <span className="section__note mono">{requests.length}</span>
      </div>

      <Notice title="A bad slug fragments a population forever">
        Approving creates the skill, and you name it — which is not always what was proposed.
        Standing is per skill, and two spellings never merge.
      </Notice>

      {queue.loading ? <Loading what="skill requests" /> : null}
      {queue.error ? <Failure message={queue.error.message} onRetry={queue.reload} /> : null}
      {decide.error ? <Failure message={decide.error.message} /> : null}
      {!queue.loading && requests.length === 0 ? <Empty title="Nothing waiting" icon="check" /> : null}

      <ul className="rows">
        {requests.map((r) => (
          <li className="row" key={r.id}>
            <div className="row__mid">
              <div className="row__ident">
                <span className="row__name">{r.proposed_name}</span>
                <span className="row__login">by {r.requested_by.display_name}</span>
              </div>
              <p className="rationale">{r.rationale}</p>

              <Field
                label="Slug to create"
                htmlFor={`slug-${r.id}`}
                help="Lower case, hyphenated."
              >
                <input
                  className="input mono"
                  id={`slug-${r.id}`}
                  placeholder={r.proposed_name.toLowerCase().replace(/\s+/g, "-")}
                  value={slug[r.id] ?? ""}
                  onChange={(e) => setSlug({ ...slug, [r.id]: e.target.value })}
                />
              </Field>

              <Field
                label="Category"
                htmlFor={`cat-${r.id}`}
                help="What KIND of skill this is — it groups the catalogue."
              >
                <select
                  className="select"
                  id={`cat-${r.id}`}
                  value={category[r.id] ?? "language"}
                  onChange={(e) => setCategory({ ...category, [r.id]: e.target.value })}
                >
                  {CATEGORIES.map((c) => (
                    <option key={c} value={c}>
                      {c[0]!.toUpperCase() + c.slice(1)}
                    </option>
                  ))}
                </select>
              </Field>

              <Field label="Reason" htmlFor={`sr-reason-${r.id}`}>
                <input
                  className="input"
                  id={`sr-reason-${r.id}`}
                  value={reason[r.id] ?? ""}
                  onChange={(e) => setReason({ ...reason, [r.id]: e.target.value })}
                />
              </Field>
            </div>

            <div className="row__right">
              <div className="row__actions">
                <Button
                  variant="primary"
                  size="sm"
                  disabled={decide.pending || !(slug[r.id] ?? "").trim()}
                  onClick={() =>
                    void decide
                      .run(r.id, true, reason[r.id] ?? "", {
                        slug: slug[r.id] ?? "",
                        name: r.proposed_name,
                        category: category[r.id] ?? "language",
                      })
                      .then(queue.reload)
                  }
                >
                  Approve and create
                </Button>
                <Button
                  variant="quiet"
                  size="sm"
                  disabled={decide.pending || !(reason[r.id] ?? "").trim()}
                  onClick={() =>
                    void decide.run(r.id, false, reason[r.id] ?? "", null).then(queue.reload)
                  }
                >
                  Reject
                </Button>
              </div>
            </div>
          </li>
        ))}
      </ul>
    </Card>
  );
}

function DisputeQueue() {
  const queue = useReevaluationQueue();
  const decide = useDecideReevaluation();
  const [note, setNote] = useState<Record<string, string>>({});

  const requests = queue.data?.requests ?? [];

  return (
    <Card>
      <div className="section__head">
        <h2 className="section__title">Disputed judgements</h2>
        <span className="section__note mono">{requests.length}</span>
      </div>

      <p className="field__help">
        Accepting re-queues the claim to be judged again. Rejecting advances an escalating cooldown
        — three rejections start one, and acceptance never counts against anybody.
      </p>

      {queue.loading ? <Loading what="disputes" /> : null}
      {queue.error ? <Failure message={queue.error.message} onRetry={queue.reload} /> : null}
      {decide.error ? <Failure message={decide.error.message} /> : null}
      {!queue.loading && requests.length === 0 ? <Empty title="Nothing waiting" icon="check" /> : null}

      <ul className="rows">
        {requests.map((r) => (
          <li className="row" key={r.id}>
            <div className="row__mid">
              <div className="row__ident">
                <span className="row__name">{r.user.display_name}</span>
              </div>
              <p className="rationale">{r.reason}</p>
              <Field label="Decision note" htmlFor={`note-${r.id}`}>
                <input
                  className="input"
                  id={`note-${r.id}`}
                  value={note[r.id] ?? ""}
                  onChange={(e) => setNote({ ...note, [r.id]: e.target.value })}
                />
              </Field>
            </div>
            <div className="row__right">
              <div className="row__actions">
                <Button
                  variant="primary"
                  size="sm"
                  disabled={decide.pending}
                  onClick={() => void decide.run(r.id, true, note[r.id] ?? "").then(queue.reload)}
                >
                  Accept — re-judge
                </Button>
                <Button
                  variant="quiet"
                  size="sm"
                  disabled={decide.pending || !(note[r.id] ?? "").trim()}
                  onClick={() => void decide.run(r.id, false, note[r.id] ?? "").then(queue.reload)}
                >
                  Reject
                </Button>
              </div>
            </div>
          </li>
        ))}
      </ul>
    </Card>
  );
}

function SweepControl() {
  const sweep = useSweep();
  const [version, setVersion] = useState("");
  const [reason, setReason] = useState("");
  const [acknowledged, setAcknowledged] = useState(false);

  return (
    <Card>
      <div className="section__head">
        <h2 className="section__title">Rubric sweep</h2>
      </div>

      <Notice title="Every score leaves search until it is re-judged">
        Re-runs the whole corpus under a new rubric. The entire scored population falls out of
        search the moment the active version moves, and drains back in as re-evaluation completes —
        a leaderboard mixing two versions would rank people by which one happened to judge them.
      </Notice>

      {sweep.error ? <Failure message={sweep.error.message} /> : null}
      {sweep.data ? (
        <Banner>
          Sweep started: <span className="mono">{sweep.data.from_rubric_version}</span> →{" "}
          <span className="mono">{sweep.data.to_rubric_version}</span>,{" "}
          {sweep.data.claims_enqueued} claims re-queued at {format.date(sweep.data.created_at)}.
        </Banner>
      ) : null}

      <Field label="Target rubric version" htmlFor="version">
        <input
          className="input mono"
          id="version"
          value={version}
          onChange={(e) => setVersion(e.target.value)}
        />
      </Field>
      <Field
        label="Reason"
        htmlFor="sweep-reason"
        help="Recorded permanently — it is what makes this reviewable later."
      >
        <input
          className="input"
          id="sweep-reason"
          value={reason}
          onChange={(e) => setReason(e.target.value)}
        />
      </Field>

      <label className="checkline">
        <input
          type="checkbox"
          checked={acknowledged}
          onChange={(e) => setAcknowledged(e.target.checked)}
        />
        <span className="checkline__text">
          I understand every score leaves search until it is re-judged
        </span>
      </label>

      <Button
        variant="danger"
        disabled={sweep.pending || !acknowledged || !version.trim() || !reason.trim()}
        onClick={() => void sweep.run(version, reason)}
      >
        Start the sweep
      </Button>
    </Card>
  );
}

export function AdminQueuesPage() {
  return (
    <div className="page">
      <PageHead
        eyebrow="Administration"
        title="Review queues"
        lede="Four decisions that reach somebody. A rejection always carries a reason, because “rejected” with nothing to fix is not an answer."
      />
      <VerificationQueue />
      <SkillRequestQueue />
      <DisputeQueue />
      <SweepControl />
    </div>
  );
}
