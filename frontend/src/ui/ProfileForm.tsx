// What a contributor wants, as opposed to what they have built (ADR-0018).
//
// Two forms, kept apart on purpose. The first says what shape of work someone
// will take; the second says what they expect to be paid, which no hirer ever
// sees. Splitting them at the screen mirrors the split in the schema and in the
// API: a person updating their availability preferences never sends a salary.

import { useEffect, useState } from "react";
import {
  useMyCompensation,
  useMyProfile,
  useSaveCompensation,
  useSaveProfile,
} from "../hooks/contributor";
import type { CompensationExpectation, WorkPreferences } from "../contract";
import { CountryPicker } from "./countries";
import {
  Banner,
  Button,
  Card,
  Checkbox,
  Failure,
  Field,
  Loading,
  SectionTitle,
} from "./primitives";

const SHAPES: Array<{ key: keyof WorkPreferences; label: string; help: string }> = [
  { key: "open_to_remote", label: "Remote roles", help: "Working from where you are." },
  { key: "open_to_onsite", label: "Onsite roles", help: "Working from an employer's office." },
  {
    key: "open_to_contract",
    label: "Contract roles",
    help: "A fixed term rather than a permanent position.",
  },
  {
    key: "open_to_internships",
    label: "Internships",
    help: "Including placements and apprenticeships.",
  },
];

const EMPTY: WorkPreferences = {
  open_to_remote: false,
  open_to_internships: false,
  open_to_onsite: false,
  open_to_contract: false,
  current_country: "",
  office_yoe: null,
  first_pr_url: "",
  latest_pr_url: "",
};

export function ProfileForm() {
  const profile = useMyProfile();
  const save = useSaveProfile();

  const [form, setForm] = useState<WorkPreferences>(EMPTY);
  const [loaded, setLoaded] = useState(false);

  // Load once. Re-syncing on every render would discard what somebody was
  // halfway through typing every time the read refreshed.
  useEffect(() => {
    if (profile.data && !loaded) {
      setForm(profile.data.preferences);
      setLoaded(true);
    }
  }, [profile.data, loaded]);

  const shown = save.data ?? profile.data;

  // Write-once. Locked as soon as there is a value on the server, so the field
  // says what it will do BEFORE somebody types into it and gets a 409.
  const locked = (shown?.preferences.first_pr_url ?? "") !== "";
  const anyShape = SHAPES.some((s) => form[s.key] === true);

  // The window is live but nothing is ticked. A person in this state believes
  // they are findable and is invisible to every role — and there is no way to
  // tell from outside, which is why it is said here rather than discovered by
  // nobody ever writing (ADR-0018).
  const looking = (shown?.availability?.status ?? "not_looking") !== "not_looking";
  const unmatchable = looking && !anyShape;

  if (profile.loading && !loaded) return <Loading what="your profile" />;

  return (
    <>
      <SectionTitle note="Availability says whether you are looking. This says what you would take — you are available for the shapes you tick, and for nothing else.">
        What you are open to
      </SectionTitle>

      {profile.error ? <Failure message={profile.error.message} onRetry={profile.reload} /> : null}
      {save.error ? <Failure message={save.error.message} /> : null}

      <Card>
        {unmatchable ? (
          <Banner icon="warn">
            You are marked as looking, but you have not said what kind of work you would take — so
            no role can reach you. Tick at least one below.
          </Banner>
        ) : null}

        {SHAPES.map((shape) => (
          <Checkbox
            key={shape.key}
            label={shape.label}
            help={shape.help}
            checked={form[shape.key] === true}
            onChange={(on) => setForm({ ...form, [shape.key]: on })}
          />
        ))}

        <p className="field__help">
          Freelance work is not here: it is part of your availability above, so that one answer
          cannot contradict another.
        </p>
      </Card>

      <Card>
        {/* The same picker a hirer searches with (ADR-0018 §9). One
            vocabulary, or the two never match. */}
        <CountryPicker
          id="country"
          label="Where you are"
          help="Used to match roles that can only hire in certain countries."
          value={form.current_country}
          onChange={(code) => setForm({ ...form, current_country: code })}
        />

        <Field
          label="Years you have worked in a job"
          htmlFor="office_yoe"
          help="Your own figure. Shown to hirers as something you said, not as something we checked."
        >
          <input
            className="input"
            id="office_yoe"
            type="number"
            min={0}
            max={80}
            value={form.office_yoe ?? ""}
            onChange={(e) =>
              setForm({
                ...form,
                office_yoe: e.target.value === "" ? null : Number(e.target.value),
              })
            }
          />
        </Field>

        {/* Asked, not read off a claim. A first contribution is often years
            old in a repository nobody claimed here, and a claim is five PRs
            somebody chose as their BEST — not their earliest. */}
        <Field
          label="Your first pull request"
          htmlFor="first_pr"
          help={
            locked
              ? "Recorded. When you started is a fact about the past, so this one cannot be changed."
              : "A link to the earliest merged pull request you are proud of. It does not have to be one you have claimed here — and once saved it cannot be changed."
          }
        >
          <input
            className="input"
            id="first_pr"
            placeholder="https://github.com/owner/repo/pull/1"
            readOnly={locked}
            value={form.first_pr_url}
            onChange={(e) => setForm({ ...form, first_pr_url: e.target.value })}
          />
        </Field>

        <Field
          label="Your latest pull request"
          htmlFor="latest_pr"
          help="Keep this current. Unlike the first one, it is meant to change."
        >
          <input
            className="input"
            id="latest_pr"
            placeholder="https://github.com/owner/repo/pull/128"
            value={form.latest_pr_url}
            onChange={(e) => setForm({ ...form, latest_pr_url: e.target.value })}
          />
        </Field>

        <Button variant="primary" disabled={save.pending} onClick={() => void save.run(form)}>
          {save.pending ? "Saving…" : "Save"}
        </Button>
      </Card>

      <CompensationForm />
    </>
  );
}

