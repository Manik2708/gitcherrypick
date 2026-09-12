// Who is signed in, and the one client everything shares.
//
// The principal is held in state and mirrored to sessionStorage, so a reload
// does not bounce a signed-in hirer back to the door. The client is built once:
// it owns the refresh-token rotation, and two clients would race each other.

import { createContext, useCallback, useContext, useMemo, useState } from "react";
import type { ReactNode } from "react";
import { Client } from "../api/http";
import type { Tokens } from "../api/http";
import { endpoints } from "../api/endpoints";
import { readPrincipal, readTokens, writePrincipal, writeTokens } from "../api/session";
import type { Principal } from "../contract";
import { useAsync } from "./useAsync";

interface Session {
  principal: Principal | null;
  client: Client;
  adopt: (principal: Principal | null, tokens: Tokens) => void;
  signOut: () => Promise<void>;
}

const SessionContext = createContext<Session | null>(null);

export function SessionProvider({ children }: { children: ReactNode }) {
  const [principal, setPrincipal] = useState<Principal | null>(() => readPrincipal());

  const client = useMemo(
    () =>
      new Client(readTokens, (tokens) => {
        writeTokens(tokens);
        if (!tokens) {
          writePrincipal(null);
          setPrincipal(null);
        }
      }),
    [],
  );

  const adopt = useCallback((next: Principal | null, tokens: Tokens) => {
    writeTokens(tokens);
    writePrincipal(next);
    setPrincipal(next);
  }, []);

  const signOut = useCallback(async () => {
    try {
      await client.post(endpoints.auth.logout());
    } catch {
      // A failed logout still ends the session on this device: the tokens are
      // gone from storage either way, and leaving the UI signed in would be a
      // worse lie than a server-side session that outlives the tab.
    }
    writeTokens(null);
    writePrincipal(null);
    setPrincipal(null);
  }, [client]);

  const value = useMemo(
    () => ({ principal, client, adopt, signOut }),
    [principal, client, adopt, signOut],
  );

  return <SessionContext.Provider value={value}>{children}</SessionContext.Provider>;
}

export function useSession(): Session {
  const session = useContext(SessionContext);
  if (!session) throw new Error("useSession used outside a SessionProvider");
  return session;
}

export function useClient(): Client {
  return useSession().client;
}

/**
 * The principal as the server currently sees it.
 *
 * The cached copy answers the first render; this re-reads, because an account
 * approved since the tab opened would otherwise keep showing as pending.
 */
export function useWhoAmI() {
  const { client, principal } = useSession();
  return useAsync<Principal>(
    (signal) => client.get<Principal>(endpoints.me.root(), signal),
    [client],
    principal !== null,
  );
}
