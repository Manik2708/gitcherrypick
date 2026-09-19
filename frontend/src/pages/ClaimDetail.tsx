// One claim, in whichever state it is in.
//
// A draft is edited; a judged claim is read and argued with; either can be
// withdrawn, which is the action that can cost standing. The same route serves
// all of it because it is the same claim — a contributor following a link from
// "your claims" should not have to know which kind of page they are about to
// get.
//
// Evidence is FIVE POSITIONED SLOTS, not a textarea. The slot number is the
// position the server judges and reports against, so a refusal ("PR 3: already
// evidences Go in claim X") anchors to the row that caused it, and clearing
// slot 2 leaves slot 3 as slot 3.

import { useState } from "react";
import { useParams } from "react-router-dom";
import type { PREvidence, ValidationItem } from "../contract";
import {
  useClaim,
  useDecideSuggestion,
  useDisputeJudgement,
  useSetEvidence,
  useSetSkills,
  useSubmitClaim,
  useWithdrawClaim,
  useWithdrawPreview,
} from "../hooks/contributor";
import type { PRInput } from "../hooks/contributor";
import {
  MAX_PRS,
  claimStatus,
  filledSlots,
  isEditable,
  isLocked,
  readyToSubmit,
  slotsFromEvidence,
} from "../logic/claims";
import * as format from "../logic/format";
import { JudgedEvidence, Suggestions } from "../ui/judgement";
import { SkillPicker } from "../ui/SkillPicker";
import {
  Banner,
  Button,
  Card,
  Checkbox,
  Empty,
  Failure,
  Field,
  Icon,
  Loading,
  Notice,
  PageHead,
  Pill,
} from "../ui/primitives";

function EvidenceSlots({
  slots,
  saved,
  errors,
  onChange,
  onClear,
}: {
  slots: string[];
  saved: PREvidence[];
  errors: Map<number, string>;
  onChange: (index: number, url: string) => void;
  onClear: (index: number) => void;
}) {
  return (
    <ul className="dims">
      {slots.map((url, index) => {
        const position = index + 1;
        const resolved = saved.find((pr) => pr.position === position);
        const message = errors.get(position);

        return (
          <li className="ev" key={position}>
            <div className="ev__head">
              <span className="ev__repo mono">PR {position}</span>
              <span className="ev__right">
                {url.trim() ? (
                  <Button
                    variant="quiet"
                    size="sm"
                    title={`Clear slot ${position}`}
                    onClick={() => onClear(index)}
                  >
                    Clear
                  </Button>
                ) : null}
              </span>
            </div>

            <input
              className="input mono"
              value={url}
              placeholder="https://github.com/owner/repo/pull/123"
              aria-label={`Pull request ${position}`}
              aria-invalid={message ? true : undefined}
              onChange={(event) => onChange(index, event.target.value)}
            />

            {resolved && !message && resolved.repo_owner ? (
              <p className="small muted mono">
                {resolved.repo_owner}/{resolved.repo_name}#{resolved.pr_number} · {resolved.role}
              </p>
            ) : null}

            {message ? (
              <p className="ev__body">
                <b>
                  PR {position}: {message}
                </b>
              </p>
            ) : null}
          </li>
        );
      })}
    </ul>
  );
}

