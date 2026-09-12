// What a claim allows, and what it is waiting for.
//
// Every rule here mirrors one the backend enforces. That duplication is
// deliberate and bounded: the client uses these to decide what to OFFER, and
// the server decides what to ALLOW. A disabled button is a courtesy; the 409 is
// the rule. Nothing here may be the only thing standing between a user and a
// state they should not reach.

import type { Claim, ClaimStatus, ClaimSummary, JudgedPR, PREvidence } from "../contract";

/** MAX_PRS is the cardinality bound from ADR-0003. */
export const MAX_PRS = 5;

/** PRIMARY_THRESHOLD is how many distinct surviving PRs promote a skill. */
export const PRIMARY_THRESHOLD = 5;

/** LOCK_DAYS is how long a judged claim is frozen. */
export const LOCK_DAYS = 7;

export function isLocked(claim: Pick<Claim, "locked_until">, now: Date): boolean {
  if (!claim.locked_until) return false;
  return new Date(claim.locked_until).getTime() > now.getTime();
}

export function isEditable(claim: Pick<Claim, "status" | "locked_until">, now: Date): boolean {
  if (claim.status !== "draft" && claim.status !== "invalid") return false;
  return !isLocked(claim, now);
}

/**
 * Evidence as five positioned slots — index i holds position i + 1.
 *
 * A claim is a bounded list of POSITIONED items, not a free-text blob: the
 * server reports validation against a position ("PR 3: …") and a verdict names
 * the position it judged. Editing evidence as one newline-separated field makes
 * position an accident of line order, so clearing the second line silently
 * renumbers everything after it and the errors point at the wrong row.
 */
export function slotsFromEvidence(
  prs: Pick<PREvidence, "position" | "repo_owner" | "repo_name" | "pr_number">[],
): string[] {
  const slots: string[] = Array.from({ length: MAX_PRS }, () => "");
  for (const pr of prs) {
    const index = pr.position - 1;
    if (index >= 0 && index < MAX_PRS && pr.repo_owner && pr.repo_name && pr.pr_number) {
      slots[index] = `https://github.com/${pr.repo_owner}/${pr.repo_name}/pull/${pr.pr_number}`;
    }
  }
  return slots;
}

export function filledSlots(slots: string[]): number {
  return slots.filter((url) => url.trim().length > 0).length;
}

export function isJudged(pr: PREvidence | JudgedPR): pr is JudgedPR {
  return "pr_number" in pr && typeof (pr as JudgedPR).pr_number === "number";
}

/** The best score across the skills a PR was judged against, or null. */
export function bestScore(pr: JudgedPR): number | null {
  if (!pr.scores?.length) return null;
  return pr.scores.reduce((best, s) => (s.score > best ? s.score : best), pr.scores[0]!.score);
}

export function drafts(claims: ClaimSummary[]): ClaimSummary[] {
  return claims.filter((c) => c.status === "draft" || c.status === "invalid");
}

export function queued(claims: ClaimSummary[]): ClaimSummary[] {
  return claims.filter((c) => c.status === "queued");
}

export function judged(claims: ClaimSummary[]): ClaimSummary[] {
  return claims.filter((c) => c.status === "evaluated");
}

const STATUS_LABEL: Record<ClaimStatus, string> = {
  draft: "Draft",
  queued: "Awaiting judgement",
  evaluated: "Judged",
  invalid: "Needs attention",
  withdrawn: "Withdrawn",
};

export function claimStatus(status: ClaimStatus): string {
  return STATUS_LABEL[status] ?? status;
}

/** Why a PR counted toward nothing, in words rather than a code. */
const REJECTION: Record<string, string> = {
  below_quality_floor: "Assessed quality was too low to represent engineering work.",
  trivial_change: "Too small to demonstrate anything.",
  not_merged: "Not merged.",
  not_public: "Not publicly visible.",
  authored_by_claimant: "You wrote this — reviewing your own work is not review.",
  not_reviewed_by_claimant: "Carries no review by you.",
  not_authored_by_claimant: "Written by someone else.",
  duplicate_in_claim: "Already counted toward this skill.",
  github_error: "Could not be read from GitHub.",
  generated_code: "Generated rather than written.",
};

export function rejectionReason(code: string | undefined): string {
  return (code && REJECTION[code]) || "Did not count.";
}

/** The five judged dimensions, in the order the rubric weights them. */
export const DIMENSION_ORDER = [
  "substance",
  "complexity",
  "conversation_quality",
  "craft",
  "skill_specificity",
] as const;

const DIMENSION_LABEL: Record<string, string> = {
  substance: "Substance",
  complexity: "Complexity",
  conversation_quality: "Conversation",
  craft: "Craft",
  skill_specificity: "Skill specificity",
  insight: "Insight",
  judgement: "Judgement",
  communication: "Communication",
  rigour: "Rigour",
  outcome: "Outcome",
};

export function dimensionLabel(name: string): string {
  return DIMENSION_LABEL[name] ?? name;
}

export function orderedDimensions<T>(dimensions: Record<string, T>): [string, T][] {
  const known = DIMENSION_ORDER.filter((n) => n in dimensions).map(
    (n) => [n, dimensions[n]!] as [string, T],
  );
  const rest = Object.entries(dimensions).filter(
    (entry) => !(DIMENSION_ORDER as readonly string[]).includes(entry[0]),
  );
  return [...known, ...rest];
}

export interface PromotionDistance {
  have: number;
  need: number;
  remaining: number;
}

export function promotionDistance(distinctPRCount: number): PromotionDistance {
  return {
    have: distinctPRCount,
    need: PRIMARY_THRESHOLD,
    remaining: Math.max(0, PRIMARY_THRESHOLD - distinctPRCount),
  };
}

/** What the claim still needs before the server will take it. */
export function readyToSubmit(claim: Claim): { ready: boolean; reasons: string[] } {
  const reasons: string[] = [];
  const prs = claim.prs ?? [];
  const skills = claim.skills ?? [];

  if (prs.length === 0) reasons.push("Add at least one pull request.");
  if (prs.length > MAX_PRS) reasons.push(`A claim carries at most ${MAX_PRS} pull requests.`);
  if (skills.length === 0) reasons.push("Declare at least one skill.");
  else if (!skills.some((s) => s.nominated_primary)) {
    reasons.push("Nominate one skill as the primary.");
  }

  return { ready: reasons.length === 0, reasons };
}
