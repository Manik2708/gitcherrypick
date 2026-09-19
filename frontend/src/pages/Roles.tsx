// Openings (ADR-0019).
//
// A role is the first thing a hirer can create that makes a FINANCIAL
// STATEMENT on the company's behalf: a salary, a base, a number of interview
// rounds, sent in writing to everybody contacted and held to afterwards. Three
// things about this screen follow from that and nothing else:
//
//   - a role is created as a DRAFT and opened separately, because opening is
//     the commitment and the organisation may want a different person doing it;
//   - an OPEN role is never edited. The form for one says so before anybody
//     types into it, and offers to publish a new version instead;
//   - closing asks WHY, and hiring through the platform asks WHO.

import { useState } from "react";
import type { CloseReason, Engagement, Role, RoleDraft } from "../contract";
import type { PillTone } from "../ui/primitives";
import {
  useCloseRole,
  useCreateRole,
  useOpenRole,
  useOrgID,
  useOrgSettings,
  useReviseRole,
  useRoleCandidates,
  useRoles,
} from "../hooks/hirer";
import * as format from "../logic/format";
import { OpeningForm } from "../ui/OpeningForm";
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
  PageHead,
  Pill,
  Radio,
  SectionTitle,
} from "../ui/primitives";

const ENGAGEMENTS: Array<{ value: Engagement; label: string }> = [
  { value: "full_time", label: "Full time" },
  { value: "contract", label: "Contract" },
  { value: "internship", label: "Internship" },
  { value: "freelance", label: "Freelance" },
];

const BLANK: RoleDraft = {
  title: "",
  description: "",
  engagement: "full_time",
  location: "remote",
  currency: "",
  yearly_ctc: null,
  yearly_base: null,
  hourly_rate: null,
  expected_hours: null,
  eligible_countries: [],
  min_office_yoe: null,
  min_oss_yoe: null,
  requires_online_test: null,
  max_interview_rounds: null,
  avg_days_to_offer: null,
  questions: [],
};

/** Minor units on the wire, a decimal on the screen. */
function toMajor(minor: number | null): string {
  return minor == null ? "" : String(minor / 100);
}

function toMinor(major: string): number | null {
  if (major.trim() === "") return null;
  return Math.round(Number(major) * 100);
}

function money(minor: number | null, currency: string): string | null {
  if (minor == null) return null;
  return `${currency} ${(minor / 100).toLocaleString()}`;
}

function draftOf(role: Role): RoleDraft {
  return {
    title: role.title,
    description: role.description,
    engagement: role.engagement,
    location: role.location,
    currency: role.currency,
    yearly_ctc: role.yearly_ctc,
    yearly_base: role.yearly_base,
    hourly_rate: role.hourly_rate,
    expected_hours: role.expected_hours,
    eligible_countries: role.eligible_countries,
    min_office_yoe: role.min_office_yoe,
    min_oss_yoe: role.min_oss_yoe,
    requires_online_test: role.requires_online_test,
    max_interview_rounds: role.max_interview_rounds,
    avg_days_to_offer: role.avg_days_to_offer,
    questions: role.questions,
  };
}