/* --- what you expect to be paid ------------------------------------------- */

const NO_PAY: CompensationExpectation = {
  currency: "",
  hourly_rate: null,
  yearly_amount: null,
};

/** Minor units on the wire, a decimal on the screen. */
function toMajor(minor: number | null): string {
  return minor == null ? "" : String(minor / 100);
}

function toMinor(major: string): number | null {
  if (major.trim() === "") return null;
  return Math.round(Number(major) * 100);
}

function CompensationForm() {
  const pay = useMyCompensation();
  const save = useSaveCompensation();

  const [form, setForm] = useState<CompensationExpectation>(NO_PAY);
  const [loaded, setLoaded] = useState(false);

  useEffect(() => {
    if (pay.data && !loaded) {
      setForm(pay.data);
      setLoaded(true);
    }
  }, [pay.data, loaded]);

  return (
    <>
      <SectionTitle>What you expect to be paid</SectionTitle>

      {pay.error ? <Failure message={pay.error.message} onRetry={pay.reload} /> : null}
      {save.error ? <Failure message={save.error.message} /> : null}

      <Card>
        <Banner icon="lock">
          <b>No hirer ever sees this.</b> It is used to keep roles that pay less than you asked for
          out of your way, and nothing else. A company that knew what you would accept could offer
          exactly that and no more, which is why we do not tell them.
        </Banner>

        <Field label="Currency" htmlFor="currency" help="Three letters, such as GBP, USD or INR.">
          <input
            className="input"
            id="currency"
            maxLength={3}
            value={form.currency}
            onChange={(e) => setForm({ ...form, currency: e.target.value.toUpperCase() })}
          />
        </Field>

        <Field label="Hourly rate" htmlFor="hourly">
          <input
            className="input"
            id="hourly"
            type="number"
            min={0}
            step="0.01"
            value={toMajor(form.hourly_rate)}
            onChange={(e) => setForm({ ...form, hourly_rate: toMinor(e.target.value) })}
          />
        </Field>

        <Field label="Yearly" htmlFor="yearly">
          <input
            className="input"
            id="yearly"
            type="number"
            min={0}
            step="0.01"
            value={toMajor(form.yearly_amount)}
            onChange={(e) => setForm({ ...form, yearly_amount: toMinor(e.target.value) })}
          />
        </Field>

        <p className="field__help">
          Fill in whichever applies. A freelancer may give an hourly rate and no yearly figure;
          somebody after a permanent role, the reverse.
        </p>

        <Button variant="primary" disabled={save.pending} onClick={() => void save.run(form)}>
          {save.pending ? "Saving…" : "Save"}
        </Button>
      </Card>
    </>
  );
}
