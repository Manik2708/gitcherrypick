// Coming back from the identity provider.
//
// The code is single-use, so the exchange fires exactly once — a re-run in
// StrictMode or on a re-render would spend it and fail the second time.
//
// Two arrivals land here, because a GitHub identity can be either kind of
// account and only the path taken says which: /auth/github/callback mints a
// contributor session, /auth/hirer/github/callback a hiring one. The API keeps
// them as separate endpoints for the same reason, so `as` picks which to call
// rather than the page guessing from the response.

import { useEffect, useRef, useState } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { endpoints } from "../api/endpoints";
import { principalOf, tokensOf } from "../api/session";
import type { SessionResponse } from "../contract";
import { useSession } from "../hooks/session";
import { Empty, Loading } from "../ui/primitives";

export function AuthCallbackPage({ as = "contributor" }: { as?: "contributor" | "hirer" }) {
  const [params] = useSearchParams();
  const { client, adopt } = useSession();
  const navigate = useNavigate();
  const spent = useRef(false);
  const [error, setError] = useState<string | null>(null);

  const code = params.get("code");
  const state = params.get("state");

  useEffect(() => {
    if (spent.current) return;
    spent.current = true;

    if (!code || !state) {
      setError("That sign-in link is missing its code.");
      return;
    }

    const exchange =
      as === "hirer"
        ? endpoints.auth.hirerGithubCallback(code, state)
        : endpoints.auth.githubCallback(code, state);

    client
      .get<SessionResponse>(exchange)
      .then((session) => {
        adopt(principalOf(session), tokensOf(session));
        navigate(as === "hirer" ? "/search" : "/", { replace: true });
      })
      .catch((cause: unknown) => {
        const message = cause instanceof Error ? cause.message : "That sign-in did not complete.";
        setError(message);
      });
  }, [client, adopt, navigate, code, state, as]);

  if (error) {
    return (
      <div className="page page--narrow">
        <Empty title="That sign-in did not complete">{error}</Empty>
      </div>
    );
  }

  return (
    <div className="page page--narrow">
      <Loading what="your session" />
    </div>
  );
}