export function RolesPage() {
  const org = useOrgID();
  const roles = useRoles(org.id);
  const settings = useOrgSettings(org.id);

  const [composing, setComposing] = useState(false);
  const [revising, setRevising] = useState<Role | null>(null);

  if (org.loading || (roles.loading && !roles.data)) {
    return (
      <div className="page">
        <PageHead eyebrow="Hiring" title="Roles" />
        <Loading what="your roles" />
      </div>
    );
  }

  if (!org.id) {
    return (
      <div className="page">
        <PageHead eyebrow="Hiring" title="Roles" />
        <Empty title="No organisation" icon="people">
          Roles belong to a company. An independent hirer has no organisation to hang one on yet.
        </Empty>
      </div>
    );
  }

  const all = roles.data?.roles ?? [];
  const open = all.filter((r) => r.status === "open");
  const drafts = all.filter((r) => r.status === "draft");
  const closed = all.filter((r) => r.status === "closed");

  // Stated as the RULE rather than guessed at the reader: the sign-in response
  // carries no org role — that shape is pinned by the approved fixtures — so a
  // screen that tried to say "you personally cannot do this" would be inferring
  // it. The policy is true for everybody, and an owner reading it learns what
  // their own setting does.
  const approvalRule = settings.data?.role_create_authority ?? "draft_and_approve";

  return (
    <div className="page">
      <PageHead
        eyebrow="Hiring"
        title="Roles"
        lede="A role is what a round is for. Everybody you contact sees its salary, where it is, and what your process looks like — so it is written once and never quietly edited afterwards."
      />

      {roles.error ? <Failure message={roles.error.message} onRetry={roles.reload} /> : null}

      {approvalRule !== "any_hirer" ? (
        <Banner icon="warn">
          {approvalRule === "owners_only"
            ? "Only an owner can write or open a role here."
            : "Anybody with a seat can write a role, and an owner opens it."}{" "}
          A role states a salary and a process on the organisation's behalf, in writing, to
          everybody contacted — so somebody accountable signs it.
        </Banner>
      ) : null}

      {composing || revising ? (
        <RoleForm
          orgId={org.id}
          revising={revising}
          onDone={() => {
            setComposing(false);
            setRevising(null);
            roles.reload();
          }}
          onCancel={() => {
            setComposing(false);
            setRevising(null);
          }}
        />
      ) : (
        <Card>
          <Button variant="primary" onClick={() => setComposing(true)}>
            <Icon name="plus" size="sm" />
            Write a role
          </Button>
        </Card>
      )}

      {all.length === 0 ? (
        <Empty title="No roles yet" icon="bookmark">
          A round can only approach people for a job you have committed to. Write one, and rounds
          will have something to be for.
        </Empty>
      ) : null}

      {(
        [
          ["Open", open],
          ["Draft — nobody has seen these", drafts],
          ["Closed", closed],
        ] as Array<[string, Role[]]>
      ).map(([title, list]) =>
        list.length > 0 ? (
          <div key={title}>
            <SectionTitle>{title}</SectionTitle>
            {list.map((role) => (
              <RoleCard
                key={role.id}
                orgId={org.id as string}
                role={role}
                onChanged={() => roles.reload()}
                onRevise={() => setRevising(role)}
              />
            ))}
          </div>
        ) : null,
      )}
    </div>
  );
}

