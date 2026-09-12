// Where the session lives between reloads.
//
// sessionStorage rather than localStorage: a hiring account can read a person's
// name and their work, so a shared machine should not keep that past the tab.

import type { Principal, SessionResponse } from "../contract";
import type { Tokens } from "./http";

const ACCESS = "gcp.access";
const REFRESH = "gcp.refresh";
const WHO = "gcp.principal";

function safe<T>(read: () => T, fallback: T): T {
  try {
    return read();
  } catch {
    return fallback;
  }
}

export function readTokens(): Tokens | null {
  return safe(() => {
    const access = sessionStorage.getItem(ACCESS);
    const refresh = sessionStorage.getItem(REFRESH);
    return access && refresh ? { access, refresh } : null;
  }, null);
}

export function writeTokens(tokens: Tokens | null): void {
  safe(() => {
    if (!tokens) {
      sessionStorage.removeItem(ACCESS);
      sessionStorage.removeItem(REFRESH);
      sessionStorage.removeItem(WHO);
      return;
    }
    sessionStorage.setItem(ACCESS, tokens.access);
    sessionStorage.setItem(REFRESH, tokens.refresh);
  }, undefined);
}

export function readPrincipal(): Principal | null {
  return safe(() => {
    const raw = sessionStorage.getItem(WHO);
    return raw ? (JSON.parse(raw) as Principal) : null;
  }, null);
}

export function writePrincipal(principal: Principal | null): void {
  safe(() => {
    if (principal) sessionStorage.setItem(WHO, JSON.stringify(principal));
    else sessionStorage.removeItem(WHO);
  }, undefined);
}

/** A sign-in response names the principal under whichever key applies. */
export function principalOf(session: SessionResponse): Principal | null {
  return session.user ?? session.hirer ?? session.admin ?? null;
}

export function tokensOf(session: SessionResponse): Tokens {
  return { access: session.access_token, refresh: session.refresh_token };
}
