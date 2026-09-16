// Signing up an INDEPENDENT hirer — someone hiring on their own account rather
// than a company's (ADR-0017 §1).
//
// No organisation here. Listing a company is /organisation, which creates the
// owner when an administrator approves it; joining a company that is already
// here is /redeem, where the owner rostered you.
//
// The proofs are the substance of this form, not an afterthought. Two of the
// five kinds — `alternative` and `payment_capability` — exist precisely so a
// three-person studio with no company domain and no LinkedIn page can still be
// verified. Without them verification would be satisfiable only by large
// companies (ADR-0002), so the form leads with them.
//
// Registration does NOT sign you in. It raises a verification request, and an
// administrator decides.

import { useState } from "react";
import { Link } from "react-router-dom";
import { endpoints } from "../api/endpoints";
import { useSession } from "../hooks/session";
import { useAction } from "../hooks/useAsync";
import { Banner, Button, Card, Failure, Field, PageHead } from "../ui/primitives";

const PROOF_KINDS = [
  {
    kind: "alternative",
    label: "Something else that shows you are real",
    help: "A business registration, a client reference, an invoice. Free text.",
    free: true,
  },
  {
    kind: "payment_capability",
    label: "Evidence you can pay",
    help: "Prior engagements, a payment processor, references. Free text.",
    free: true,
  },
  {
    kind: "organization_linkedin",
    label: "Company LinkedIn page",
    help: "A URL.",
    free: false,
  },
  {
    kind: "work_email_domain",
    label: "Work email domain",
    help: "The domain your address is on.",
    free: false,
  },
  { kind: "freelancer_profile", label: "Freelancer profile", help: "A URL.", free: false },
] as const;

export function RegisterPage() {
  const { client } = useSession();

  const [email, setEmail] = useState("");
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [displayName, setDisplayName] = useState("");
  const [proofs, setProofs] = useState<Record<string, string>>({});

  const register = useAction((body: unknown) =>
    client.post<unknown>(endpoints.auth.hirerRegister(), body),
  );

  const ready = email.trim() && username.trim() && password.length >= 8 && displayName.trim();

  if (register.data) {
    return (
      <div className="page page--narrow">
        <PageHead eyebrow="Hiring account" title="Submitted" />
        <Banner>
          Your account exists and is <b>awaiting review</b>. An administrator checks who you are
          before you can search the pool or shortlist anybody — a scorecard names a person and their
          work, so the account asking for one is checked first.
        </Banner>
        <Banner>
          You can sign in now and watch the status. Searching stays closed until it is approved.
        </Banner>
        <p>
          <Link to="/signin">Sign in</Link>
        </p>
      </div>
    );
  }

  return (
    <div className="page page--narrow">
      <PageHead
        eyebrow="Hiring account"
        title="Register as an independent hirer"
        lede="For hiring on your own account rather than a company's. Verified by a person before you can see anybody — this form is what they read."
      />

      {register.error ? <Failure message={register.error.message} /> : null}

      <Card>
        <div className="section__head">
          <h2 className="section__title">You</h2>
        </div>
        <Field label="Your name" htmlFor="name">
          <input
            className="input"
            id="name"
            value={displayName}
            onChange={(e) => setDisplayName(e.target.value)}
          />
        </Field>
        <Field
          label="Username"
          htmlFor="username"
          help="What you sign in with. Chosen once and never reused, even after an account is closed — it is what keeps your name on your own work."
        >
          <input
            className="input"
            id="username"
            autoComplete="username"
            value={username}
            onChange={(e) => setUsername(e.target.value)}
          />
        </Field>
        <Field
          label="Email"
          htmlFor="email"
          help="Where we reach you. Not how you sign in — colleagues may share an address like hiring@yourcompany.com."
        >
          <input
            className="input"
            id="email"
            type="email"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
          />
        </Field>
        <Field label="Password" htmlFor="password" help="At least 8 characters.">
          <input
            className="input"
            id="password"
            type="password"
            autoComplete="new-password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
        </Field>
      </Card>

      <Card>
        <div className="section__head">
          <h2 className="section__title">Evidence that you are real</h2>
          <span className="section__note">
            A small studio with no company domain and no LinkedIn page is expected to use the first
            two — they exist for exactly that, and an administrator reads them.
          </span>
        </div>

        {PROOF_KINDS.map((p) => (
          <Field key={p.kind} label={p.label} htmlFor={p.kind} help={p.help}>
            {p.free ? (
              <textarea
                className="input"
                id={p.kind}
                rows={2}
                value={proofs[p.kind] ?? ""}
                onChange={(e) => setProofs({ ...proofs, [p.kind]: e.target.value })}
              />
            ) : (
              <input
                className="input"
                id={p.kind}
                value={proofs[p.kind] ?? ""}
                onChange={(e) => setProofs({ ...proofs, [p.kind]: e.target.value })}
              />
            )}
          </Field>
        ))}
      </Card>

      <Button
        variant="primary"
        disabled={!ready || register.pending}
        onClick={() =>
          void register.run({
            email,
            username,
            password,
            display_name: displayName,
            proofs: PROOF_KINDS.filter((p) => (proofs[p.kind] ?? "").trim()).map((p) =>
              p.free
                ? { kind: p.kind, notes: proofs[p.kind] }
                : { kind: p.kind, value: proofs[p.kind] },
            ),
          })
        }
      >
        {register.pending ? "Submitting…" : "Submit for review"}
      </Button>

      <p className="small">
        <Link to="/signin">Already have an account?</Link>
      </p>
    </div>
  );
}
