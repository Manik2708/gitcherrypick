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
import { Link } from "react-router-dom";
import type {
  AddressesResponse,
  CloseReason,
  Engagement,
  LocationRequest,
  OrgAddress,
  Role,
  RoleCandidate,
  RoleDraft,
  RoleStatus,
} from "../contract";
import {
  useAddresses,
  useCloseRole,
  useContactCandidates,
  useCreateRole,
  useOpenRole,
  useOrgID,
  useOrgSettings,
  useReviseRole,
  useRoleCandidates,
  useRoles,
  useSeatRole,
  useUnstageCandidate,
} from "../hooks/hirer";
import * as format from "../logic/format";
import type { RoleProblem } from "../logic/roleDraft";
import { draftProblem, mayOpen, messageFor, problemFrom } from "../logic/roleDraft";
import type { LocationChoice } from "../logic/roleLocations";
import {
  DEFAULT_CHOICE,
  addressLine,
  choiceOf,
  describeForHirer,
  locationsOf,
} from "../logic/roleLocations";
import { CountryList, useCountryNames } from "../ui/countries";
import { CurrencyPicker } from "../ui/currencies";
import { OpeningForm } from "../ui/OpeningForm";
import type { Stage } from "../ui/stages";
import { StageToggles, inStages, stageLook, stageOf } from "../ui/stages";
import {
  Banner,
  Button,
  Card,
  Checkbox,
  Empty,
  Failure,
  Field,
  FieldError,
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

const TABS: Array<[RoleStatus, string]> = [
  ["open", "Open"],
  ["draft", "Drafts"],
  ["closed", "Closed"],
];

/**
 * Candidates whose name, GitHub login or note contains every word typed.
 *
 * The same every-word rule as the role search above, and for the same reason:
 * somebody types what they remember, not a field verbatim.
 */
function matchingCandidates(people: RoleCandidate[], query: string): RoleCandidate[] {
  const words = query.toLowerCase().split(/\s+/).filter(Boolean);
  if (words.length === 0) return people;
  return people.filter((c) => {
    const haystack = `${c.display_name} ${c.github_login ?? ""} ${c.note ?? ""}`.toLowerCase();
    return words.every((word) => haystack.includes(word));
  });
}

/**
 * Roles whose title or description contains every word typed.
 *
 * Every word rather than the whole phrase, so "senior go" finds "Senior
 * platform engineer — Go and Kubernetes". A hirer types what they remember
 * about a role, not its title verbatim.
 */
function matching(roles: Role[], query: string): Role[] {
  const words = query.toLowerCase().split(/\s+/).filter(Boolean);
  if (words.length === 0) return roles;
  return roles.filter((role) => {
    const haystack = `${role.title} ${role.description}`.toLowerCase();
    return words.every((word) => haystack.includes(word));
  });
}

const BLANK: RoleDraft = {
  title: "",
  description: "",
  engagement: "full_time",
  locations: [],
  currency: "",
  yearly_ctc: null,
  yearly_base: null,
  hourly_rate: null,
  expected_hours: null,
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
    locations: locationsOf(choiceOf(role.locations)),
    currency: role.currency,
    yearly_ctc: role.yearly_ctc,
    yearly_base: role.yearly_base,
    hourly_rate: role.hourly_rate,
    expected_hours: role.expected_hours,
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
  // Once for the page, not once per card: each lookup is a request.
  const nameOf = useCountryNames();

  const [composing, setComposing] = useState(false);

  // Which statuses to show. A SET rather than one tab: a hirer comparing what
  // they have open against what they drafted wants both at once, and being
  // made to flick between them is the thing a filter should spare them.
  //
  // Open alone to begin with, because it is the only status anybody acts on.
  const [statuses, setStatuses] = useState<Set<RoleStatus>>(new Set(["open"]));

  // Searched over the roles ALREADY FETCHED rather than by asking the server.
  // An organisation has tens of roles, not thousands, and the page holds all
  // of them — a round trip per keystroke would buy nothing and lose the
  // instant feedback that makes a search box worth having.
  const [query, setQuery] = useState("");

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

  // No status ticked means NO FILTER, not an empty list. A control that can be
  // switched into showing nothing at all is one a hirer will switch into by
  // accident and read as the page being broken.
  const inScope = statuses.size === 0 ? all : all.filter((r) => statuses.has(r.status));
  const shown = matching(inScope, query);

  return (
    <div className="page">
      <PageHead eyebrow="Hiring" title="Roles" />

      {roles.error ? <Failure message={roles.error.message} onRetry={roles.reload} /> : null}

      {/* Only WRITING a role takes the page. Revising one is a panel on the
          role it revises, like advertising and closing it — the form is about
          that role, so it belongs under it rather than on a screen of its own
          that hides every other role. */}
      {composing ? (
        <RoleForm
          orgId={org.id}
          revising={null}
          onDone={() => {
            setComposing(false);
            roles.reload();
          }}
          onCancel={() => setComposing(false)}
        />
      ) : null}

      {all.length === 0 ? (
        <Empty title="No roles yet" icon="bookmark">
          A round can only approach people for a job you have committed to.
        </Empty>
      ) : null}

      {/* One status at a time, rather than three stacked lists. A company with
          a dozen roles had every draft and every closed one between them and
          the open role they came to look at — the thing they want is almost
          always open, and everything else is history. */}
      {/* One bar: the view on the left, the action on the right. The create
          button used to sit in a card of its own above the lists, which put a
          whole row between a hirer and the roles they came to read. */}
      {!composing ? (
        <Card>
          <div className="od-row" style={{ justifyContent: "space-between" }}>
            {all.length > 0 ? (
              // od-fill so the group takes the row's leftover width, and the
              // search box inside it takes what the toggles do not. The
              // toggles are a fixed size; the box is the part that should
              // grow, because a role title is longer than "Drafts".
              <div className="od-row od-fill">
                {/* aria-PRESSED, not aria-selected: these are toggles now, and
                    several can be on at once. The stylesheet already matched
                    both, so the look is unchanged. */}
                {/* flex:none so the SEARCH BOX absorbs every change in width.
                    Without it the toggles shrink first on a narrow window and
                    "Drafts" starts wrapping, which is the one part of this row
                    that must stay legible. */}
                <div className="segmented" style={{ flex: "none" }}>
                  {TABS.map(([key, label]) => (
                    <button
                      key={key}
                      type="button"
                      aria-pressed={statuses.has(key)}
                      onClick={() => {
                        const next = new Set(statuses);
                        if (next.has(key)) {
                          next.delete(key);
                        } else {
                          next.add(key);
                        }
                        setStatuses(next);
                      }}
                    >
                      {label}
                    </button>
                  ))}
                </div>

                <div className="search-input od-fill">
                  <Icon name="search" size="sm" />
                  <input
                    className="input"
                    type="search"
                    autoComplete="off"
                    aria-label="Search roles"
                    placeholder="Search roles"
                    value={query}
                    onChange={(event) => setQuery(event.target.value)}
                  />
                </div>
              </div>
            ) : (
              // Nothing to switch between, but the row still has two ends —
              // without a spacer the button would drift left and land
              // somewhere different from where it sits the rest of the time.
              <span />
            )}

            <Button variant="primary" onClick={() => setComposing(true)}>
              <Icon name="plus" size="sm" />
              Write a role
            </Button>
          </div>
        </Card>
      ) : null}

      {/* WRITING A ROLE REPLACES THE LIST. The form used to appear above it
          with every role still stacked underneath, so switching to it looked
          like nothing had happened until you scrolled — and the thing you had
          moved on from was still on screen. */}
      {!composing && shown.length === 0 && all.length > 0 ? (
        <Empty
          title={query.trim() ? `Nothing matches “${query.trim()}”` : "Nothing in that view"}
          icon="bookmark"
        >
          {query.trim()
            ? "Titles and descriptions are searched. Check the statuses you have ticked."
            : "Tick another status above."}
        </Empty>
      ) : null}

      {(composing ? [] : shown).map((role) => (
        <RoleCard
          key={role.id}
          orgId={org.id as string}
          role={role}
          nameOf={nameOf}
          onChanged={() => roles.reload()}
        />
      ))}
    </div>
  );
}

function RoleCard({
  orgId,
  role,
  nameOf,
  onChanged,
}: {
  orgId: string;
  role: Role;
  nameOf: (code: string) => string;
  onChanged: () => void;
}) {
  const openRole = useOpenRole(orgId);
  // ONE panel at a time. These were four independent toggles, so opening the
  // advert form and then the closing form left both on screen stacked — and
  // the one you had moved on from was still there, below the fold, still
  // holding whatever you had typed into it.
  //
  // A single value makes switching mean what it looks like: the last thing
  // you asked for is the thing you see.
  const [panel, setPanel] = useState<"candidates" | "advertise" | "close" | "revise" | null>(null);
  type Panel = Exclude<typeof panel, null>;
  const show = (which: Panel) => setPanel((current) => (current === which ? null : which));

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
        {ENGAGEMENTS.find((e) => e.value === role.engagement)?.label}
        {pay ? ` · ${pay}${role.engagement === "freelance" ? " / hour" : " a year"}` : ""}
      </p>

      {role.description ? <p className="small">{role.description}</p> : null}

      <p className="field__help">
        {describeForHirer(role.locations, nameOf)}.
        {role.min_oss_yoe != null
          ? ` At least ${role.min_oss_yoe} years contributing to open source.`
          : ""}
      </p>

      {role.close_requested_at && role.status === "open" ? (
        <Banner icon="warn">
          Somebody asked to close this on {format.date(role.close_requested_at)}. It is still open
          until an owner withdraws it.
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
        {/* Only a DRAFT opens. A closed role is final — see the warning on
            the closing form. */}
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
            <Button aria-pressed={panel === "revise"} onClick={() => show("revise")}>
              Publish a new version
            </Button>

            {/* Advertising it. Alongside the other actions rather than below
                them: a button on its own under a row of buttons reads as
                belonging to whatever comes next, and this one was being
                missed entirely. Only on an OPEN role — a draft is not a
                commitment and a closed one is a withdrawn job. */}
            <Button aria-pressed={panel === "advertise"} onClick={() => show("advertise")}>
              {role.advertised ? "Edit the public advert" : "Advertise it publicly"}
            </Button>

            <Button aria-pressed={panel === "close"} onClick={() => show("close")}>
              Close it
            </Button>
          </>
        ) : null}

        {role.status !== "draft" ? (
          <Button aria-pressed={panel === "candidates"} onClick={() => show("candidates")}>
            Who is on it
          </Button>
        ) : null}
      </div>

      {panel === "revise" && role.status === "open" ? (
        <RoleForm
          orgId={orgId}
          revising={role}
          onDone={() => {
            setPanel(null);
            onChanged();
          }}
          onCancel={() => setPanel(null)}
        />
      ) : null}

      {panel === "advertise" && role.status === "open" ? (
        <OpeningForm orgId={orgId} role={role} />
      ) : null}

      {/* Who has already been approached for this job. Read before searching
          again — and the search itself now stops offering them. */}
      {panel === "candidates" && role.status !== "draft" ? (
        <Candidates orgId={orgId} role={role} />
      ) : null}

      {panel === "close" ? (
        <CloseForm
          orgId={orgId}
          role={role}
          onDone={() => {
            setPanel(null);
            onChanged();
          }}
          onCancel={() => setPanel(null)}
        />
      ) : null}
    </Card>
  );
}

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
  const [query, setQuery] = useState("");
  const [stages, setStages] = useState<Set<Stage>>(new Set());

  const unstage = useUnstageCandidate(orgId, role.id);
  const contact = useContactCandidates(orgId, role.id);

  // The date everybody contacted is promised, asked HERE rather than when the
  // job was written: a role is immutable once open (ADR-0019 §13), and this is
  // a promise about now, not a property of the opening.
  const [resultBy, setResultBy] = useState("");
  const [acknowledged, setAcknowledged] = useState(false);

  const all = candidates.data?.candidates ?? [];
  const stageFor = (c: RoleCandidate) => stageOf(c.contact_status, c.notified_at);
  const present = new Set(all.map(stageFor));

  // Scoped to THIS ROLE, which is the whole point: a hirer asking "did I
  // already approach her for this job" is asking about one job. It searches
  // names and the round somebody is on, because those are the two things a
  // person remembers about a candidate they staged last week.
  const list = inStages(matchingCandidates(all, query), stages, stageFor);

  // Who has been told nothing yet. The only people a contact step reaches.
  const untold = all.filter((c) => !c.notified_at);

  return (
    <Card>
      <SectionTitle>Already on this role</SectionTitle>

      {candidates.loading ? <Loading what="candidates" /> : null}
      {candidates.error ? (
        <Failure message={candidates.error.message} onRetry={candidates.reload} />
      ) : null}

      {all.length > 0 ? (
        <div className="od-row">
          <StageToggles
            present={present}
            selected={stages}
            onChange={setStages}
            label={`Filter candidates on ${role.title} by stage`}
          />

          <div className="search-input od-fill">
            <Icon name="search" size="sm" />
            <input
              className="input"
              type="search"
              autoComplete="off"
              aria-label={`Search candidates on ${role.title}`}
              placeholder="Search these candidates"
              value={query}
              onChange={(event) => setQuery(event.target.value)}
            />
          </div>
        </div>
      ) : null}

      {!candidates.loading && all.length === 0 ? (
        <Empty title="Nobody yet" icon="people">
          Shortlist somebody from search into this role and they will appear here.
        </Empty>
      ) : null}

      {untold.length > 0 ? (
        <Card>
          {/* THE IRREVERSIBLE STEP, on the job the people were staged onto.
              Staging told them nothing; this is the act that does, and it
              cannot be taken back — so it asks for a date and an
              acknowledgement rather than being a single click. */}
          <SectionTitle>
            Tell {untold.length} {untold.length === 1 ? "person" : "people"}
          </SectionTitle>

          {contact.error ? <Failure message={contact.error.message} /> : null}

          <Field
            label="You expect to have an answer by"
            htmlFor={`result-by-${role.id}`}
            help="Everybody you contact is told this date."
            error={contact.error?.field === "tentative_result_date" ? contact.error.message : null}
          >
            <input
              className="input"
              id={`result-by-${role.id}`}
              type="date"
              value={resultBy}
              onChange={(event) => setResultBy(event.target.value)}
            />
          </Field>

          <Checkbox
            label="I understand this cannot be undone"
            checked={acknowledged}
            onChange={setAcknowledged}
          />

          <Button
            variant="danger"
            disabled={!acknowledged || !resultBy || contact.pending}
            onClick={() =>
              void contact.run(resultBy).then((ok) => {
                if (ok === null) return;
                setAcknowledged(false);
                candidates.reload();
              })
            }
          >
            <Icon name="mail" size="sm" />
            {contact.pending ? "Sending…" : `Tell ${untold.length}`}
          </Button>
        </Card>
      ) : null}

      {all.length > 0 && list.length === 0 ? (
        <Empty
          title={query.trim() ? `Nobody here matches “${query.trim()}”` : "Nobody at that stage"}
          icon="people"
        >
          This searches only the {all.length} {all.length === 1 ? "person" : "people"} already on
          this role.
        </Empty>
      ) : null}

      {list.map((c) => {
        const look = stageLook(stageFor(c));
        return (
          <div className="od-row" key={c.user_id} style={{ ["--od-gap" as string]: "10px" }}>
            <span className="od-field od-fill">
              {/* Through to the scorecard, as in search. */}
              <Link to={`/contributors/${c.user_id}`}>{c.display_name}</Link>
              {c.note ? <span className="field__help">{c.note}</span> : null}
            </span>
            {/* The address, where it exists at all — it is carried only for
                somebody who accepted (ADR-0019 amendment 4). Beside the pill
                that says they accepted, because reading "accepted" and then
                having to go somewhere else for the address is the gap this
                closes. */}
            {c.email ? <span className="row__meta mono">{c.email}</span> : null}
            <Pill tone={look.tone}>{look.label}</Pill>

            {/* Removable only while they have been told nothing. Afterwards
                there is no button and nothing in its place — the pill beside
                it already says they were told. */}
            {c.notified_at ? null : (
              <Button
                variant="quiet"
                size="sm"
                disabled={unstage.pending}
                onClick={() =>
                  void unstage.run(c.user_id).then((ok) => ok !== null && candidates.reload())
                }
              >
                Remove
              </Button>
            )}
          </div>
        );
      })}
    </Card>
  );
}

