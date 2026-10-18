// What is out there (ADR-0020).
//
// The first page on this platform that shows a contributor something they were
// not sent. Everything else waits: a company searches, shortlists, and a
// contact request arrives. This inverts that in one direction and no other —
// you can see what exists, and seeing it tells nobody anything.
//
// There is deliberately NO APPLY BUTTON. Reading an opening sends nothing: no
// application, no interest, no view count. The consent model runs one way —
// companies approach, contributors answer — and a second channel would have
// none of the protections the first one has. The page says so plainly, because
// a jobs list with no apply button is otherwise read as a bug.

import type { ContributorOpening } from "../contract";
import { useOpenings } from "../hooks/contributor";
import * as format from "../logic/format";
import { describeForContributor } from "../logic/roleLocations";
import { useCountryNames } from "../ui/countries";
import { Card, Empty, Failure, Loading, PageHead, Pill } from "../ui/primitives";

const ENGAGEMENTS: Record<string, string> = {
  full_time: "Full time",
  contract: "Contract",
  internship: "Internship",
  freelance: "Freelance",
};

function money(minor: number | null, currency: string): string | null {
  if (minor == null || !currency) return null;
  return `${currency} ${(minor / 100).toLocaleString()}`;
}

function OpeningCard({
  opening,
  nameOf,
}: {
  opening: ContributorOpening;
  nameOf: (code: string) => string;
}) {
  const role = opening.role;
  if (!role) return null;

  const pay =
    role.engagement === "freelance"
      ? money(role.hourly_rate, role.currency)
      : money(role.yearly_ctc, role.currency);

  const bar = opening.bar;
  const asks: string[] = [];
  if (bar.min_overall_score != null) asks.push(`overall ${bar.min_overall_score}`);
  if (bar.min_generalist_score != null) asks.push(`generalist ${bar.min_generalist_score}`);
  if (bar.min_oss_yoe != null) asks.push(`${bar.min_oss_yoe}y in open source`);
  for (const s of bar.skills) asks.push(`${s.name ?? s.slug} ${s.min_score}`);

  const process: string[] = [];
  if (role.max_interview_rounds != null) {
    process.push(`at most ${role.max_interview_rounds} rounds`);
  }
  if (role.avg_days_to_offer != null) process.push(`~${role.avg_days_to_offer} days to an offer`);
  if (role.requires_online_test) process.push("an online test");

  return (
    <Card>
      <div className="section__head">
        <h3 className="section__title">{role.title}</h3>
        <Pill tone="primary">{opening.organization}</Pill>
      </div>

      <p className="field__help">
        {ENGAGEMENTS[role.engagement] ?? role.engagement}
        {pay ? ` · ${pay}${role.engagement === "freelance" ? " an hour" : " a year"}` : ""}
        {opening.posted_at ? ` · open since ${format.date(opening.posted_at)}` : ""}
      </p>

      {role.description ? <p className="small">{role.description}</p> : null}

      <p className="field__help">{describeForContributor(role.locations, nameOf)}.</p>

      {/* The bar is shown because you have already cleared it — it discloses
          nothing you could not infer, and a role that says what it wanted is
          more legible than one that simply appeared. */}
      {asks.length > 0 ? (
        <p className="field__help">You meet what it asks: {asks.join(" · ")}.</p>
      ) : null}

      {process.length > 0 ? <p className="field__help">Process: {process.join(", ")}.</p> : null}
    </Card>
  );
}

export function OpeningsPage() {
  const openings = useOpenings();
  const nameOf = useCountryNames();
  const list = openings.data?.openings ?? [];
  const missed = openings.data?.missed ?? 0;

  return (
    <div className="page">
      <PageHead eyebrow="Discovery" title="Open to you" />

      {openings.loading && !openings.data ? <Loading what="open roles" /> : null}
      {openings.error ? (
        <Failure message={openings.error.message} onRetry={openings.reload} />
      ) : null}

      {!openings.loading && list.length === 0 ? (
        <Empty title="Nothing open to you right now" icon="bookmark">
          {missed > 0
            ? `${missed} ${missed === 1 ? "role is" : "roles are"} published that you do not currently meet. Submit a claim to move that number.`
            : "No company has published a role yet."}
        </Empty>
      ) : null}

      {list.map((opening) => (
        <OpeningCard key={opening.id} opening={opening} nameOf={nameOf} />
      ))}

      {/* The count is the whole of what you are told about the rest. It names
          no shortfall and exposes no company's bar — it exists so a short list
          cannot be mistaken for an empty market. */}
      {list.length > 0 && missed > 0 ? (
        <p className="field__help">
          {list.length} open to you · {missed} you do not currently meet.
        </p>
      ) : null}
    </div>
  );
}