function RoleCard({
  orgId,
  role,
  onChanged,
  onRevise,
}: {
  orgId: string;
  role: Role;
  onChanged: () => void;
  onRevise: () => void;
}) {
  const openRole = useOpenRole(orgId);
  const [closing, setClosing] = useState(false);
  const [advertising, setAdvertising] = useState(false);
  const [showing, setShowing] = useState(false);

  const pay =
    role.engagement === "freelance"
      ? money(role.hourly_rate, role.currency)
      : money(role.yearly_ctc, role.currency);

  return (
    <Card>
      <div className="section__head">
        <h3 className="section__title">{role.title}</h3>
        <span className="od-row" style={{ ["--od-gap" as string]: "6px" }}>
          {/* "Public" answers the question a hirer has first and could not
              previously ask of the list at all: which of these can a
              contributor actually see? */}
          {role.advertised ? <Pill tone="primary">public</Pill> : null}
          <Pill
            tone={
              role.status === "closed" ? "stale" : role.status === "draft" ? "secondary" : "primary"
            }
          >
            {role.status}
          </Pill>
        </span>
      </div>

      <p className="small muted">
        {ENGAGEMENTS.find((e) => e.value === role.engagement)?.label} ·{" "}
        {role.location === "remote" ? "Remote" : "At an office"}
        {pay ? ` · ${pay}${role.engagement === "freelance" ? " / hour" : " a year"}` : ""}
      </p>

      {role.description ? <p className="small">{role.description}</p> : null}

      <p className="field__help">
        {role.eligible_countries.length === 0
          ? "Open to anybody, anywhere."
          : `Can hire in ${role.eligible_countries.join(", ")}.`}
        {role.min_oss_yoe != null
          ? ` At least ${role.min_oss_yoe} years contributing to open source.`
          : ""}
      </p>

      {role.close_requested_at && role.status === "open" ? (
        <Banner icon="warn">
          Somebody asked to close this on {format.date(role.close_requested_at)}. It is still open,
          still matching, and still a commitment until an owner withdraws it.
        </Banner>
      ) : null}

      {role.supersedes ? (
        <p className="field__help">This replaced an earlier version of the role.</p>
      ) : null}

      {role.status === "closed" ? (
        <p className="field__help">
          Closed {role.closed_at ? format.date(role.closed_at) : ""} —{" "}
          {role.close_reason === "superseded"
            ? "replaced by a new version"
            : role.close_reason === "hired_via_platform"
              ? `filled from here (${role.hires.length} ${role.hires.length === 1 ? "person" : "people"})`
              : role.close_reason === "hired_elsewhere"
                ? "filled another way"
                : role.close_reason === "not_needed"
                  ? "no longer needed"
                  : role.close_note || "other"}
        </p>
      ) : null}

      {openRole.error ? <Failure message={openRole.error.message} /> : null}

      <div className="od-row">
        {role.status === "draft" ? (
          <Button
            variant="primary"
            disabled={openRole.pending}
            onClick={() => void openRole.run(role.id).then((ok) => ok && onChanged())}
          >
            {openRole.pending ? "Opening…" : "Open it"}
          </Button>
        ) : null}

        {role.status === "open" ? (
          <>
            {/* An open role is never edited. Publishing a new version is what
                change means here, and everybody already contacted keeps reading
                the one they were shown. */}
            <Button onClick={onRevise}>Publish a new version</Button>

            {/* Advertising it. Alongside the other actions rather than below
                them: a button on its own under a row of buttons reads as
                belonging to whatever comes next, and this one was being
                missed entirely. Only on an OPEN role — a draft is not a
                commitment and a closed one is a withdrawn job. */}
            <Button onClick={() => setAdvertising(!advertising)}>
              {role.advertised
                ? "Edit the public advert"
                : advertising
                  ? "Hide the advert form"
                  : "Advertise it publicly"}
            </Button>

            <Button onClick={() => setClosing(true)}>Close it</Button>
          </>
        ) : null}

        {role.status !== "draft" ? (
          <Button onClick={() => setShowing(!showing)}>
            {showing ? "Hide who is on it" : "Who is on it"}
          </Button>
        ) : null}
      </div>

      {advertising && role.status === "open" ? <OpeningForm orgId={orgId} role={role} /> : null}

      {/* Who has already been approached for this job. Read before searching
          again — and the search itself now stops offering them. */}
      {role.status !== "draft" ? showing ? <Candidates orgId={orgId} role={role} /> : null : null}

      {closing ? (
        <CloseForm
          orgId={orgId}
          role={role}
          onDone={() => {
            setClosing(false);
            onChanged();
          }}
          onCancel={() => setClosing(false)}
        />
      ) : null}
    </Card>
  );
}

/**
 * What each state means, said as a sentence rather than as a database value.
 *
 * The empty one is the case worth spelling out: a candidate with no contact
 * status has been STAGED — put on a round that has not been confirmed — so
 * nothing has been sent and they do not know they were considered. "staged"
 * on its own reads as jargon, and worse, it reads as though something already
 * happened to them.
 */
const CANDIDATE_STATE: Record<string, { label: string; tone: PillTone }> = {
  "": { label: "not contacted yet", tone: "secondary" },
  pending: { label: "waiting for their answer", tone: "secondary" },
  accepted: { label: "accepted", tone: "primary" },
  declined: { label: "declined", tone: "stale" },
  expired: { label: "never answered", tone: "stale" },
};

/**
 * Everybody already on a round for this role.
 *
 * One row per PERSON: the same contributor staged on two rounds for one job
 * has been approached once, and showing them twice would make six people read
 * as nine approaches.
 *
 * No addresses. The hirer who needs one has it from the contact request
 * itself, and a list read in bulk is the wrong place to hand them out.
 */
