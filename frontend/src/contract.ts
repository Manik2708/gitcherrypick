// The wire, as types.
//
// Every shape here mirrors a response body in backend/internal/controller. Two
// of its habits matter more than the rest:
//
//   A score is `number | null`. NULL means "not measured" — no primary skill
//   yet, or a pair the model dropped — and it is never 0. Rendering an absent
//   score as zero invents a judgement nobody made.
//
//   A rank is a LABEL, not an index. Ranks are computed over the whole scored
//   population and only then filtered, so a filtered list legitimately has gaps
//   and `results[i].rank === i + 1` is wrong (ADR-0008).

export type PrincipalType = "contributor" | "hirer" | "admin";

export type Standing = "primary" | "secondary";

export type AvailabilityStatus =
  | "looking_for_job"
  | "looking_for_freelance"
  | "open_to_freelance"
  | "not_looking";

export interface Availability {
  status: AvailabilityStatus;
  expires_at?: string | null;
  last_confirmed_at?: string | null;
  inactive_for_days?: number;
}

export interface Principal {
  id: string;
  display_name: string;
  github_login?: string;
  email?: string;
  principal_type: PrincipalType;
  availability?: Availability | null;
  verified?: boolean;
  organization?: { name: string; verified: boolean } | null;
  capabilities?: {
    can_search: boolean;
    can_shortlist: boolean;
    can_view_scorecards: boolean;
  };
}

export interface SessionResponse {
  user?: Principal;
  hirer?: Principal;
  admin?: Principal;
  access_token: string;
  refresh_token: string;
}

/* --- discovery ----------------------------------------------------------- */

export interface ResultSkill {
  slug: string;
  name?: string;
  standing: Standing;
  score: number | null;
}

export interface SearchResult {
  rank: number;
  id: string;
  display_name: string;
  github_login: string;
  active: boolean;
  availability?: Availability | null;
  skills: ResultSkill[];
  overall_score: number | null;
  generalist_score: number | null;
}

export interface SearchResponse {
  total: number;
  inactive_hidden: number;
  ranked_by: string;
  page: number;
  per_page: number;
  results: SearchResult[];
}

export interface LeaderboardEntry {
  rank: number;
  user_id: string;
  display_name: string;
  github_login: string;
  score: number | null;
  active: boolean;
}

export interface LeaderboardResponse {
  kind: "overall" | "generalist" | "skill";
  skill?: { slug: string; name: string } | null;
  rubric_version: string;
  entries: LeaderboardEntry[];
}

/** One judged dimension: what it scored, and the model's reason for it. */
export interface DimensionVerdict {
  score: number;
  remark: string;
}

export interface ScorecardEvidence {
  repo: string;
  pr_number: number;
  url?: string;
  merged_at: string;
  score: number | null;
  /** Absent on a share link, and on PRs judged before remarks were carried. */
  dimensions?: Record<string, DimensionVerdict>;
}

export interface ScorecardSkill {
  slug: string;
  name: string;
  standing: Standing;
  distinct_pr_count: number;
  score: number | null;
  /** Present only for a primary skill; a secondary one is deliberately unranked. */
  rank?: number | null;
  evidence: ScorecardEvidence[];
}

export interface Scorecard {
  user: {
    id?: string;
    display_name: string;
    github_login: string;
    active?: boolean;
    availability?: Availability | null;
  };
  skills: ScorecardSkill[];
  overall_score: number | null;
  generalist_score: number | null;
  rubric_version: string;
  /** Released only where the contributor accepted a contact request. */
  email?: string | null;
}

export interface SavedSearch {
  id: string;
  name: string;
  filters: Record<string, unknown>;
  created_at: string;
}

/* --- shortlists ---------------------------------------------------------- */

export type ShortlistStatus = "draft" | "open" | "closed";
export type ContactStatus = "pending" | "accepted" | "declined" | "expired";

export interface ShortlistEntry {
  shortlist_id: string;
  user: { id: string; display_name: string; github_login: string };
  note?: string | null;
  /** Null means this candidate has not been told. Removal is only legal then. */
  notified_at?: string | null;
  contact_status?: ContactStatus | null;
  email?: string | null;
  added_at: string;
}

export interface ShortlistSummary {
  id: string;
  name: string;
  status: ShortlistStatus;
  tentative_result_date: string;
  entry_count?: number;
  unnotified_count?: number;
  created_at: string;
}

export interface Shortlist extends ShortlistSummary {
  description: string;
  entry_count: number;
  unnotified_count: number;
  first_confirmed_at?: string | null;
  entries: ShortlistEntry[];
  organization?: { name: string; verified: boolean } | null;
}

/** `irreversible` arrives on the wire because the platform offers no way back. */
export interface ConfirmResult {
  notified: number;
  already_notified: number;
  irreversible: boolean;
}

/* --- contributor: standing ------------------------------------------------ */

export interface UserSkill {
  slug: string;
  name?: string;
  standing: Standing;
  distinct_pr_count: number;
  score: number;
  rubric_version: string;
  /** Judged under a superseded rubric, so not comparable with a current board. */
  stale?: boolean;
  promoted_at?: string | null;
}

