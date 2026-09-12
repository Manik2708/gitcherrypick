// The door.
//
// Two paths, because one GitHub identity can own both a contributor account and
// a hirer seat — so the path you take decides which one you are asking for,
// rather than the server guessing.

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

  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");

  const github = useAction(async () => {
    const body = await client.post<{ authorize_url: string }>(endpoints.auth.githubStart());
    window.location.assign(body.authorize_url);
    return body;
  });

  const hirerGithub = useAction(async () => {
    const body = await client.post<{ authorize_url: string }>(
      endpoints.auth.hirerGithubStart(),
    );
    window.location.assign(body.authorize_url);
    return body;
  });

  const password_ = useAction(async (kind: "hirer" | "admin") => {
    const path = kind === "hirer" ? endpoints.auth.hirerLogin() : endpoints.auth.adminLogin();
    const session = await client.post<SessionResponse>(path, { email, password });
    adopt(principalOf(session), tokensOf(session));
    navigate(kind === "hirer" ? "/search" : "/admin", { replace: true });
    return session;
  });

  const failure = github.error ?? hirerGithub.error ?? password_.error;

  return (
    <div className="page page--narrow">
      <PageHead
        eyebrow="GitCherryPick"
        title="Sign in"
        lede="Prove what you can do with evidence — merged pull requests, judged against the skills you claim."
      />

      {failure ? <Failure message={failure.message} /> : null}

      <Card>
        <p className="eyebrow">Contributors</p>
        <p className="small muted">
          There is no password: your account is your GitHub identity.
        </p>
        <Button variant="primary" disabled={github.pending} onClick={() => void github.run()}>
          <Icon name="commit" size="sm" />
          {github.pending ? "Redirecting…" : "Sign in with GitHub"}
        </Button>
      </Card>

      <Card>
        <p className="eyebrow">Hiring and administration</p>
        <Field label="Email" htmlFor="email">
          <input
            className="input"
            id="email"
            type="email"
            autoComplete="username"
            value={email}
            onChange={(event) => setEmail(event.target.value)}
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
            disabled={hirerGithub.pending}
            onClick={() => void hirerGithub.run()}
          >
            <Icon name="commit" size="sm" />
            {hirerGithub.pending ? "Redirecting…" : "Hirer, with GitHub"}
          </Button>
          <Button
            variant="quiet"
            disabled={password_.pending}
            onClick={() => void password_.run("admin")}
          >
            Sign in as an admin
          </Button>
        </div>
        <p className="field__help">
          New here? <Link to="/register">Register a hiring account</Link>. It is reviewed by a
          person before it can see anybody.
        </p>
      </Card>
    </div>
  );
}