function Candidates({ orgId, role }: { orgId: string; role: Role }) {
  const candidates = useRoleCandidates(orgId, role.id);
  const list = candidates.data?.candidates ?? [];

  return (
    <Card>
      <SectionTitle note="Searching again for this role will not offer these people back. Anyone not contacted yet can still be removed from their round — nothing has been sent.">
        Already on this role
      </SectionTitle>

      {candidates.loading ? <Loading what="candidates" /> : null}
      {candidates.error ? (
        <Failure message={candidates.error.message} onRetry={candidates.reload} />
      ) : null}

      {!candidates.loading && list.length === 0 ? (
        <Empty title="Nobody yet" icon="people">
          Stage someone from search onto a round for this role and they will appear here.
        </Empty>
      ) : null}

      {list.map((c) => (
        <div className="od-row" key={c.user_id} style={{ ["--od-gap" as string]: "10px" }}>
          <span className="od-field od-fill">
            <span>{c.display_name}</span>
            <span className="field__help">on {c.shortlist_name}</span>
          </span>
          <Pill tone={CANDIDATE_STATE[c.contact_status]?.tone ?? "secondary"}>
            {CANDIDATE_STATE[c.contact_status]?.label ?? c.contact_status}
          </Pill>
        </div>
      ))}
    </Card>
  );
}

/* --- closing ---------------------------------------------------------------- */

const REASONS: Array<{ value: CloseReason; label: string; help: string }> = [
  {
    value: "hired_via_platform",
    label: "We hired somebody from here",
    help: "The one answer that tells us whether any of this works.",
  },
  {
    value: "hired_elsewhere",
    label: "We filled it another way",
    help: "Worth knowing, and not a consolation answer.",
  },
  { value: "not_needed", label: "We do not need the headcount", help: "" },
  { value: "other", label: "Something else", help: "Tell us what, below." },
];

function CloseForm({
  orgId,
  role,
  onDone,
  onCancel,
}: {
  orgId: string;
  role: Role;
  onDone: () => void;
  onCancel: () => void;
}) {
  const close = useCloseRole(orgId);
  const candidates = useRoleCandidates(orgId, role.id);
  const [reason, setReason] = useState<CloseReason>("hired_via_platform");
  const [note, setNote] = useState("");
  const [hired, setHired] = useState<string[]>([]);

  // Only somebody who ACCEPTED can be recorded as a hire. A staged candidate
  // has been told nothing, a notified one has not answered, and a declined one
  // said no — recording any of them would be the company asserting something
  // about a person who never agreed to talk to it.
  const acceptable = (candidates.data?.candidates ?? []).filter((c) => c.accepted);

  return (
    <Card>
      <SectionTitle note="A role that just stops teaches nobody anything.">
        Why are you closing this?
      </SectionTitle>

      {REASONS.map((r) => (
        <Radio
          key={r.value}
          name={`close-${role.id}`}
          label={r.label}
          help={r.help || undefined}
          checked={reason === r.value}
          onChange={() => setReason(r.value)}
        />
      ))}

      {reason === "hired_via_platform" ? (
        <>
          <SectionTitle note="Only people who accepted a contact request for this role. A role can fill several seats, so tick everybody.">
            Who did you hire?
          </SectionTitle>

          {candidates.loading ? <Loading what="candidates" /> : null}
          {candidates.error ? <Failure message={candidates.error.message} /> : null}

          {!candidates.loading && acceptable.length === 0 ? (
            <Banner icon="warn">
              Nobody has accepted a contact request for this role yet, so there is no one we can
              record as hired through the platform. If you found someone another way, close it as
              hired elsewhere.
            </Banner>
          ) : null}

          {acceptable.map((c) => (
            <Checkbox
              key={c.user_id}
              label={c.display_name}
              help={`accepted · ${c.shortlist_name}`}
              checked={hired.includes(c.user_id)}
              onChange={(on) =>
                setHired(on ? [...hired, c.user_id] : hired.filter((id) => id !== c.user_id))
              }
            />
          ))}
        </>
      ) : null}

      {reason === "other" ? (
        <Field label="What happened?" htmlFor={`note-${role.id}`}>
          <input
            className="input"
            id={`note-${role.id}`}
            value={note}
            onChange={(event) => setNote(event.target.value)}
          />
        </Field>
      ) : null}

      {close.error ? <Failure message={close.error.message} /> : null}

      <div className="od-row">
        <Button
          variant="primary"
          disabled={
            close.pending ||
            (reason === "other" && !note.trim()) ||
            (reason === "hired_via_platform" && hired.length === 0)
          }
          onClick={() => void close.run(role.id, reason, note, hired).then((ok) => ok && onDone())}
        >
          {close.pending ? "Closing…" : "Close the role"}
        </Button>
        <Button onClick={onCancel}>Cancel</Button>
      </div>
    </Card>
  );
}

