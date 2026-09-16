// Who may hire here, and who does.
//
// Two lists that look similar and mean different things. The ROSTER is intent:
// addresses an owner has said may hold a seat, most of which have not been
// claimed yet. The SEATS are accounts that exist. An entry becomes a seat only
// when the person proves they hold the address (ADR-0016 §3).
//
// Removing an entry revokes access; it never deletes the person. Their rounds,
// their saved searches and the record of who was told all stay, and the seat
// stays listed here marked inactive — which is what keeps a name on a round
// from two years ago (ADR-0016 §5, §5a).

import { useState } from "react";
import { endpoints } from "../api/endpoints";
import type {
  OrgRole,
  RosterEntry,
  RosterResponse,
  Seat,
  SeatsResponse,
  VerificationStatus,
} from "../contract";
import { useSession } from "../hooks/session";
import { useAction, useAsync } from "../hooks/useAsync";
import {
  Banner,
  Button,
  By,
  Card,
  Empty,
  Failure,
  Field,
  Loading,
  PageHead,
  Pill,
  SectionTitle,
} from "../ui/primitives";

export function TeamPage() {
  const { client, principal } = useSession();

  // The id arrives with the SIGN-IN response. `/me` does not carry one — that
  // shape is pinned by the approved fixtures — so a session rehydrated from
  // storage written by an older build has a name and no id. /me/verification
  // does carry it, so the fallback is one request rather than a sign-out.
  const stored = principal?.organization?.id;
  const recovered = useAsync<VerificationStatus>(
    (signal) => client.get<VerificationStatus>(endpoints.me.verification(), signal),
    [client],
    !stored,
  );
  const orgId = stored ?? recovered.data?.organization?.id;

  const roster = useAsync<RosterResponse>(
    (signal) => client.get<RosterResponse>(endpoints.orgs.roster(orgId ?? ""), signal),
    [client, orgId],
    Boolean(orgId),
  );
  const seats = useAsync<SeatsResponse>(
    (signal) => client.get<SeatsResponse>(endpoints.orgs.seats(orgId ?? ""), signal),
    [client, orgId],
    Boolean(orgId),
  );

  if (!orgId) {
    return (
      <div className="page">
        <PageHead eyebrow="Organisation" title="Your team" />
        {recovered.loading ? (
          <Loading what="your organisation" />
        ) : (
          <Banner icon="warn">
            This session does not name an organisation, so there is no roster to show. Sign out and
            in again.
          </Banner>
        )}
      </div>
    );
  }

  return (
    <div className="page">
      <PageHead
        eyebrow="Organisation"
        title="Your team"
        lede="Reserve a seat for a colleague, and see who holds one."
      />

      <RosterSection
        orgId={orgId}
        entries={roster.data?.entries}
        loading={roster.loading}
        error={roster.error?.message}
        onChange={() => {
          roster.reload();
          seats.reload();
        }}
      />

      <SeatsSection
        seats={seats.data?.seats}
        loading={seats.loading}
        error={seats.error?.message}
      />
    </div>
  );
}

/* --- the roster ----------------------------------------------------------- */

