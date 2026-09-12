// How a value reads once it reaches a screen.
//
// The rule that matters: NULL IS NOT ZERO. An unmeasured score says so in
// words, because rendering it as 0 would invent a judgement nobody made — and
// "not measured" is the honest answer for a contributor with no primary skill.

import type { AvailabilityStatus, Standing } from "../contract";

/** One decimal, because the rubric publishes one. */
export function n1(value: number | null | undefined): string {
  return value == null ? "—" : value.toFixed(1);
}

export function score(value: number | null | undefined): string {
  return value == null ? "not measured" : value.toFixed(1);
}

export function rank(value: number | null | undefined): string {
  return value == null ? "unranked" : `#${value}`;
}

export function standing(value: Standing): string {
  return value === "primary" ? "Primary" : "Secondary";
}

export function standingNote(value: Standing): string {
  return value === "primary"
    ? "Ranked and searchable — hirers can find you by this."
    : "Visible and counts toward your scores, but never ranked.";
}

const AVAILABILITY: Record<AvailabilityStatus, string> = {
  looking_for_job: "Looking for a job",
  looking_for_freelance: "Looking for freelance work",
  open_to_freelance: "Open to freelance work",
  not_looking: "Not looking",
};

export function availability(status: AvailabilityStatus | string): string {
  return AVAILABILITY[status as AvailabilityStatus] ?? status;
}

export function date(iso: string | null | undefined): string {
  if (!iso) return "—";
  const when = new Date(iso);
  if (Number.isNaN(when.getTime())) return "—";
  return when.toLocaleDateString(undefined, { day: "numeric", month: "short", year: "numeric" });
}

/** Relative days, so "in 4 days" beats a date the reader has to subtract. */
export function relativeDays(iso: string | null | undefined, now: Date): string {
  if (!iso) return "—";
  const when = new Date(iso);
  if (Number.isNaN(when.getTime())) return "—";

  const days = Math.round((when.getTime() - now.getTime()) / 86_400_000);
  if (days === 0) return "today";
  if (days === 1) return "tomorrow";
  if (days === -1) return "yesterday";
  return days > 0 ? `in ${days} days` : `${Math.abs(days)} days ago`;
}

/** The board a search was ordered by, named the way the API names it. */
export function rankedBy(value: string): string {
  if (value.startsWith("skill:")) return value;
  return value || "overall";
}
