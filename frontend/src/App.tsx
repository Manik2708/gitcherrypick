// Routing and the shell.
//
// The sidebar is per principal type, because the three accounts are three
// different products: a contributor manages evidence, a hirer searches a ranked
// pool, an admin drains queues. Showing all of it and refusing most of it would
// be worse than showing what applies.
//
// The shell markup follows design/index.html: a fixed rail and a scrolling
// main, so the nav never scrolls away from a long results page.

import type { ReactNode } from "react";
import { Link, Navigate, Route, Routes, useLocation } from "react-router-dom";
import { useSession } from "./hooks/session";
import { AdminQueuesPage } from "./pages/AdminQueues";
import { AuthCallbackPage } from "./pages/AuthCallback";
import { ClaimDetailPage } from "./pages/ClaimDetail";
import { ClaimsPage } from "./pages/Claims";
import { DiscoverabilityPage } from "./pages/Discoverability";
import { DisputeStatusPage } from "./pages/DisputeStatus";
import { LandingPage, PublicNav } from "./pages/Landing";
import { LeaderboardPage } from "./pages/Leaderboard";
import { MyStandingPage } from "./pages/MyStanding";
import { NotFoundPage } from "./pages/NotFound";
import { PublicScorecardPage } from "./pages/PublicScorecard";
import { OnboardingPage, OnboardingVerifyPage } from "./pages/Onboarding";
import { RedeemPage } from "./pages/Redeem";
import { RegisterPage } from "./pages/Register";
import { SavedSearchesPage } from "./pages/SavedSearches";
import { ScorecardPage } from "./pages/Scorecard";
import { SearchPage } from "./pages/Search";
import { ShortlistDetailPage } from "./pages/ShortlistDetail";
import { RolesPage } from "./pages/Roles";
import { ShortlistsPage } from "./pages/Shortlists";
import { SignInPage } from "./pages/SignIn";
import { TeamPage } from "./pages/Team";
import { ErrorBoundary } from "./ui/ErrorBoundary";
import { ScrollToHash } from "./ui/ScrollToHash";
import { Avatar, Icon } from "./ui/primitives";
import type { IconName } from "./ui/primitives";

function NavItem({
  to,
  icon,
  children,
  count,
}: {
  to: string;
  icon: IconName;
  children: ReactNode;
  count?: number;
}) {
  const { pathname } = useLocation();
  const here = pathname === to || pathname.startsWith(`${to}/`);
  return (
    <Link className="nav__item" aria-current={here ? "page" : undefined} to={to}>
      <Icon name={icon} />
      <span>{children}</span>
      {count != null && count > 0 ? <span className="nav__count">{count}</span> : null}
    </Link>
  );
}

function Sidebar() {
  const { principal, signOut } = useSession();
  if (!principal) return null;

  const org = principal.organization;

  return (
    <header className="sidebar">
      <div className="sidebar__brand">
        <span className="brand-mark" aria-hidden="true">
          <svg
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            strokeWidth="1.6"
            strokeLinecap="round"
            strokeLinejoin="round"
          >
            <circle cx="7.5" cy="17" r="3.2" />
            <circle cx="16.5" cy="17" r="3.2" />
            <path d="M12 13.8V9.2M12 9.2c0-2.6 1.7-4.6 4.3-5.2M12 9.2C12 6.6 10.3 4.6 7.7 4" />
          </svg>
        </span>
        <span className="brand-name">GitCherryPick</span>
        <span className="brand-sub">
          {principal.principal_type === "hirer"
            ? "Hiring console"
            : principal.principal_type === "admin"
              ? "Administration"
              : "Your evidence"}
        </span>
      </div>

      <nav className="nav" aria-label="Primary">
        {principal.principal_type === "contributor" ? (
          <>
            <NavItem to="/" icon="board">
              Standing
            </NavItem>
            <NavItem to="/claims" icon="commit">
              Claims
            </NavItem>
            <NavItem to="/disputes" icon="warn">
              Disputes
            </NavItem>
            <NavItem to="/being-found" icon="search">
              Being found
            </NavItem>
          </>
        ) : null}

        {principal.principal_type === "admin" ? (
          <NavItem to="/admin" icon="list">
            Review queues
          </NavItem>
        ) : null}

        {principal.principal_type === "hirer" ? (
          <>
            <NavItem to="/search" icon="search">
              Search
            </NavItem>
            <NavItem to="/leaderboard" icon="board">
              Leaderboard
            </NavItem>
            <NavItem to="/roles" icon="bookmark">
              Roles
            </NavItem>
            <NavItem to="/shortlists" icon="bookmark">
              Shortlists
            </NavItem>
            <NavItem to="/saved-searches" icon="list">
              Saved searches
            </NavItem>
            <NavItem to="/team" icon="people">
              Team
            </NavItem>
          </>
        ) : null}
      </nav>

      <div className="sidebar__foot">
        {org ? (
          <div className="org-card">
            <div className="od-row" style={{ ["--od-gap" as string]: "10px" }}>
              <Avatar name={org.name} square tint={2} />
              <span className="od-field od-fill">
                <span className="org-card__name od-truncate">{org.name}</span>
                <span className="org-card__meta">
                  {org.verified ? (
                    <>
                      <Icon name="check" size="xs" />
                      Verified organisation
                    </>
                  ) : (
                    "Awaiting verification"
                  )}
                </span>
              </span>
            </div>
          </div>
        ) : null}

        <div className="user-card od-row" style={{ ["--od-gap" as string]: "10px" }}>
          <Avatar name={principal.display_name} size="sm" tint={4} />
          <span className="od-field od-fill">
            <span className="user-card__name od-truncate">{principal.display_name}</span>
            <button
              type="button"
              className="user-card__meta od-truncate"
              onClick={() => void signOut()}
            >
              Sign out
            </button>
          </span>
        </div>
      </div>
    </header>
  );
}