function RosterSection({
  orgId,
  entries,
  loading,
  error,
  onChange,
}: {
  orgId: string;
  entries: RosterEntry[] | undefined;
  loading: boolean;
  error: string | undefined;
  onChange: () => void;
}) {
  const { client } = useSession();

  const [email, setEmail] = useState("");
  const [username, setUsername] = useState("");
  const [role, setRole] = useState<OrgRole>("member");

  const add = useAction(async () => {
    const created = await client.post<RosterEntry>(endpoints.orgs.roster(orgId), {
      email,
      username,
      role,
    });
    setEmail("");
    setUsername("");
    setRole("member");
    onChange();
    return created;
  });

  const remove = useAction(async (entryId: string) => {
    await client.delete<void>(endpoints.orgs.removeRosterEntry(orgId, entryId));
    onChange();
  });

  return (
    <>
      <SectionTitle note="Owners only. An owner entry can add further people, so it is not a choice a member gets to make.">
        Reserve a seat
      </SectionTitle>

      {add.error ? <Failure message={add.error.message} /> : null}
      {remove.error ? <Failure message={remove.error.message} /> : null}

      <Card>
        <Banner>
          Listing an address does not create an account and grants nothing on its own. Your
          colleague proves they hold the address, and the seat exists from that moment.
        </Banner>

        <Field
          label="Work email"
          htmlFor="roster-email"
          help="Where the link goes. Two colleagues may share one — it is a contact address, not an identity."
        >
          <input
            className="input"
            id="roster-email"
            type="email"
            value={email}
            onChange={(event) => setEmail(event.target.value)}
          />
        </Field>

        <Field
          label="Username"
          htmlFor="roster-username"
          help="You choose it, not them. It is unique across the platform and is never reused, so the name on an old round always means one person."
        >
          <input
            className="input"
            id="roster-username"
            value={username}
            onChange={(event) => setUsername(event.target.value)}
          />
        </Field>

        <Field label="Role" htmlFor="roster-role">
          <select
            className="input"
            id="roster-role"
            value={role}
            onChange={(event) => setRole(event.target.value as OrgRole)}
          >
            <option value="member">Member — searches and shortlists</option>
            <option value="owner">Owner — can also add and remove people</option>
          </select>
        </Field>

        <Button
          variant="primary"
          disabled={!email.trim() || !username.trim() || add.pending}
          onClick={() => void add.run()}
        >
          {add.pending ? "Adding…" : "Add to the roster"}
        </Button>
      </Card>

      <SectionTitle note="Redeemed entries stay listed: they are the record of why a seat exists.">
        Roster
      </SectionTitle>

      {error ? <Failure message={error} /> : null}
      {loading ? <Loading what="the roster" /> : null}

      {!loading && entries?.length === 0 ? (
        <Empty title="Nobody is on the roster yet">
          Add a colleague&rsquo;s work address above and they can claim a seat.
        </Empty>
      ) : null}

      <div className="rows">
        {entries?.map((entry) => (
          <div className="row" key={entry.id}>
            <span className="row__ident">
              <span className="row__name">{entry.username}</span>
              <span className="row__login">{entry.email}</span>
              <span className="row__meta">
                <Pill tone={entry.role === "owner" ? "violet" : undefined}>{entry.role}</Pill>
                {entry.redeemed_at ? <Pill tone="primary">Claimed</Pill> : <Pill>Not claimed</Pill>}
                <By who={entry.added_by} verb="Added by" />
              </span>
            </span>
            <span className="row__right">
              <span className="row__actions">
                <Button
                  variant="danger"
                  size="sm"
                  disabled={remove.pending}
                  title={
                    entry.redeemed_at
                      ? "Revokes their access. Their rounds and the record of who was told all stay."
                      : "Withdraws the offer before anyone claims it."
                  }
                  onClick={() => void remove.run(entry.id)}
                >
                  {entry.redeemed_at ? "Revoke access" : "Withdraw"}
                </Button>
              </span>
            </span>
          </div>
        ))}
      </div>
    </>
  );
}

/* --- the seats ------------------------------------------------------------ */

function SeatsSection({
  seats,
  loading,
  error,
}: {
  seats: Seat[] | undefined;
  loading: boolean;
  error: string | undefined;
}) {
  const departed = seats?.filter((s) => !s.active).length ?? 0;

  return (
    <>
      <SectionTitle note="Everyone in the organisation can read this — every seat already sees every round.">
        Seats
      </SectionTitle>

      {error ? <Failure message={error} /> : null}
      {loading ? <Loading what="seats" /> : null}

      {departed > 0 ? (
        <Banner>
          {departed === 1 ? "One seat has" : `${departed} seats have`} been revoked. They stay
          listed on purpose: a round from two years ago still carries their name, and a list that
          quietly dropped them would leave that name unresolvable.
        </Banner>
      ) : null}

      <div className="rows">
        {seats?.map((seat) => (
          <div className={seat.active ? "row" : "row row--inactive"} key={seat.id}>
            <span className="row__ident">
              <span className="row__name">{seat.display_name}</span>
              <span className="row__login">{seat.username}</span>
              <span className="row__meta">
                <Pill tone={seat.role === "owner" ? "violet" : undefined}>{seat.role}</Pill>
                {seat.active ? null : <Pill tone="stale">Access revoked</Pill>}
                <span>{seat.email}</span>
              </span>
            </span>
          </div>
        ))}
      </div>
    </>
  );
}
