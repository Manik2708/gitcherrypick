// Advertising a role (ADR-0020).
//
// Everything before this was private: a hirer searched, shortlisted, and the
// contributor heard about the job only when a contact request arrived.
// Publishing inverts that in ONE direction — somebody can now see what exists
// — and in no other. There is no apply button on the far side of this, no way
// for a contributor to register interest, and no counter telling a company who
// looked. They still approach; the address is still released only on
// acceptance.
//
// The bar is the substance of the form. It is written here and NOT on the
// role, because an open role is immutable: a threshold living on the role
// could not be raised without superseding the job and closing it under
// everybody already contacted.

import { useEffect, useState } from "react";
import type { Opening, OpeningBar, OpeningSkill, Role } from "../contract";
import { useOpening, usePublishOpening, useSaveOpening, useWithdrawOpening } from "../hooks/hirer";
import { Banner, Button, Card, Failure, Field, Pill, SectionTitle } from "./primitives";
import { SkillPicker } from "./SkillPicker";

const NO_BAR: OpeningBar = {
  min_overall_score: null,
  min_generalist_score: null,
  min_oss_yoe: null,
  skills: [],
};

/** Blank means NO bar. Zero means a bar of zero — they are not the same. */
function num(raw: string): number | null {
  return raw.trim() === "" ? null : Number(raw);
}

export function OpeningForm({ orgId, role }: { orgId: string; role: Role }) {
  const existing = useOpening(orgId, role.id);
  const save = useSaveOpening(orgId, role.id);
  const publish = usePublishOpening(orgId, role.id);
  const withdraw = useWithdrawOpening(orgId, role.id);

  const [bar, setBar] = useState<OpeningBar>(NO_BAR);
  const [loaded, setLoaded] = useState(false);

  // Load once. Re-syncing on every render would discard what somebody was
  // halfway through typing whenever the read refreshed.
  useEffect(() => {
    if (existing.data && !loaded) {
      setBar(existing.data);
      setLoaded(true);
    }
  }, [existing.data, loaded]);

  const current: Opening | undefined = publish.data ?? withdraw.data ?? save.data ?? existing.data;
  const live = current?.live ?? false;

  const setSkill = (slug: string, min_score: number) =>
    setBar({
      ...bar,
      skills: bar.skills.some((s) => s.slug === slug)
        ? bar.skills.map((s) => (s.slug === slug ? { ...s, min_score } : s))
        : [...bar.skills, { slug, min_score }],
    });

  return (
    <Card>
      <SectionTitle note="Published roles are the only thing on this platform a contributor can find on their own. Everything else waits for you to approach them.">
        Advertise this role {live ? <Pill tone="primary">live</Pill> : null}
      </SectionTitle>

      {role.status !== "open" ? (
        <Banner icon="warn">
          Only an open role can be advertised. Open it first — a draft is not a commitment, and a
          closed role is a job nobody is offering.
        </Banner>
      ) : null}

      <p className="field__help">
        A contributor sees this only if they clear every bar below. Leave one blank to ask nothing
        of it — blank is no bar, and 0 is a bar of zero. Somebody with no score at all clears
        nothing you state, because an unjudged contributor has not been measured rather than
        measured low.
      </p>

      <Field
        label="Minimum overall score"
        htmlFor="min_overall"
        help="Depth, 0–100. What their best skills are worth."
      >
        <input
          className="input"
          id="min_overall"
          type="number"
          min={0}
          max={100}
          step="0.1"
          value={bar.min_overall_score ?? ""}
          onChange={(e) => setBar({ ...bar, min_overall_score: num(e.target.value) })}
        />
      </Field>

      <Field
        label="Minimum generalist score"
        htmlFor="min_generalist"
        help="Breadth, and deliberately unbounded — there is no top mark to compare against."
      >
        <input
          className="input"
          id="min_generalist"
          type="number"
          min={0}
          step="0.1"
          value={bar.min_generalist_score ?? ""}
          onChange={(e) => setBar({ ...bar, min_generalist_score: num(e.target.value) })}
        />
      </Field>

      <Field
        label="Minimum years in open source"
        htmlFor="opening_oss"
        help="We check this one, dated from when their first pull request was written. Anybody we could not verify is left out rather than let through."
      >
        <input
          className="input"
          id="opening_oss"
          type="number"
          min={0}
          max={80}
          value={bar.min_oss_yoe ?? ""}
          onChange={(e) =>
            setBar({ ...bar, min_oss_yoe: e.target.value === "" ? null : Number(e.target.value) })
          }
        />
      </Field>

      <SectionTitle note="Only primary standing counts — five distinct merged pull requests. A skill with four is on their scorecard and clears nothing here.">
        Skills, and how good at them
      </SectionTitle>

      <SkillPicker
        id="opening-skills"
        label="Add a skill"
        chosen={bar.skills.map((s) => s.slug)}
        onChange={(slugs) =>
          setBar({
            ...bar,
            skills: slugs.map(
              (slug) => bar.skills.find((s) => s.slug === slug) ?? { slug, min_score: 0 },
            ),
          })
        }
      />

      {bar.skills.map((s: OpeningSkill) => (
        <Field key={s.slug} label={s.name ?? s.slug} htmlFor={`score-${s.slug}`}>
          <input
            className="input"
            id={`score-${s.slug}`}
            type="number"
            min={0}
            max={100}
            step="0.1"
            value={s.min_score}
            onChange={(e) => setSkill(s.slug, Number(e.target.value))}
          />
        </Field>
      ))}

      {existing.error && existing.error.code !== "opening_not_found" ? (
        <Failure message={existing.error.message} onRetry={existing.reload} />
      ) : null}
      {save.error ? <Failure message={save.error.message} /> : null}
      {publish.error ? <Failure message={publish.error.message} /> : null}
      {withdraw.error ? <Failure message={withdraw.error.message} /> : null}

      <div className="od-row">
        <Button
          disabled={save.pending}
          onClick={() => void save.run(bar).then((ok) => ok && existing.reload())}
        >
          {save.pending ? "Saving…" : "Save the bar"}
        </Button>

        {live ? (
          <Button
            disabled={withdraw.pending}
            onClick={() => void withdraw.run().then((ok) => ok && existing.reload())}
          >
            {withdraw.pending ? "Withdrawing…" : "Withdraw it"}
          </Button>
        ) : (
          <Button
            variant="primary"
            disabled={publish.pending || role.status !== "open"}
            onClick={() => void publish.run().then((ok) => ok && existing.reload())}
          >
            {publish.pending ? "Publishing…" : "Publish it"}
          </Button>
        )}
      </div>

      {live ? (
        <p className="field__help">
          Live since {current?.published_at?.slice(0, 10)}. Withdrawing takes it down without
          deleting it, and closing the role takes it down too.
        </p>
      ) : null}
    </Card>
  );
}
