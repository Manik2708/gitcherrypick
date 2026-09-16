// Claiming a seat someone at your company reserved for you.
//
// Two steps, because a roster is an ALLOWLIST and not a credential: being
// listed is a guessable fact, so the first step proves you hold the address and
// the second creates the account (ADR-0016 §3).
//
// The screen deliberately never tells you whether an address is on a roster.
// The server answers the same way either way, and so does this: "if that
// address is on their list, a link is on its way." Saying more would turn the
// form into a way of discovering who a company is hiring.

import { useMemo, useState } from "react";
import { Link, useNavigate, useSearchParams } from "react-router-dom";
import { endpoints } from "../api/endpoints";
import { principalOf, tokensOf } from "../api/session";
import type { OrganizationsResponse, SessionResponse } from "../contract";
import { useSession } from "../hooks/session";
import { useAction, useAsync } from "../hooks/useAsync";
import { Banner, Button, Card, Failure, Field, Loading, PageHead } from "../ui/primitives";

export function RedeemPage() {
  const [params] = useSearchParams();
  const token = params.get("token") ?? "";

  // The link in the mail carries the token, so arriving with one means step
  // two. Arriving without one means step one, whether that is a first visit or
  // a return to ask for another link.
  return token ? <CompleteRedemption token={token} /> : <StartRedemption />;
}

/* --- step one: prove the address ----------------------------------------- */

function StartRedemption() {
  const { client } = useSession();

  const [slug, setSlug] = useState("");
  const [email, setEmail] = useState("");

  const organizations = useAsync<OrganizationsResponse>(
    (signal) => client.get<OrganizationsResponse>(endpoints.public.organizations(), signal),
    [client],
  );

  const start = useAction(() =>
    client.post<void>(endpoints.auth.redeemStart(), { org_slug: slug, email }),
  );

  const resend = useAction(() => client.post<void>(endpoints.auth.resendVerification(), { email }));

  const orgs = useMemo(() => organizations.data?.organizations ?? [], [organizations.data]);
  const sent = start.data !== undefined || resend.data !== undefined;

  return (
    <div className="page page--narrow">
      <PageHead
        eyebrow="Hiring account"
        title="Claim your seat"
        lede="Someone at your company reserved a seat against your work address. Prove you hold that address and it becomes an account."
      />

      {start.error ? <Failure message={start.error.message} /> : null}
      {resend.error ? <Failure message={resend.error.message} /> : null}

      {sent ? (
        <>
          <Banner icon="check">
            If <b>{email}</b> is on that organisation&rsquo;s list, a link is on its way. It works
            once and expires in 24 hours.
          </Banner>
          <Banner>
            We do not say whether the address was listed. Answering that question would let anyone
            with this form discover who a company is hiring.
          </Banner>
          <p className="field__help">
            Nothing arrived?{" "}
            <button type="button" className="linklike" onClick={() => void resend.run()}>
              Send it again
            </button>
            . The previous link stops working when a new one is issued.
          </p>
        </>
      ) : (
        <Card>
          {organizations.loading ? (
            <Loading what="organisations" />
          ) : (
            <Field
              label="Your company"
              htmlFor="org"
              help="Only verified organisations can seat anybody, so only those are listed."
            >
              <select
                className="input"
                id="org"
                value={slug}
                onChange={(event) => setSlug(event.target.value)}
              >
                <option value="">Choose…</option>
                {orgs.map((o) => (
                  <option key={o.slug} value={o.slug}>
                    {o.name}
                  </option>
                ))}
              </select>
            </Field>
          )}

          <Field
            label="Your work email"
            htmlFor="email"
            help="The address your company listed. Not a username — you do not choose that."
          >
            <input
              className="input"
              id="email"
              type="email"
              autoComplete="email"
              value={email}
              onChange={(event) => setEmail(event.target.value)}
            />
          </Field>

          <Button
            variant="primary"
            disabled={!slug || !email.trim() || start.pending}
            onClick={() => void start.run()}
          >
            {start.pending ? "Sending…" : "Send me a link"}
          </Button>

          <p className="field__help">
            Already have an account? <Link to="/signin">Sign in</Link>.
          </p>
        </Card>
      )}
    </div>
  );
}

/* --- step two: set a password -------------------------------------------- */

function CompleteRedemption({ token }: { token: string }) {
  const { client, adopt } = useSession();
  const navigate = useNavigate();

  const [displayName, setDisplayName] = useState("");
  const [password, setPassword] = useState("");

  const complete = useAction(async () => {
    const session = await client.post<SessionResponse>(endpoints.auth.redeemComplete(), {
      token,
      display_name: displayName,
      password,
    });
    adopt(principalOf(session), tokensOf(session));
    navigate("/search", { replace: true });
    return session;
  });

  // 410 means the link WAS valid and no longer is. The two reasons need
  // different things from the reader — sign in, or ask for another — so they
  // are separated rather than collapsed into one refusal.
  const gone = complete.error?.status === 410;
  const spent = complete.error?.code === "verification_already_used";

  return (
    <div className="page page--narrow">
      <PageHead
        eyebrow="Hiring account"
        title="Set your password"
        lede="Your address is proven. This is the last step."
      />

      {complete.error ? <Failure message={complete.error.message} /> : null}

      {gone ? (
        <Card>
          <p>
            {spent ? (
              <>
                That link has already been used, which means your seat exists.{" "}
                <Link to="/signin">Sign in</Link> with the username your organisation gave you.
              </>
            ) : (
              <>
                That link has expired. <Link to="/redeem">Ask for another</Link> — links last 24
                hours so that a forwarded or archived message stops working the next day.
              </>
            )}
          </p>
        </Card>
      ) : (
        <Card>
          <Banner>
            You do not choose a username here. Your organisation pinned it when they added you, so
            nobody can take a colleague&rsquo;s name or grant themselves an owner seat.
          </Banner>

          <Field label="Your name" htmlFor="name" help="How colleagues will see you.">
            <input
              className="input"
              id="name"
              value={displayName}
              onChange={(event) => setDisplayName(event.target.value)}
            />
          </Field>
          <Field label="Password" htmlFor="password" help="At least 8 characters.">
            <input
              className="input"
              id="password"
              type="password"
              autoComplete="new-password"
              value={password}
              onChange={(event) => setPassword(event.target.value)}
            />
          </Field>

          <Button
            variant="primary"
            disabled={!displayName.trim() || password.length < 8 || complete.pending}
            onClick={() => void complete.run()}
          >
            {complete.pending ? "Creating your seat…" : "Create my seat"}
          </Button>
        </Card>
      )}
    </div>
  );
}
