// The contributor's control over being found.
//
// Three separate things, deliberately not merged into one "privacy" screen:
// availability decides whether a hirer sees you at all, a contact request is a
// decision only you can make, and a share link is the one public surface on the
// platform (ADR-0002).

import { useState } from "react";
import {
  useMintShareLink,
  useMyContactRequests,
  useRespondToContact,
  useRevokeShareLink,
  useSetAvailability,
} from "../hooks/contributor";
import { useWhoAmI } from "../hooks/session";
import * as format from "../logic/format";
import {
  Banner,
  Button,
  Card,
  Empty,
  Failure,
  Icon,
  Loading,
  PageHead,
  Pill,
} from "../ui/primitives";

const CHOICES = [
  ["looking_for_job", "Looking for a job"],
  ["looking_for_freelance", "Looking for freelance work"],
  ["open_to_freelance", "Open to freelance work"],
  ["not_looking", "Not looking"],
] as const;

export function DiscoverabilityPage() {
  const me = useWhoAmI();
  const set = useSetAvailability();
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
        lede="Whether a hirer can see you, who has asked, and the one link you can publish."
      />

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
          Saying nothing keeps you out of search entirely — a new contributor is invisible until
          they choose to be visible. Any answer lasts fifteen days and then lapses, which hides you
          by default without unranking you.
        </p>

        <Banner icon="warn">
          <b>Not looking</b> is different: it is an opt-out. It removes you from search and from the
          ranked population, and no hirer toggle reveals you. Your scores are untouched.
        </Banner>

        {set.error ? <Failure message={set.error.message} /> : null}

        <div className="od-cluster">
          {CHOICES.map(([value, label]) => (
            <Button
              key={value}
              variant={current === value ? "primary" : "ghost"}
              size="sm"
              disabled={set.pending}
              onClick={() => {
                setChosen(value);
                void set.run(value).then(() => me.reload());
              }}
            >
              {label}
            </Button>
          ))}
        </div>
      </Card>

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
        {requests.error ? <Failure message={requests.error.message} onRetry={requests.reload} /> : null}
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
                    <span className="row__name">{r.organization.name}</span>
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