/* --- writing one ------------------------------------------------------------ */

function RoleForm({
  orgId,
  revising,
  onDone,
  onCancel,
}: {
  orgId: string;
  revising: Role | null;
  onDone: () => void;
  onCancel: () => void;
}) {
  const create = useCreateRole(orgId);
  const revise = useReviseRole(orgId, revising?.id ?? "");
  const action = revising ? revise : create;

  const [form, setForm] = useState<RoleDraft>(revising ? draftOf(revising) : BLANK);
  const [countries, setCountries] = useState(
    revising ? revising.eligible_countries.join(", ") : "",
  );

  const set = <K extends keyof RoleDraft>(key: K, value: RoleDraft[K]) =>
    setForm({ ...form, [key]: value });

  const freelance = form.engagement === "freelance";

  const submit = () => {
    const draft: RoleDraft = {
      ...form,
      eligible_countries: countries
        .split(",")
        .map((c) => c.trim().toUpperCase())
        .filter(Boolean),
    };
    void action.run(draft).then((ok) => ok && onDone());
  };

  return (
    <Card>
      <SectionTitle
        note={
          revising
            ? "This publishes a NEW role and closes the current one. Everybody you have already contacted keeps reading the version they were shown — the salary somebody agreed to talk about cannot change under them."
            : "Saved as a draft. Nobody sees it until it is opened."
        }
      >
        {revising ? "Publish a new version" : "Write a role"}
      </SectionTitle>

      <Field label="Title" htmlFor="title">
        <input
          className="input"
          id="title"
          value={form.title}
          onChange={(event) => set("title", event.target.value)}
        />
      </Field>

      <Field label="Description" htmlFor="description">
        <textarea
          className="input"
          id="description"
          rows={3}
          value={form.description}
          onChange={(event) => set("description", event.target.value)}
        />
      </Field>

      <Field label="Kind of work" htmlFor="engagement">
        <select
          className="input"
          id="engagement"
          value={form.engagement}
          onChange={(event) => set("engagement", event.target.value as Engagement)}
        >
          {ENGAGEMENTS.map((e) => (
            <option key={e.value} value={e.value}>
              {e.label}
            </option>
          ))}
        </select>
      </Field>

      <Field
        label="Currency"
        htmlFor="currency"
        help="Three letters, such as GBP. Any amount needs one — a number without a currency cannot be compared against what somebody expects."
      >
        <input
          className="input"
          id="currency"
          maxLength={3}
          value={form.currency}
          onChange={(event) => set("currency", event.target.value.toUpperCase())}
        />
      </Field>

      {freelance ? (
        <>
          <Field label="Hourly rate" htmlFor="hourly">
            <input
              className="input"
              id="hourly"
              type="number"
              min={0}
              step="0.01"
              value={toMajor(form.hourly_rate)}
              onChange={(event) => set("hourly_rate", toMinor(event.target.value))}
            />
          </Field>
          <Field label="Expected hours a week" htmlFor="hours">
            <input
              className="input"
              id="hours"
              type="number"
              min={1}
              max={168}
              value={form.expected_hours ?? ""}
              onChange={(event) =>
                set("expected_hours", event.target.value === "" ? null : Number(event.target.value))
              }
            />
          </Field>
        </>
      ) : (
        <>
          <Field label="Total package a year" htmlFor="ctc" help="Everything included.">
            <input
              className="input"
              id="ctc"
              type="number"
              min={0}
              step="0.01"
              value={toMajor(form.yearly_ctc)}
              onChange={(event) => set("yearly_ctc", toMinor(event.target.value))}
            />
          </Field>
          <Field
            label="Base a year"
            htmlFor="base"
            help="What arrives monthly. Stated separately because it is the distinction candidates most need and most often are not given."
          >
            <input
              className="input"
              id="base"
              type="number"
              min={0}
              step="0.01"
              value={toMajor(form.yearly_base)}
              onChange={(event) => set("yearly_base", toMinor(event.target.value))}
            />
          </Field>
        </>
      )}

      <Field label="Where the work happens" htmlFor="location">
        <select
          className="input"
          id="location"
          value={form.location}
          onChange={(event) => set("location", event.target.value as RoleDraft["location"])}
        >
          <option value="remote">Remote</option>
          <option value="address">At one of our offices</option>
        </select>
      </Field>

      <Field
        label="Countries you can hire in"
        htmlFor="countries"
        help="Two-letter codes, separated by commas. LEAVE BLANK FOR ANYWHERE — a list you have not thought about would quietly exclude the world."
      >
        <input
          className="input"
          id="countries"
          placeholder="GB, DE, IN"
          value={countries}
          onChange={(event) => setCountries(event.target.value)}
        />
      </Field>

      <Field label="Least years in a job" htmlFor="office_yoe" help="Blank for no minimum.">
        <input
          className="input"
          id="office_yoe"
          type="number"
          min={0}
          max={80}
          value={form.min_office_yoe ?? ""}
          onChange={(event) =>
            set("min_office_yoe", event.target.value === "" ? null : Number(event.target.value))
          }
        />
      </Field>

      <Field
        label="Least years contributing to open source"
        htmlFor="oss_yoe"
        help="We check this one. It is dated from when somebody's first pull request was written, not from when it was merged — so it says when they started, not how fast a maintainer replied."
      >
        <input
          className="input"
          id="oss_yoe"
          type="number"
          min={0}
          max={80}
          value={form.min_oss_yoe ?? ""}
          onChange={(event) =>
            set("min_oss_yoe", event.target.value === "" ? null : Number(event.target.value))
          }
        />
      </Field>

      <SectionTitle note="Shown to the people you contact, and to nobody else. Not on a scorecard, not in search, not on the public picker.">
        Your process
      </SectionTitle>

      <Checkbox
        label="There is an online test"
        checked={form.requires_online_test === true}
        onChange={(on) => set("requires_online_test", on)}
      />

      <Field label="Most interview rounds" htmlFor="rounds">
        <input
          className="input"
          id="rounds"
          type="number"
          min={1}
          value={form.max_interview_rounds ?? ""}
          onChange={(event) =>
            set(
              "max_interview_rounds",
              event.target.value === "" ? null : Number(event.target.value),
            )
          }
        />
      </Field>

      <Field
        label="Usual days from first round to an offer"
        htmlFor="days"
        help="Leave blank rather than inventing one. Somebody will hold you to it."
      >
        <input
          className="input"
          id="days"
          type="number"
          min={0}
          value={form.avg_days_to_offer ?? ""}
          onChange={(event) =>
            set("avg_days_to_offer", event.target.value === "" ? null : Number(event.target.value))
          }
        />
      </Field>

      {action.error ? <Failure message={action.error.message} /> : null}

      <div className="od-row">
        <Button variant="primary" disabled={action.pending || !form.title.trim()} onClick={submit}>
          {action.pending ? "Saving…" : revising ? "Publish it" : "Save as a draft"}
        </Button>
        <Button onClick={onCancel}>Cancel</Button>
      </div>
    </Card>
  );
}