/* --- closing ---------------------------------------------------------------- */

const REASONS: Array<{ value: CloseReason; label: string; help: string }> = [
  {
    value: "hired_via_platform",
    label: "We hired somebody from here",
    help: "",
  },
  {
    value: "hired_elsewhere",
    label: "We filled it another way",
    help: "",
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
  const refused = (field: string) => (close.error?.field === field ? close.error.message : null);

  return (
    <Card>
      <SectionTitle>Why are you closing this?</SectionTitle>

      {/* Said BEFORE the decision, not discovered afterwards. Closing is the
          one action on this screen with no way back, and a hirer who thought
          it was reversible would close a role to tidy up and then find they
          have to write it again. */}
      <Banner icon="warn">
        <b>Closing is permanent.</b> A closed role cannot be reopened. To change what it says,
        publish a new version instead.
      </Banner>

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
      <FieldError message={refused("reason")} />

      {reason === "hired_via_platform" ? (
        <>
          <SectionTitle>Who did you hire?</SectionTitle>

          {candidates.loading ? <Loading what="candidates" /> : null}
          {candidates.error ? <Failure message={candidates.error.message} /> : null}

          {!candidates.loading && acceptable.length === 0 ? (
            <Banner icon="warn">
              Nobody has accepted a contact request for this role yet. If you found someone another
              way, close it as hired elsewhere.
            </Banner>
          ) : null}

          {acceptable.map((c) => (
            <Checkbox
              key={c.user_id}
              label={c.display_name}
              help={c.github_login ? `@${c.github_login}` : undefined}
              checked={hired.includes(c.user_id)}
              onChange={(on) =>
                setHired(on ? [...hired, c.user_id] : hired.filter((id) => id !== c.user_id))
              }
            />
          ))}
          <FieldError message={refused("hired")} />
        </>
      ) : null}

      {reason === "other" ? (
        <Field label="What happened?" htmlFor={`note-${role.id}`} error={refused("note")}>
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

  // Creating and opening in one go is offered only to a seat the organisation
  // lets open roles; the server still decides.
  const openRole = useOpenRole(orgId);
  const settings = useOrgSettings(orgId);
  const seat = useSeatRole(orgId);
  const canOpen = !revising && mayOpen(settings.data?.role_create_authority, seat);
  // The draft a "Create and open" made before opening it was refused, so a
  // retry opens that one rather than writing a second.
  const [created, setCreated] = useState<string | null>(null);

  // The onsite picker, the organisation's kind and any office request, from
  // one read (ADR-0024 Implementation).
  const addresses = useAddresses(orgId);
  const nameOf = useCountryNames();

  const [form, setForm] = useState<RoleDraft>(revising ? draftOf(revising) : BLANK);
  const [choice, setChoice] = useState<LocationChoice>(
    revising ? choiceOf(revising.locations) : DEFAULT_CHOICE,
  );

  // The locations of the request last sent. A refusal's position is read
  // against these, never against whatever the form holds now.
  const [sent, setSent] = useState<LocationRequest[]>([]);

  const set = <K extends keyof RoleDraft>(key: K, value: RoleDraft[K]) =>
    setForm({ ...form, [key]: value });

  const freelance = form.engagement === "freelance";

  // Shown once they have tried to save, and cleared by the next edit that
  // fixes it — not while they are still filling the form in.
  const [tried, setTried] = useState(false);
  const draft: RoleDraft = { ...form, locations: locationsOf(choice) };
  const problem =
    (tried ? draftProblem(form.title, choice) : null) ?? problemFrom(action.error, sent);
  const refused = (field: string) => messageFor(problem, field);

  const submit = () => {
    if (created) return onDone();
    setTried(true);
    if (draftProblem(form.title, choice)) return;
    setSent(draft.locations);
    void action.run(draft).then((ok) => ok && onDone());
  };

  const open = (id: string) => void openRole.run(id).then((ok) => ok && onDone());

  const submitAndOpen = () => {
    if (created) return open(created);
    setTried(true);
    if (draftProblem(form.title, choice)) return;
    setSent(draft.locations);
    void create.run(draft).then((role) => {
      if (!role) return;
      setCreated(role.id);
      open(role.id);
    });
  };

  // The live offices, and any this role already names that have since been
  // removed — shown so they can be unticked rather than sent unseen.
  const live = addresses.data?.addresses ?? [];
  const carried = (revising?.locations ?? [])
    .map((entry) => entry.address)
    .filter((a): a is OrgAddress => a != null && !live.some((l) => l.id === a.id));

  return (
    <Card>
      <SectionTitle note={revising ? "Publishes a new role and closes this one." : undefined}>
        {revising ? "Publish a new version" : "Write a role"}
      </SectionTitle>

      <Field label="Title" htmlFor="title" error={refused("title")}>
        <input
          className="input"
          id="title"
          value={form.title}
          onChange={(event) => set("title", event.target.value)}
        />
      </Field>

      <Field label="Description" htmlFor="description" error={refused("description")}>
        <textarea
          className="input"
          id="description"
          rows={3}
          value={form.description}
          onChange={(event) => set("description", event.target.value)}
        />
      </Field>

      <Field label="Kind of work" htmlFor="engagement" error={refused("engagement")}>
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

      <CurrencyPicker
        id="currency"
        label="Currency"
        value={form.currency}
        onChange={(code) => set("currency", code)}
        error={refused("currency")}
      />

      {freelance ? (
        <>
          <Field label="Hourly rate" htmlFor="hourly" error={refused("hourly_rate")}>
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
          <Field label="Expected hours a week" htmlFor="hours" error={refused("expected_hours")}>
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
          <Field
            label="Total package a year"
            htmlFor="ctc"
            help="Everything included."
            error={refused("yearly_ctc")}
          >
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
            help="What arrives monthly."
            error={refused("yearly_base")}
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

      <LocationFields
        formId={revising?.id ?? "new"}
        choice={choice}
        onChange={setChoice}
        offices={[...live, ...carried]}
        loading={addresses.loading}
        loadError={addresses.error?.message}
        fullyRemote={addresses.data?.workplace === "fully_remote"}
        officeRequest={addresses.data?.office_request ?? null}
        problem={problem}
        nameOf={nameOf}
      />

      <Field
        label="Least years in a job"
        htmlFor="office_yoe"
        help="Blank for no minimum."
        error={refused("min_office_yoe")}
      >
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
        help="Dated from their first pull request."
        error={refused("min_oss_yoe")}
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

      <SectionTitle>Your process</SectionTitle>

      <Checkbox
        label="There is an online test"
        checked={form.requires_online_test === true}
        onChange={(on) => set("requires_online_test", on)}
      />

      <Field label="Most interview rounds" htmlFor="rounds" error={refused("max_interview_rounds")}>
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
        help="Blank rather than invented — you will be held to it."
        error={refused("avg_days_to_offer")}
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

      {created && openRole.error ? (
        <Failure message={`Saved as a draft, not opened. ${openRole.error.message}`} />
      ) : problem ? (
        <Failure message={problem.message} />
      ) : action.error ? (
        <Failure message={action.error.message} />
      ) : null}

      <div className="od-row">
        {canOpen ? (
          <Button
            variant="primary"
            disabled={create.pending || openRole.pending || !form.title.trim()}
            onClick={submitAndOpen}
          >
            {create.pending || openRole.pending ? "Opening…" : "Create and open"}
          </Button>
        ) : null}
        <Button
          variant={canOpen ? undefined : "primary"}
          disabled={action.pending || openRole.pending || !form.title.trim()}
          onClick={submit}
        >
          {action.pending
            ? "Saving…"
            : revising
              ? "Publish it"
              : created
                ? "Done"
                : "Save as a draft"}
        </Button>
        <Button onClick={onCancel}>Cancel</Button>
      </div>
    </Card>
  );
}

/**
 * Where the work happens: Remote and Onsite, each a switch with its own list.
 *
 * Presentational — the offices and the organisation's kind arrive as props.
 * Remote is anywhere OR a set of countries, never both (ADR-0024 decision 3).
 * Onsite is off for a fully remote organisation, except to untick it on a role
 * written before the organisation went fully remote.
 */
function LocationFields({
  formId,
  choice,
  onChange,
  offices,
  loading,
  loadError,
  fullyRemote,
  officeRequest,
  problem,
  nameOf,
}: {
  formId: string;
  choice: LocationChoice;
  onChange: (choice: LocationChoice) => void;
  offices: OrgAddress[];
  loading: boolean;
  loadError: string | undefined;
  fullyRemote: boolean;
  officeRequest: AddressesResponse["office_request"];
  problem: RoleProblem | null;
  nameOf: (code: string) => string;
}) {
  const at = (slot: RoleProblem["slot"]) =>
    problem && problem.slot === slot && !problem.addressId ? problem.message : null;
  const atOffice = (id: string) =>
    problem && problem.slot === "onsite" && problem.addressId === id ? problem.message : null;

  const remoteNote = fullyRemote
    ? officeRequest?.status === "pending"
      ? "Your organisation is fully remote. An office request is waiting for review."
      : officeRequest?.status === "rejected"
        ? `Your organisation is fully remote. The office request was not approved: ${officeRequest.reason ?? ""}`
        : "Your organisation is fully remote."
    : undefined;

  return (
    <>
      <SectionTitle>Where the work happens</SectionTitle>
      <FieldError message={at("where")} />

      <Checkbox
        label="Remote"
        checked={choice.remote}
        onChange={(on) => onChange({ ...choice, remote: on })}
      />
      {choice.remote ? (
        <>
          <Radio
            name={`remote-scope-${formId}`}
            label="From anywhere"
            checked={choice.anywhere}
            onChange={() => onChange({ ...choice, anywhere: true })}
          />
          <Radio
            name={`remote-scope-${formId}`}
            label="Only in certain countries"
            checked={!choice.anywhere}
            onChange={() => onChange({ ...choice, anywhere: false })}
          />
          {!choice.anywhere ? (
            <CountryList
              id={`remote-countries-${formId}`}
              label="Countries"
              values={choice.countries}
              onChange={(countries) => onChange({ ...choice, countries })}
            />
          ) : null}
        </>
      ) : null}
      <FieldError message={at("remote")} />

      <Checkbox
        label="Onsite"
        help={remoteNote}
        checked={choice.onsite}
        disabled={fullyRemote && !choice.onsite}
        onChange={(on) => onChange({ ...choice, onsite: on })}
      />
      {choice.onsite ? (
        <>
          {loading ? <Loading what="your offices" /> : null}
          {loadError ? <Failure message={loadError} /> : null}
          {!loading && offices.length === 0 ? (
            <p className="field__help">
              No offices yet. <Link to="/offices">Add one</Link>.
            </p>
          ) : null}
          {offices.map((office) => (
            <div key={office.id}>
              <Checkbox
                label={`${office.city}, ${nameOf(office.country)}`}
                help={[
                  addressLine(office),
                  office.is_main_office ? "main office" : null,
                  office.removed ? "removed" : null,
                ]
                  .filter(Boolean)
                  .join(" · ")}
                checked={choice.addressIds.includes(office.id)}
                onChange={(on) =>
                  onChange({
                    ...choice,
                    addressIds: on
                      ? [...choice.addressIds, office.id]
                      : choice.addressIds.filter((id) => id !== office.id),
                  })
                }
              />
              <FieldError message={atOffice(office.id)} />
            </div>
          ))}
        </>
      ) : null}
      <FieldError message={at("onsite")} />
    </>
  );
}
