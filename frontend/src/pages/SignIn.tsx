// The door.
//
// Contributors sign in with GitHub; hirers with a username and password or with
// Google; admins with an email and password.

import { useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import { endpoints } from "../api/endpoints";
import { principalOf, tokensOf } from "../api/session";
import type { SessionResponse } from "../contract";
import { useSession } from "../hooks/session";
import { useAction } from "../hooks/useAsync";
import { Button, Card, Failure, Field, Icon, PageHead } from "../ui/primitives";

export function SignInPage() {
  const { client, adopt } = useSession();
  const navigate = useNavigate();

  // A hirer signs in with a USERNAME and an admin with an email (ADR-0016).
  // They are separate fields rather than one "identifier", because the two
  // accounts key on different columns and a shared box would quietly send an
  // address where a username belongs.
  const [username, setUsername] = useState("");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");

  const github = useAction(async () => {
    const body = await client.post<{ authorize_url: string }>(endpoints.auth.githubStart());
    window.location.assign(body.authorize_url);
    return body;
  });

  const hirerGoogle = useAction(async () => {
    const body = await client.get<{ authorize_url: string }>(endpoints.auth.googleStart());
    window.location.assign(body.authorize_url);
    return body;
  });

  const password_ = useAction(async (kind: "hirer" | "admin") => {
    const path = kind === "hirer" ? endpoints.auth.hirerLogin() : endpoints.auth.adminLogin();
    const credentials = kind === "hirer" ? { username, password } : { email, password };
    const session = await client.post<SessionResponse>(path, credentials);
    adopt(principalOf(session), tokensOf(session));
    navigate(kind === "hirer" ? "/search" : "/admin", { replace: true });
    return session;
  });

  const failure = github.error ?? hirerGoogle.error ?? password_.error;

  return (
    <div className="page page--narrow">
      <PageHead eyebrow="GitCherryPick" title="Sign in" />

      {failure ? <Failure message={failure.message} /> : null}

      <Card>
        <p className="eyebrow">Contributors</p>
        <Button variant="primary" disabled={github.pending} onClick={() => void github.run()}>
          <Icon name="commit" size="sm" />
          {github.pending ? "Redirecting…" : "Sign in with GitHub"}
        </Button>
      </Card>

      <Card>
        <p className="eyebrow">Hiring</p>
        <Field label="Username" htmlFor="username" help="Not your email address.">
          <input
            className="input"
            id="username"
            autoComplete="username"
            value={username}
            onChange={(event) => setUsername(event.target.value)}
          />
        </Field>
        <Field label="Password" htmlFor="password">
          <input
            className="input"
            id="password"
            type="password"
            autoComplete="current-password"
            value={password}
            onChange={(event) => setPassword(event.target.value)}
          />
        </Field>
        <div className="od-cluster">
          <Button
            variant="ghost"
            disabled={password_.pending}
            onClick={() => void password_.run("hirer")}
          >
            Sign in as a hirer
          </Button>
          <Button
            variant="ghost"
            disabled={hirerGoogle.pending}
            onClick={() => void hirerGoogle.run()}
          >
            {hirerGoogle.pending ? "Redirecting…" : "Hirer, with Google"}
          </Button>
        </div>
        <p className="field__help">
          Someone at your company added you? <Link to="/redeem">Claim your seat</Link>.
        </p>
        <p className="field__help">
          New company? <Link to="/organisation">List your organisation</Link> — an administrator
          reviews it, and your account is created when they approve it.
        </p>
        <p className="field__help">
          Hiring on your own rather than for a company?{" "}
          <Link to="/register">Register as an independent hirer</Link>.
        </p>
      </Card>

      <Card>
        <p className="eyebrow">Administration</p>
        <Field label="Email" htmlFor="admin-email">
          <input
            className="input"
            id="admin-email"
            type="email"
            autoComplete="email"
            value={email}
            onChange={(event) => setEmail(event.target.value)}
          />
        </Field>
        <Field label="Password" htmlFor="admin-password">
          <input
            className="input"
            id="admin-password"
            type="password"
            autoComplete="current-password"
            value={password}
            onChange={(event) => setPassword(event.target.value)}
          />
        </Field>
        <Button
          variant="quiet"
          disabled={password_.pending}
          onClick={() => void password_.run("admin")}
        >
          Sign in as an admin
        </Button>
      </Card>
    </div>
  );
}
