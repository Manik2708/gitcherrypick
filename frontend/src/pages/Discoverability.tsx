// The contributor's control over being found.
//
// Three separate things, deliberately not merged into one "privacy" screen:
// availability decides whether a hirer sees you at all, a contact request is a
// decision only you can make, and a share link is the one public surface on the
// platform (ADR-0002).

import { useState } from "react";
import type { ContactRole, Engagement } from "../contract";

/** Words, not enum values. "full_time" on a screen is the database leaking. */
const ENGAGEMENT_LABELS: Record<Engagement, string> = {
  full_time: "full time",
  contract: "contract",
  internship: "internship",
  freelance: "freelance",
};
import {
  useMintShareLink,
  useMyContactRequests,
  useMyProfile,
  useRespondToContact,
  useRevokeShareLink,
  useSetAvailability,
} from "../hooks/contributor";
import { useWhoAmI } from "../hooks/session";
import { ProfileForm } from "../ui/ProfileForm";
import { ReadinessPanel } from "../ui/Readiness";
import * as format from "../logic/format";
import {
  Banner,
  Button,
  Card,
  Checkbox,
  Empty,
  Failure,
  Icon,
  Loading,
  PageHead,
  Pill,
} from "../ui/primitives";

// One switch (ADR-0021). There were four buttons, three of which said "yes"
// and differed only in what KIND of work somebody wanted — which is the
// question the preferences below already ask, and ask better.
//
// LOOKING MEANS OPEN TO ANYTHING. Turning it on is the whole of making
// yourself reachable; nothing further has to be ticked, which is the trap the
// old design had.

/** The job, as the person being approached sees it. */
function RoleSummary({ role }: { role: ContactRole }) {
  const pay =
    role.engagement === "freelance"
      ? role.hourly_rate != null
        ? `${role.currency} ${(role.hourly_rate / 100).toLocaleString()} an hour`
        : null
      : role.yearly_ctc != null
        ? `${role.currency} ${(role.yearly_ctc / 100).toLocaleString()} a year`
        : null;

  const process: string[] = [];
  if (role.max_interview_rounds != null) {
    process.push(`at most ${role.max_interview_rounds} interview rounds`);
  }
  if (role.avg_days_to_offer != null) {
    process.push(`usually ${role.avg_days_to_offer} days to an offer`);
  }
  if (role.requires_online_test) process.push("an online test");

  return (
    <div className="ev__body">
      <p className="small">
        <strong>{role.title}</strong> · {ENGAGEMENT_LABELS[role.engagement]} ·{" "}
        {role.location === "remote" ? "remote" : "at their office"}
        {pay ? ` · ${pay}` : ""}
      </p>
      {role.description ? <p className="small muted">{role.description}</p> : null}
      {role.eligible_countries.length > 0 ? (
        <p className="field__help">Can hire in {role.eligible_countries.join(", ")}.</p>
      ) : null}
      {process.length > 0 ? (
        <p className="field__help">Their process: {process.join(", ")}.</p>
      ) : null}
    </div>
  );
}