export function ClaimDetailPage() {
  const { claimId } = useParams<{ claimId: string }>();
  const claim = useClaim(claimId);

  const setEvidence = useSetEvidence(claimId ?? "");
  const setSkills = useSetSkills(claimId ?? "");
  const submit = useSubmitClaim(claimId ?? "");
  const decide = useDecideSuggestion(claimId ?? "");
  const dispute = useDisputeJudgement(claimId ?? "");
  const withdraw = useWithdrawClaim(claimId ?? "");
  const preview = useWithdrawPreview(claimId);

  const [slots, setSlots] = useState<string[] | null>(null);
  const [chosen, setChosen] = useState<string[]>([]);
  const [primary, setPrimary] = useState("");
  const [reason, setReason] = useState("");
  const [acknowledged, setAcknowledged] = useState(false);
  const now = new Date();

  if (claim.loading)
    return (
      <div className="page">
        <Loading what="the claim" />
      </div>
    );
  if (claim.error) {
    return (
      <div className="page page--narrow">
        <Failure message={claim.error.message} onRetry={claim.reload} />
      </div>
    );
  }
  if (!claim.data) {
    return (
      <div className="page page--narrow">
        <Empty title="No such claim" />
      </div>
    );
  }

  const current = claim.data;
  const editable = isEditable(current, now);
  const locked = isLocked(current, now);
  const readiness = readyToSubmit(current);

  const savedEvidence = (current.prs ?? []) as PREvidence[];
  const evidenceSlots = slots ?? slotsFromEvidence(savedEvidence);

  // A refusal that names a position belongs on that slot; one that names none
  // is about the claim as a whole and stays in the summary.
  const items = (submit.error?.detail.items ?? []) as ValidationItem[];
  const slotErrors = new Map<number, string>(
    items
      .filter((i) => typeof i.position === "number")
      .map((i) => [i.position as number, i.message]),
  );
  const claimWide = items.filter((i) => typeof i.position !== "number");

  const demotions = preview.data?.demotions ?? [];
  const failure =
    setEvidence.error ?? setSkills.error ?? decide.error ?? dispute.error ?? withdraw.error;

  async function saveEvidence(list: string[]) {
    const prs: PRInput[] = list
      .map((url, index) => ({ position: index + 1, url: url.trim() }))
      .filter((pr) => pr.url.length > 0);
    if (await setEvidence.run(prs)) claim.reload();
  }

  async function saveSkills() {
    const payload = chosen.map((slug) => ({
      slug,
      nominated_primary: slug === (primary || chosen[0]),
    }));
    if (await setSkills.run(payload)) claim.reload();
  }

  return (
    <div className="page">
      <PageHead
        eyebrow="Claim"
        title={current.nominated_primary ?? "Untitled claim"}
        lede={
          <>
            <Pill tone={current.status === "evaluated" ? "primary" : "secondary"}>
              {claimStatus(current.status)}
            </Pill>{" "}
            {locked ? <Pill tone="stale">Locked</Pill> : null}
          </>
        }
      />

      {locked ? (
        <Banner icon="lock">
          A judged claim is frozen for seven days — it unlocks{" "}
          {format.relativeDays(current.locked_until, now)}. The freeze is what stops a score being
          re-rolled by resubmitting the same work.
        </Banner>
      ) : null}

      {failure ? <Failure message={failure.message} /> : null}

      {claimWide.length > 0 ? (
        <Notice title="This could not be submitted">
          <ul>
            {claimWide.map((item, i) => (
              <li key={i}>{item.message}</li>
            ))}
          </ul>
        </Notice>
      ) : null}

      {current.status === "evaluated" ? (
        <>
          <Card>
            <div className="section__head">
              <h2 className="section__title">What your evidence was worth</h2>
              <span className="section__note">
                Each pull request was judged against every skill you declared. The dimension scores
                and the remarks are the reasoning — if you disagree with a verdict, this is what you
                are disagreeing with.
              </span>
            </div>
            <JudgedEvidence
              prs={current.prs ?? []}
              skills={current.skills}
              onDispute={(pr) => {
                // A dispute is filed against the claim — reevaluation_requests
                // is keyed on claim_id, there is no per-PR request — so the
                // per-PR button seeds the reason with which verdict is being
                // argued and sends you to the one form.
                setReason((was) =>
                  was.includes(`PR #${pr.pr_number}`)
                    ? was
                    : `${was ? `${was}\n\n` : ""}PR #${pr.pr_number}: `,
                );
                document.getElementById("disagree")?.scrollIntoView({ behavior: "smooth" });
                document.getElementById("dispute")?.focus({ preventScroll: true });
              }}
            />
          </Card>

          <Suggestions
            suggestions={current.suggestions ?? []}
            pending={decide.pending}
            onDecide={(skillId, accept) => {
              void decide.run(skillId, accept).then(() => claim.reload());
            }}
          />

          <Card className="claim-dispute" id="disagree">
            <div className="section__head">
              <h2 className="section__title">Disagree with this?</h2>
              <span className="section__note">
                A dispute is read by a person. Say which verdict is wrong and why — three rejected
                disputes start a cooldown, so it is worth making the argument specific.
              </span>
            </div>
            <Field label="What did the model get wrong?" htmlFor="dispute">
              <textarea
                className="input"
                id="dispute"
                rows={3}
                value={reason}
                onChange={(event) => setReason(event.target.value)}
              />
            </Field>
            <Button
              variant="ghost"
              disabled={dispute.pending || reason.trim().length === 0}
              onClick={() => void dispute.run(reason).then(() => claim.reload())}
            >
              Request a re-evaluation
            </Button>
          </Card>
        </>
      ) : null}

      {current.status === "queued" ? (
        <Banner icon="clock">
          Submitted and waiting. Nothing is scored until the evaluator reaches this claim.
        </Banner>
      ) : null}

      {editable ? (
        <>
          <Card>
            <div className="section__head">
              <h2 className="section__title">Evidence</h2>
              <span className="section__note mono">
                {filledSlots(evidenceSlots)} of {MAX_PRS} used
              </span>
            </div>
            <p className="field__help">
              Up to five merged pull requests you wrote. Each slot is judged in place, so removing
              one does not renumber the others.
            </p>

            <EvidenceSlots
              slots={evidenceSlots}
              saved={savedEvidence}
              errors={slotErrors}
              onChange={(index, url) =>
                setSlots(evidenceSlots.map((s, i) => (i === index ? url : s)))
              }
              onClear={(index) => setSlots(evidenceSlots.map((s, i) => (i === index ? "" : s)))}
            />

            <Button
              variant="ghost"
              disabled={setEvidence.pending}
              onClick={() => void saveEvidence(evidenceSlots)}
            >
              Save evidence
            </Button>
          </Card>

          <Card>
            <div className="section__head">
              <h2 className="section__title">Skills</h2>
              <span className="section__note">
                One is nominated primary and scored directly; the rest are scored as a share of it.
              </span>
            </div>

            <SkillPicker
              id="skillq"
              label="Search the catalogue"
              help="Skills are picked from a curated catalogue, never typed freely — otherwise “Go” and “Golang” rank as separate populations."
              chosen={chosen}
              onChange={setChosen}
            />

            {chosen.length > 1 ? (
              <Field
                label="Nominated primary"
                htmlFor="primary"
                help="Defaults to the first you picked."
              >
                <select
                  className="select"
                  id="primary"
                  value={primary}
                  onChange={(event) => setPrimary(event.target.value)}
                >
                  {chosen.map((slug) => (
                    <option key={slug} value={slug}>
                      {slug}
                    </option>
                  ))}
                </select>
              </Field>
            ) : null}

            <Button
              variant="ghost"
              disabled={setSkills.pending || chosen.length === 0}
              onClick={() => void saveSkills()}
            >
              Save skills
            </Button>
          </Card>

          <Card>
            <div className="section__head">
              <h2 className="section__title">Submit</h2>
            </div>
            {readiness.ready ? (
              <p className="field__help">
                Once submitted this is judged and then frozen for seven days.
              </p>
            ) : (
              <Notice title="Not ready yet">
                <ul>
                  {readiness.reasons.map((why) => (
                    <li key={why}>{why}</li>
                  ))}
                </ul>
              </Notice>
            )}
            <Button
              variant="primary"
              disabled={!readiness.ready || submit.pending}
              onClick={() => void submit.run().then(() => claim.reload())}
            >
              Submit for judgement
            </Button>
          </Card>
        </>
      ) : null}

      {current.status !== "withdrawn" && !locked ? (
        <Card>
          <div className="section__head">
            <h2 className="section__title">Withdraw this claim</h2>
            <span className="section__note">
              Withdrawing releases the evidence so you can use it elsewhere. What it can cost is
              standing, and the platform names which skills before you do it.
            </span>
          </div>

          {preview.loading ? <Loading what="what this would cost" /> : null}

          {preview.data && demotions.length === 0 ? (
            <Banner>
              Nothing loses standing. This claim's evidence is not the only thing holding any of
              your skills up.
            </Banner>
          ) : null}

          {demotions.length > 0 ? (
            <>
              <Notice title="Withdrawing costs you standing">
                <ul>
                  {demotions.map((d) => (
                    <li key={d.skill}>
                      <b>{d.skill}</b>: {d.from} → {d.to} ({d.distinct_pr_count_after} pull{" "}
                      {d.distinct_pr_count_after === 1 ? "request" : "requests"} left). {d.message}
                    </li>
                  ))}
                </ul>
              </Notice>
              <Checkbox
                label={`I understand this unranks ${
                  demotions.length === 1 ? "that skill" : "those skills"
                }`}
                checked={acknowledged}
                onChange={setAcknowledged}
              />
            </>
          ) : null}

          <Button
            variant="danger"
            disabled={withdraw.pending || (demotions.length > 0 && !acknowledged)}
            onClick={() => void withdraw.run(acknowledged).then(() => claim.reload())}
          >
            <Icon name="trash" size="sm" />
            Withdraw
          </Button>
        </Card>
      ) : null}
    </div>
  );
}
