// Whether anybody can find you, and what is left (ADR-0022).
//
// The rules this reports are the SEARCH QUERY'S own — `overall_score IS NOT
// NULL`, the lapse comparison, the availability gate — which is why the server
// decides them and this only renders words. A client deriving them would be a
// second copy of the gate stack, drifting in the direction that leaves
// somebody believing they are visible when they are not.
//
// The split between blocking and limiting is the substance. "Nobody can find
// you" and "you are findable, and remote-only roles cannot reach you" are
// different sentences, and a single checklist would flatten an urgent thing
// and a deliberate choice into the same row.

import { Link } from "react-router-dom";
import type { Readiness } from "../contract";
import { Banner, Card, Icon, Pill, SectionTitle } from "./primitives";

type Explanation = {
  title: string;
  detail: string;
  /** Where to go and fix it, when there is somewhere. */
  fix?: { to: string; label: string };
};

const WHAT: Record<string, Explanation> = {
  availability_never_set: {
    title: "You have not said whether you are looking",
    detail:
      "A new contributor is invisible until they choose to be visible. Turn on “Looking for opportunities” above.",
  },
  opted_out: {
    title: "You have opted out",
    detail:
      "This is working as you set it: you are out of search and out of the ranked population, and no hirer toggle reveals you. Your scores are untouched, and turning the switch back on restores everything.",
  },
  availability_lapsed: {
    title: "Your availability lapsed",
    detail:
      "You said yes, and the fifteen-day window ran out. You keep your place in the ranking — a hirer simply has to go looking for quiet profiles to see you. Turn the switch above off and on to confirm it again.",
  },
  no_scored_claim: {
    title: "None of your work has been scored yet",
    detail:
      "Search only returns contributors with an overall score, and that comes from a claim being judged. Until then you are in no result anywhere, however complete the rest of this page is.",
    fix: { to: "/claims", label: "Submit a claim" },
  },
  no_country: {
    title: "You have not said where you are",
    detail:
      "Any role that can only hire in certain countries cannot reach you — it has no way to know whether you are in one of them.",
  },
  no_verified_first_pr: {
    title: "You have not given your first pull request",
    detail:
      "Roles asking for a minimum number of years in open source cannot reach you. We date it from when the pull request was written and check it, so an unknown figure is left out rather than let through.",
  },
  first_pr_verification_pending: {
    title: "We could not read your first pull request yet",
    detail:
      "It is stated but not confirmed — usually a rate limit on our side rather than anything wrong with the link. We keep retrying; until it lands, roles asking for open-source years treat you as unknown.",
  },
  no_office_yoe: {
    title: "You have not said how long you have worked in a job",
    detail:
      "A role asking for a minimum cannot reach you. It is your own figure, shown to hirers as something you said rather than something we checked.",
  },
  no_preferences: {
    title: "You have not said what you would prefer",
    detail:
      "This does not hide you from anybody — hirers can still find you for any role. What it empties is your own “Open to you” list, which is filtered by what you ticked.",
    fix: { to: "/openings", label: "Open to you" },
  },
};

function Row({ code }: { code: string }) {
  const what = WHAT[code];
  if (!what) return null;
  return (
    <div className="od-row" style={{ ["--od-gap" as string]: "10px" }}>
      <span className="od-field od-fill">
        <span>{what.title}</span>
        <span className="field__help">
          {what.detail}
          {what.fix ? (
            <>
              {" "}
              <Link to={what.fix.to}>{what.fix.label}</Link>.
            </>
          ) : null}
        </span>
      </span>
    </div>
  );
}

export function ReadinessPanel({ readiness }: { readiness: Readiness | undefined }) {
  if (!readiness) return null;

  const { findable, blocking, limiting } = readiness;

  return (
    <Card>
      <SectionTitle
        note={
          findable
            ? "Nothing is stopping a hirer finding you."
            : "Until these are cleared, you are in no search result."
        }
      >
        Can hirers find you?{" "}
        {findable ? <Pill tone="primary">yes</Pill> : <Pill tone="cherry">not yet</Pill>}
      </SectionTitle>

      {findable && limiting.length === 0 ? (
        <Banner icon="check">
          Your profile is complete. Every role that matches your work can reach you.
        </Banner>
      ) : null}

      {blocking.length > 0 ? (
        <>
          <p className="field__help">
            <b>
              {blocking.length === 1
                ? "One thing is stopping you being found"
                : `${blocking.length} things are stopping you being found`}
              .
            </b>{" "}
            A hirer searching today sees nobody where you should be.
          </p>
          {blocking.map((code) => (
            <Row key={code} code={code} />
          ))}
        </>
      ) : null}

      {limiting.length > 0 ? (
        <>
          <p className="field__help">
            <Icon name="info" size="xs" />{" "}
            {findable
              ? "You are findable. These narrow which roles can reach you:"
              : "And these will narrow which roles can reach you once you are:"}
          </p>
          {limiting.map((code) => (
            <Row key={code} code={code} />
          ))}
        </>
      ) : null}
    </Card>
  );
}