export function DiscoverabilityPage() {
  const me = useWhoAmI();
  const set = useSetAvailability();

  // The page owns a copy of the profile for the readiness panel, and hands
  // the form a way to say it changed. Without that the panel goes stale the
  // moment somebody fixes a field — still telling them to state a country
  // they just stated, which is worse than not showing it at all.
  const profile = useMyProfile();
  const requests = useMyContactRequests();
  const respond = useRespondToContact();
  const mint = useMintShareLink();
  const revoke = useRevokeShareLink();

  const [chosen, setChosen] = useState("");
  const [revoked, setRevoked] = useState(false);

  const current = chosen || me.data?.availability?.status || "";
  const expires = me.data?.availability?.expires_at;
  const open = requests.data?.requests ?? [];

  return (
    <div className="page">
      <PageHead
        eyebrow="Visibility"
        title="Being found"
        lede="Whether a hirer can see you, what you are looking for, who has asked, and the one link you can publish."
      />

      {/* FIRST, because it is the question the page is about. Everything
          below is a field; this is the answer they came for. */}
      <ReadinessPanel readiness={profile.data?.readiness} />

      <Card>
        <div className="section__head">
          <h2 className="section__title">Availability</h2>
        </div>

        {me.loading ? <Loading what="your current setting" /> : null}

        {current ? (
          <Banner>
            You are currently <b>{format.availability(current)}</b>
            {expires ? (
              <>
                , which lapses {format.relativeDays(expires, new Date())} unless you confirm it
                again
              </>
            ) : null}
            .
          </Banner>
        ) : (
          <Banner>You have not said anything yet, so no hirer can see you.</Banner>
        )}

        <p className="field__help">
          Turning this on opens you to everything — remote, contract, freelance, an internship. What
          you would prefer is below, and it only shapes what you are shown; it never stops a company
          reaching you. Saying nothing keeps you out of search entirely, and any answer lapses after
          fifteen days, which hides you by default without touching your scores.
        </p>

        {set.error ? <Failure message={set.error.message} /> : null}

        <Checkbox
          label="Looking for opportunities"
          help="Hirers can find you, for any kind of role."
          checked={current === "looking"}
          disabled={set.pending}
          onChange={(on) => {
            const next = on ? "looking" : "not_looking";
            setChosen(next);
            void set.run(next).then(() => {
              me.reload();
              profile.reload();
            });
          }}
        />

        <Banner icon="warn">
          Turning it off is an <b>opt-out</b>, not just silence. It removes you from search and from
          the ranked population, and no hirer toggle reveals you. Your scores are untouched, and
          turning it back on restores everything.
        </Banner>
      </Card>

      <ProfileForm onSaved={() => profile.reload()} />

      <Card>
        <div className="section__head">
          <h2 className="section__title">Organisations interested</h2>
          <span className="section__note mono">{open.length}</span>
        </div>

        <p className="field__help">
          Accepting releases your email address to this organisation, and only this one. Declining
          tells them nothing beyond that you declined. Ignoring it lets it expire.
        </p>

        {requests.loading ? <Loading what="contact requests" /> : null}
        {requests.error ? (
          <Failure message={requests.error.message} onRetry={requests.reload} />
        ) : null}
        {respond.error ? <Failure message={respond.error.message} /> : null}

        {!requests.loading && open.length === 0 ? (
          <Empty title="Nobody has asked" icon="mail" />
        ) : null}

        {open.length > 0 ? (
          <ul className="rows">
            {open.map((r) => (
              <li className="row" key={r.id}>
                <div className="row__mid">
                  <div className="row__ident">
                    <span className="row__name">
                      {r.organization.name}
                      {r.role ? ` — ${r.role.title}` : ""}
                    </span>
                    {r.organization.verified ? <Pill tone="primary">Verified</Pill> : null}
                    <Pill tone={r.organization.payment_verified ? "primary" : "cherry"}>
                      {r.organization.payment_verified ? "Payment verified" : "Payment unverified"}
                    </Pill>
                    <Pill tone="secondary">{r.status}</Pill>
                  </div>
                  <div className="row__meta">
                    <span>Result expected by {format.date(r.tentative_result_date)}</span>
                    <span>Expires {format.relativeDays(r.expires_at, new Date())}</span>
                  </div>
                  {r.disclosure ? <p className="ev__body">{r.disclosure}</p> : null}

                  {/* What they are approaching you FOR (ADR-0019 §2). Your
                      address is still released only if you accept — this is
                      here so a decline can be informed rather than made on
                      suspicion. The process figures are shown to you and to the
                      organisation, and to nobody else. */}
                  {r.role ? <RoleSummary role={r.role} /> : null}
                </div>
                <div className="row__right">
                  {r.status === "pending" ? (
                    <div className="row__actions">
                      <Button
                        variant="primary"
                        size="sm"
                        disabled={respond.pending}
                        onClick={() => void respond.run(r.id, true).then(() => requests.reload())}
                      >
                        <Icon name="mail" size="sm" />
                        Share my email
                      </Button>
                      <Button
                        variant="quiet"
                        size="sm"
                        disabled={respond.pending}
                        onClick={() => void respond.run(r.id, false).then(() => requests.reload())}
                      >
                        Decline
                      </Button>
                    </div>
                  ) : null}
                </div>
              </li>
            ))}
          </ul>
        ) : null}
      </Card>

      <Card>
        <div className="section__head">
          <h2 className="section__title">A link you can publish</h2>
        </div>

        <p className="field__help">
          The only thing on this platform an anonymous visitor can see. It carries your scores and
          your evidence — never your rank, which is a fact about a pool the reader cannot see, and
          never your email.
        </p>

        {mint.error ? <Failure message={mint.error.message} /> : null}
        {revoke.error ? <Failure message={revoke.error.message} /> : null}

        {mint.data && !revoked ? (
          <>
            <Banner icon="warn">
              <b>Copy this now.</b> Only a hash is stored, so it cannot be shown again.
            </Banner>
            <pre className="input mono">{mint.data.url}</pre>
            <Button
              variant="quiet"
              size="sm"
              disabled={revoke.pending}
              onClick={() => void revoke.run(mint.data!.id).then(() => setRevoked(true))}
            >
              Revoke this link
            </Button>
          </>
        ) : null}

        {revoked ? (
          <Banner>
            Revoked. Anyone holding it now gets the same answer as a link that never existed — the
            two are deliberately indistinguishable.
          </Banner>
        ) : null}

        <Button
          variant="ghost"
          disabled={mint.pending}
          onClick={() => {
            setRevoked(false);
            void mint.run();
          }}
        >
          {mint.data && !revoked ? "Create another" : "Create a share link"}
        </Button>
      </Card>
    </div>
  );
}