export interface MySkills {
  skills: UserSkill[];
  overall_score: number | null;
  generalist_score: number | null;
  rubric_version: string;
  stale?: boolean;
  reevaluation_in_progress?: boolean;
}

export interface MyRank {
  rubric_version: string;
  ranked: boolean;
  /** "opted_out" when the contributor said they are not looking. */
  unranked_reason?: string | null;
  active: boolean;
  inactive_for_days?: number;
  overall: { score: number | null; rank: number | null; out_of: number };
  generalist: { score: number | null; rank: number | null; out_of: number };
}

/* --- contributor: claims -------------------------------------------------- */

export type ClaimStatus = "draft" | "queued" | "evaluated" | "invalid" | "withdrawn";
export type EvidenceRole = "author" | "reviewer";

export interface PREvidence {
  position: number;
  repo_owner: string | null;
  repo_name: string | null;
  pr_number: number | null;
  role: EvidenceRole;
  invalid_reason?: string;
}

export interface PRScore {
  skill: string;
  score: number;
}

/** A judged dimension: the remark is the reasoning, the score only summarises it. */
export interface Dimension {
  score: number;
  remark: string;
}

export interface JudgedPR {
  position: number;
  pr_number: number;
  repo_owner?: string;
  repo_name?: string;
  /** What the contributor pasted — absent on older claims. */
  url?: string;
  scores?: PRScore[];
  dimensions?: Record<string, Dimension>;
  /** The model read it and it counted toward nothing. */
  rejected?: boolean;
  rejection_reason?: string;
  /** Nobody judged it — it could not be read. Different from rejected. */
  skipped?: boolean;
  reason?: string;
  message?: string;
}

export interface ClaimSkill {
  slug: string;
  origin: string;
  nominated_primary: boolean;
  scoring_mode?: string;
  score?: number | null;
  standing?: Standing;
}

/** The model proposes; only the contributor promotes. Inert until accepted. */
export interface Suggestion {
  skill_id: string;
  slug: string;
  name: string;
  rationale: string;
  accepted_at?: string | null;
  dismissed_at?: string | null;
}

export interface ProjectEvidence {
  repo_owner: string;
  repo_name: string;
  contribution_summary?: string;
  maintainer_declared?: boolean;
}

export interface Claim {
  id: string;
  status: ClaimStatus;
  version: number;
  nominated_primary?: string;
  prs?: (PREvidence | JudgedPR)[];
  projects?: ProjectEvidence[];
  skills?: ClaimSkill[];
  suggestions?: Suggestion[];
  submitted_at?: string | null;
  evaluated_at?: string | null;
  locked_until?: string | null;
}

export interface ClaimSummary {
  id: string;
  status: ClaimStatus;
  version: number;
  nominated_primary: string | null;
  pr_count: number;
  skill_count: number;
  submitted_at: string | null;
  evaluated_at: string | null;
  locked_until?: string | null;
}

export interface ValidationItem {
  position?: number;
  reason?: string;
  message: string;
}

/** What withdrawing would cost, named before it happens. */
export interface Demotion {
  skill: string;
  from: Standing;
  to: Standing | "none";
  distinct_pr_count_after: number;
  message: string;
}

export interface SkillMatch {
  slug: string;
  name: string;
  matched_via?: "name" | "alias";
  matched_alias?: string;
}

/* --- contributor: disputes and discoverability ---------------------------- */

export interface ReevaluationStatus {
  rejection_count: number;
  tier: number;
  cooldown_until?: string | null;
  cooldown_days?: number;
  can_request: boolean;
  blocked_by?: "pending_request" | "cooldown" | string;
  pending_request_id?: string | null;
  claims_eligible: string[];
}

export interface ContactRequest {
  id: string;
  organization: { name: string; verified: boolean; payment_verified: boolean };
  status: ContactStatus;
  tentative_result_date: string;
  /** The ADR-0002 §5 warning, or null when there is nothing to warn about. */
  disclosure: string | null;
  expires_at: string;
}

export interface ShareLink {
  id: string;
  token: string;
  url: string;
  created_at: string;
}

export interface VerificationStatus {
  status: "pending" | "approved" | "rejected";
  hirer_verified: boolean;
  organization: { id?: string; name: string; verified: boolean; payment_verified?: boolean } | null;
  submitted_at: string | null;
  reviewed_at: string | null;
  reason: string | null;
  can_appeal?: boolean;
}

/* --- admin ---------------------------------------------------------------- */

export interface VerificationProof {
  kind: string;
  value?: string;
  notes?: string;
  attachment_url?: string;
}

export interface VerificationRequest {
  id: string;
  subject: { kind: string; display_name: string };
  hirer?: { id: string; display_name: string; email: string } | null;
  organization?: { id: string; name: string } | null;
  status: string;
  age_hours: number;
  proofs: VerificationProof[];
  created_at: string;
}

export interface SkillRequest {
  id: string;
  requested_by: { id: string; display_name: string };
  proposed_name: string;
  rationale: string;
  status: string;
  created_at: string;
}

export interface ReevaluationRequest {
  id: string;
  user: { id: string; display_name: string };
  claim_id?: string;
  reason: string;
  status: string;
  created_at: string;
}

export interface SweepResult {
  from_rubric_version: string;
  to_rubric_version: string;
  claims_enqueued: number;
  created_at: string;
}