/** RequireSession sends a visitor to sign in rather than rendering an empty shell. */
function RequireSession({ children }: { children: ReactNode }) {
  const { principal } = useSession();
  const location = useLocation();

  if (!principal) {
    return <Navigate to="/signin" replace state={{ from: location.pathname }} />;
  }
  return <>{children}</>;
}

/** Home is whichever screen the account actually uses. */
function Home() {
  const { principal } = useSession();

  switch (principal?.principal_type) {
    case "hirer":
      return <Navigate to="/search" replace />;
    case "admin":
      return <Navigate to="/admin" replace />;
    case "contributor":
      return <MyStandingPage />;
    default:
      return <LandingPage />;
  }
}

export function App() {
  const { principal } = useSession();

  // Signed out there is no rail, so there is no grid either — the sign-in and
  // callback screens centre themselves inside main.
  return (
    <div className={principal ? "shell" : undefined}>
      {principal ? <Sidebar /> : <PublicNav />}
      <main className="main" id="main">
        <div className="screen">
          <ScrollToHash />
          <ErrorBoundary>
            <Routes>
              <Route path="/signin" element={<SignInPage />} />
              <Route path="/register" element={<RegisterPage />} />

              {/* Unauthenticated by necessity: whoever is here has no account
                  yet, and the second step is what creates one (ADR-0016 §3). */}
              <Route path="/redeem" element={<RedeemPage />} />

              {/* Listing a company. Public, because onboarding is how an OWNER
                  comes to exist — there is nobody to be signed in as yet
                  (ADR-0017 §1). */}
              <Route path="/organisation" element={<OnboardingPage />} />
              <Route path="/organisation/verify" element={<OnboardingVerifyPage />} />
              <Route path="/organisation/revise" element={<OnboardingPage mode="revise" />} />
              <Route path="/auth/github/callback" element={<AuthCallbackPage />} />
              <Route path="/auth/hirer/github/callback" element={<AuthCallbackPage as="hirer" />} />
              {/* The same GitHub identity can own both kinds of account, so the
                  path is what says which session to mint. Without this route the
                  hirer return landed on the 404. */}

              <Route path="/s/:token" element={<PublicScorecardPage />} />

              <Route path="/" element={<Home />} />

              <Route
                path="/claims"
                element={
                  <RequireSession>
                    <ClaimsPage />
                  </RequireSession>
                }
              />
              <Route
                path="/claims/:claimId"
                element={
                  <RequireSession>
                    <ClaimDetailPage />
                  </RequireSession>
                }
              />
              <Route
                path="/disputes"
                element={
                  <RequireSession>
                    <DisputeStatusPage />
                  </RequireSession>
                }
              />
              <Route
                path="/being-found"
                element={
                  <RequireSession>
                    <DiscoverabilityPage />
                  </RequireSession>
                }
              />
              <Route
                path="/admin"
                element={
                  <RequireSession>
                    <AdminQueuesPage />
                  </RequireSession>
                }
              />
              <Route
                path="/search"
                element={
                  <RequireSession>
                    <SearchPage />
                  </RequireSession>
                }
              />
              <Route
                path="/contributors/:userId"
                element={
                  <RequireSession>
                    <ScorecardPage />
                  </RequireSession>
                }
              />
              <Route
                path="/leaderboard"
                element={
                  <RequireSession>
                    <LeaderboardPage />
                  </RequireSession>
                }
              />
              <Route
                path="/roles"
                element={
                  <RequireSession>
                    <RolesPage />
                  </RequireSession>
                }
              />
              <Route
                path="/shortlists"
                element={
                  <RequireSession>
                    <ShortlistsPage />
                  </RequireSession>
                }
              />
              <Route
                path="/shortlists/:shortlistId"
                element={
                  <RequireSession>
                    <ShortlistDetailPage />
                  </RequireSession>
                }
              />
              <Route
                path="/saved-searches"
                element={
                  <RequireSession>
                    <SavedSearchesPage />
                  </RequireSession>
                }
              />
              <Route
                path="/team"
                element={
                  <RequireSession>
                    <TeamPage />
                  </RequireSession>
                }
              />

              <Route path="*" element={<NotFoundPage />} />
            </Routes>
          </ErrorBoundary>
        </div>
      </main>
    </div>
  );
}
