// Listing a company.
//
// Three screens on one route, decided by what the URL carries (ADR-0017):
//
//   /organisation                 the form
//   /organisation/verify?token=   confirm the address, choose how to sign in
//   /organisation/revise?token=   correct a refusal
//
// Nothing here creates anything. The form writes a submission, the code proves
// the address, and an administrator approving it is what brings the company,
// its address and the owner seat into existence — together. That ordering is
// why nobody can take a company name by typing it into a public form.

import { useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { endpoints } from "../api/endpoints";
import type { HeadcountBand, OnboardingForm, Workplace } from "../contract";
import { HEADCOUNT_BANDS } from "../contract";
import { useSession } from "../hooks/session";
import { useAction } from "../hooks/useAsync";
import { Banner, Button, Card, Failure, Field, PageHead, SectionTitle } from "../ui/primitives";

const EMPTY: OnboardingForm = {
  name: "",
  description: "",
  email: "",
  phone: "",
  headcount: "",
  workplace: "",
  address: { country: "", city: "", postal_code: "", street1: "", street2: "" },
};

export function OnboardingPage({ mode = "submit" }: { mode?: "submit" | "revise" }) {
  const [params] = useSearchParams();
  const token = params.get("token") ?? "";

  if (mode === "revise") return <OnboardingFormScreen token={token} revising />;
  return <OnboardingFormScreen token="" revising={false} />;
}

/* --- the form ------------------------------------------------------------- */

function OnboardingFormScreen({ token, revising }: { token: string; revising: boolean }) {
  const { client } = useSession();
  const [form, setForm] = useState<OnboardingForm>(EMPTY);

  // A fully remote company sends no address (ADR-0024 decision 11). What was
  // typed before switching stays in the disabled fields, unsent.
  const fullyRemote = form.workplace === "fully_remote";
  const { address, ...company } = form;
  const body = fullyRemote ? company : form;

  const submit = useAction(() =>
    client.post<void>(
      revising ? endpoints.public.reviseOrganization() : endpoints.public.onboardOrganization(),
      revising ? { token, ...body } : body,
    ),
  );

  const set = <K extends keyof OnboardingForm>(key: K, value: OnboardingForm[K]) =>
    setForm({ ...form, [key]: value });
  const setAddress = (key: keyof OnboardingForm["address"], value: string) =>
    setForm({ ...form, address: { ...address, [key]: value } });

  // invalid_onboarding names its field (ADR-0017 amendment 4).
  const refused = (field: string) => (submit.error?.field === field ? submit.error.message : null);

  const ready = form.name.trim() && form.email.trim();

  if (submit.data !== undefined) {
    return (
      <div className="page page--narrow">
        <PageHead eyebrow="Your organisation" title="Check your email" />
        <Banner icon="check">
          If <b>{form.email}</b> can receive mail, a confirmation code is on its way. It works once
          and expires in 24 hours.
        </Banner>
        <Banner>Nothing has been created yet. Confirm the address to send it for review.</Banner>
      </div>
    );
  }

  return (
    <div className="page page--narrow">
      <PageHead
        eyebrow="Your organisation"
        title={revising ? "Correct your submission" : "List your organisation"}
      />

      {submit.error ? <Failure message={submit.error.message} /> : null}

      <Card>
        <Field label="Company name" htmlFor="name" error={refused("name")}>
          <input
            className="input"
            id="name"
            value={form.name}
            onChange={(e) => set("name", e.target.value)}
          />
        </Field>
        <Field
          label="What you do"
          htmlFor="description"
          help="A sentence or two. An administrator reads this."
        >
          <textarea
            className="input"
            id="description"
            rows={3}
            value={form.description}
            onChange={(e) => set("description", e.target.value)}
          />
        </Field>
        <Field
          label="Company email"
          htmlFor="email"
          help="The confirmation code goes here."
          error={refused("email")}
        >
          <input
            className="input"
            id="email"
            type="email"
            value={form.email}
            onChange={(e) => set("email", e.target.value)}
          />
        </Field>
        <Field label="Phone" htmlFor="phone">
          <input
            className="input"
            id="phone"
            value={form.phone}
            onChange={(e) => set("phone", e.target.value)}
          />
        </Field>
        <Field
          label="Roughly how many people work there"
          htmlFor="headcount"
          error={refused("headcount")}
        >
          <select
            className="input"
            id="headcount"
            value={form.headcount}
            onChange={(e) => set("headcount", e.target.value as HeadcountBand | "")}
          >
            <option value="">Prefer not to say</option>
            {HEADCOUNT_BANDS.map((band) => (
              <option key={band} value={band}>
                {band}
              </option>
            ))}
          </select>
        </Field>
      </Card>

      <SectionTitle>Main office</SectionTitle>

      <Card>
        <Field label="Office or fully remote" htmlFor="workplace" error={refused("workplace")}>
          <select
            className="input"
            id="workplace"
            value={form.workplace}
            onChange={(e) => set("workplace", e.target.value as Workplace | "")}
          >
            <option value="">Choose one</option>
            <option value="office">We have an office</option>
            <option value="fully_remote">We are fully remote</option>
          </select>
        </Field>
        <Field
          label="Country"
          htmlFor="country"
          help="Two-letter code, such as GB or IN."
          error={refused("address.country") ?? refused("address")}
        >
          <input
            className="input"
            id="country"
            maxLength={2}
            disabled={fullyRemote}
            value={address.country}
            onChange={(e) => setAddress("country", e.target.value.toUpperCase())}
          />
        </Field>
        <Field label="City" htmlFor="city" error={refused("address.city")}>
          <input
            className="input"
            id="city"
            disabled={fullyRemote}
            value={address.city}
            onChange={(e) => setAddress("city", e.target.value)}
          />
        </Field>
        <Field label="Street" htmlFor="street1" error={refused("address.street1")}>
          <input
            className="input"
            id="street1"
            disabled={fullyRemote}
            value={address.street1}
            onChange={(e) => setAddress("street1", e.target.value)}
          />
        </Field>
        <Field label="Street, continued" htmlFor="street2">
          <input
            className="input"
            id="street2"
            disabled={fullyRemote}
            value={address.street2}
            onChange={(e) => setAddress("street2", e.target.value)}
          />
        </Field>
        <Field label="Postcode" htmlFor="postal_code" help="Leave blank if your country has none.">
          <input
            className="input"
            id="postal_code"
            disabled={fullyRemote}
            value={address.postal_code}
            onChange={(e) => setAddress("postal_code", e.target.value)}
          />
        </Field>
      </Card>

      <Button
        variant="primary"
        disabled={!ready || submit.pending}
        onClick={() => void submit.run()}
      >
        {submit.pending ? "Sending…" : "Send me a confirmation code"}
      </Button>

      <p className="field__help">
        Hiring on your own rather than for a company?{" "}
        <Link to="/register">Register as an independent hirer</Link>. Already listed and someone
        added you? <Link to="/redeem">Claim your seat</Link>.
      </p>
    </div>
  );
}

/* --- confirming the address ----------------------------------------------- */

export function OnboardingVerifyPage() {
  const [params] = useSearchParams();
  const token = params.get("token") ?? "";
  const { client } = useSession();

  const [username, setUsername] = useState("");
  const [displayName, setDisplayName] = useState("");
  const [password, setPassword] = useState("");

  const verify = useAction(() =>
    client.post<void>(endpoints.public.verifyOrganization(), {
      token,
      username,
      display_name: displayName,
      password,
    }),
  );

  // 410 means the code WAS valid and no longer is. The two reasons need
  // different things from the reader, so they are separated.
  const gone = verify.error?.status === 410;
  const spent = verify.error?.code === "verification_already_used";

  if (verify.data !== undefined) {
    return (
      <div className="page page--narrow">
        <PageHead eyebrow="Your organisation" title="Now with an administrator" />
        <Banner icon="check">
          Your address is confirmed and <b>{username}</b> is reserved for you.
        </Banner>
        <Banner>An administrator reviews the company next. We will email you either way.</Banner>
      </div>
    );
  }

  return (
    <div className="page page--narrow">
      <PageHead eyebrow="Your organisation" title="Confirm and choose how you will sign in" />

      {verify.error ? <Failure message={verify.error.message} /> : null}

      {gone ? (
        <Card>
          <p>
            {spent ? (
              <>
                That code has already been used. If an administrator has approved the company you
                can <Link to="/signin">sign in</Link>; otherwise you will hear from us.
              </>
            ) : (
              <>
                That code has expired. <Link to="/organisation">Submit the form again</Link> — codes
                last 24 hours so an old message stops working.
              </>
            )}
          </p>
        </Card>
      ) : (
        <Card>
          <Banner>
            No account exists yet. These details are held until the company is approved.
          </Banner>

          <Field
            label="Username"
            htmlFor="username"
            help="What you sign in with. Chosen once and never reused."
          >
            <input
              className="input"
              id="username"
              autoComplete="username"
              value={username}
              onChange={(e) => setUsername(e.target.value)}
            />
          </Field>
          <Field label="Your name" htmlFor="display_name">
            <input
              className="input"
              id="display_name"
              value={displayName}
              onChange={(e) => setDisplayName(e.target.value)}
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

          <Button
            variant="primary"
            disabled={
              !username.trim() || !displayName.trim() || password.length < 8 || verify.pending
            }
            onClick={() => void verify.run()}
          >
            {verify.pending ? "Confirming…" : "Confirm"}
          </Button>
        </Card>
      )}
    </div>
  );
}
