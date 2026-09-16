// What the platform is, for somebody who has not signed in.
//
// The root used to redirect a visitor to the sign-in form, which asked for a
// GitHub identity before saying what it would be used for. A contributor is
// being asked to submit their best work for judgement and a hirer is being
// asked to be verified by a person — neither is a reasonable thing to consent
// to from a login box.
//
// Every claim on this page is a rule the backend actually enforces: five
// surviving pull requests, five judged dimensions, the seven-day freeze, the
// one-way consent step. Nothing here is aspirational copy, because a landing
// page that oversells is the first thing a contributor will catch us on.
//
// The figures are inline SVG rather than photographs: they show mechanisms,
// they inherit the palette, they stay sharp, and they cost no request.

import { Link } from "react-router-dom";
import { FlowDeck } from "../ui/FlowDeck";
import { RulesFlow } from "../ui/RulesFlow";
import { Icon } from "../ui/primitives";

/** Evidence goes in as five positioned slots; a verdict comes back per PR per skill. */
function ClaimFigure() {
  return (
    <svg viewBox="0 0 360 196" role="img" aria-labelledby="figClaim" className="fig">
      <title id="figClaim">
        Five pull request slots feed one judgement, which returns a score and a written remark for
        each of five dimensions.
      </title>
      {[0, 1, 2, 3, 4].map((i) => (
        <g key={i}>
          <rect
            x="4"
            y={10 + i * 34}
            width="118"
            height="26"
            rx="6"
            className={i < 4 ? "fig-slot fig-slot--on" : "fig-slot"}
          />
          <text x="16" y={27 + i * 34} className="fig-mono">
            PR {i + 1}
          </text>
          {i < 4 ? (
            <text x="100" y={27 + i * 34} className="fig-tick">
              ✓
            </text>
          ) : null}
        </g>
      ))}

      <path d="M128 98 H152" className="fig-wire" />
      <polygon points="152,94 160,98 152,102" className="fig-arrow" />

      <rect x="168" y="52" width="188" height="92" rx="10" className="fig-box" />
      <text x="184" y="76" className="fig-label">
        one judgement, five dimensions
      </text>
      {[0, 1, 2, 3, 4].map((i) => (
        <g key={i}>
          <rect x="184" y={86 + i * 11} width="120" height="5" rx="2.5" className="fig-track" />
          <rect
            x="184"
            y={86 + i * 11}
            width={[102, 94, 110, 86, 72][i]}
            height="5"
            rx="2.5"
            className="fig-fill"
          />
        </g>
      ))}
      <text x="316" y="108" className="fig-score">
        82.4
      </text>
    </svg>
  );
}

export function LandingPage() {
  return (
    <div className="landing">
      <section className="lp-hero">
        <div className="lp-hero__copy">
          <p className="eyebrow">Evidence, not adjectives</p>
          <h1 className="lp-title display">Prove what you can do with merged code.</h1>
          <p className="lp-lede">
            Anyone can write “expert in Go” on a profile. GitCherryPick asks for up to five merged
            pull requests instead, judges each one against every skill you claim, and turns the
            result into a standing you did not get to declare.
          </p>
          <div className="od-cluster">
            <Link className="btn btn--primary" to="/signin">
              <Icon name="commit" size="sm" />
              Sign in with GitHub
            </Link>
            <Link className="btn btn--ghost" to="/organisation">
              List your organisation
            </Link>
          </div>
        </div>
        <div className="lp-hero__figure">
          <ClaimFigure />
        </div>
      </section>

      <FlowDeck />

      <section className="lp-band" id="fair">
        <h2 className="section__title">How a pull request is judged</h2>
        <p className="lp-lede">
          Every pull request is scored against every skill claimed. Here is one that does not
          survive it, and what that costs the claim it was part of.
        </p>
        <RulesFlow />
      </section>

      <section className="lp-cta">
        <h2 className="lp-title display">Show the work.</h2>
        <p className="lp-lede">
          Sign in with GitHub and submit your first claim. It takes five pull requests.
        </p>
        <div className="od-cluster lp-cta__actions">
          <Link className="btn btn--primary" to="/signin">
            <Icon name="commit" size="sm" />
            Sign in with GitHub
          </Link>
          <Link className="btn btn--ghost" to="/organisation">
            List your organisation
          </Link>
        </div>
      </section>
    </div>
  );
}

/** The bar a visitor sees: no rail, because there is no product to navigate yet. */
export function PublicNav() {
  return (
    <header className="lp-nav">
      <Link className="lp-nav__brand" to="/">
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
      </Link>
      {/* These carry "/" so they resolve to the landing route rather than to
          whatever page the visitor is on — a bare "#how" from /signin lands on
          /signin#how, which is a page with no such section. */}
      <nav className="lp-nav__links" aria-label="About">
        <Link to="/#how">How it works</Link>
        <Link to="/#hirers">For hirers</Link>
        <Link to="/#fair">Fairness</Link>
      </nav>
      <Link className="btn btn--quiet btn--sm" to="/signin">
        Sign in
      </Link>
      <Link className="btn btn--primary btn--sm" to="/organisation">
        List your organisation
      </Link>
    </header>
  );
}
