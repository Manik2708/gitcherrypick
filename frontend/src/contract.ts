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

/**
 * Looking, or not (ADR-0021).
 *
 * There were four values; three said "yes" and differed only in what KIND of
 * work somebody wanted, which the OpenTo* preferences answer. LOOKING MEANS
 * OPEN TO ANYTHING — it is the whole of what decides whether a hirer can reach
 * somebody.
 *
 * `not_looking` is a deliberate opt-out that no hirer toggle reveals, and is
 * distinct from having never answered (no availability at all).
 */
export type AvailabilityStatus = "looking" | "not_looking";

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
  /**
   * The organisation this seat acts for.
   *
   * `id` arrives from SIGN-IN and not from `/me`, whose shape is pinned by the
   * stage-3 fixtures and carries name and verification only. Screens that need
   * the id — the roster and the seat list — read it from the stored principal,
   * and say so plainly when it is absent rather than guessing a uuid.
   */
  organization?: { id?: string; name: string; verified: boolean } | null;
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

  /**
   * How many matches were dropped for being on a round for `for_role`
   * already. ABSENT when that filter was not asked for — a zero would read as
   * "nobody" rather than "not asked".
   */
  already_shortlisted?: number;

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
  created_by?: HirerRef | null;
  created_at: string;
}

/* --- attribution (ADR-0016 §9) ------------------------------------------- */

/**
 * Whoever authored a round, an entry or an approach.
 *
 * `active: false` is a seat whose access was revoked. It is reported rather
 * than hidden: that person did the work, and a round from two years ago still
 * needs a name on it. Render them as departed, never as missing.
 */
export interface HirerRef {
  id: string;
  username: string;
  display_name: string;
  active: boolean;
}

/* --- shortlists ---------------------------------------------------------- */

export type ShortlistStatus = "draft" | "open" | "closed";
export type ContactStatus = "pending" | "accepted" | "declined" | "expired";

/**
 * An entry as STAGING returns it — nested user, note, who added it.
 *
 * Distinct from RoundEntry below, which is what reading a round returns. The
 * two are genuinely different shapes for different questions, and treating one
 * as the other is what crashed the round screen: it read `entry.user.id` off a
 * response that has never carried a `user` object.
 */
export interface ShortlistEntry {
  shortlist_id: string;
  user: { id: string; display_name: string; github_login: string };
  note?: string | null;
  added_by?: HirerRef | null;
  /** Null means this candidate has not been told. Removal is only legal then. */
  notified_at?: string | null;
  contact_status?: ContactStatus | null;
  email?: string | null;
  added_at: string;
}

/**
 * An entry as READING A ROUND returns it: flat, and carrying where each
 * candidate stands.
 *
 * `notified_at` is the boundary removal turns on (ADR-0008 §3a) — null means
 * still private and removable, set means they were told and the entry is
 * permanent. `email` appears only once somebody accepted.
 */
export interface RoundEntry {
  user_id: string;
  display_name: string;
  notified_at: string | null;
  contact_status?: ContactStatus | null;
  email?: string | null;
}

export interface ShortlistSummary {
  id: string;
  /** The job this round is for. One round, one role (ADR-0019 §3). */
  role_id: string;
  name: string;
  status: ShortlistStatus;
  tentative_result_date: string;
  entry_count?: number;
  unnotified_count?: number;
  created_by?: HirerRef | null;
  created_at: string;
}

export interface Shortlist extends ShortlistSummary {
  description: string;
  entry_count: number;
  unnotified_count: number;
  first_confirmed_at?: string | null;
  entries: RoundEntry[];
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

  /**
   * What they are being approached FOR (ADR-0019 §2).
   *
   * The platform sends this; the address is still released only on acceptance.
   * Carrying it is what lets somebody decline informed rather than on
   * suspicion. Null only when the role could not be read.
   */
  role: ContactRole | null;
  expires_at: string;
}

/**
 * The job, as the person being approached sees it.
 *
 * Narrower than the hirer's Role: no status, no draft history, no supersedes
 * chain, no hires. What somebody needs in order to answer, and nothing about
 * how the company runs its hiring.
 *
 * `expected` is deliberately absent from the questions. Telling somebody which
 * answer is wanted before they answer turns a screening question into a leading
 * one.
 */
export interface ContactRole {
  id: string;
  title: string;
  description: string;
  engagement: Engagement;
  location: RoleLocation;

  currency: string;
  yearly_ctc: number | null;
  yearly_base: number | null;
  hourly_rate: number | null;
  expected_hours: number | null;

  /** EMPTY MEANS ANYWHERE, not nowhere. A client showing this has to say so. */
  eligible_countries: string[];

  /** The process, disclosed here and nowhere else (ADR-0017). */
  requires_online_test: boolean | null;
  max_interview_rounds: number | null;
  avg_days_to_offer: number | null;

  questions: Array<{ id: string; question: string }>;
}

/* --- roles (ADR-0019) ------------------------------------------------------ */

export type Engagement = "full_time" | "contract" | "internship" | "freelance";
export type RoleLocation = "remote" | "address";
export type RoleStatus = "draft" | "open" | "closed";

/**
 * Why a role stopped being open.
 *
 * `superseded` is written by a revision and offered to nobody — a hirer
 * claiming a role was replaced when nothing replaced it would leave a closed
 * role that lies about why.
 */
export type CloseReason =
  | "not_needed"
  | "hired_elsewhere"
  | "hired_via_platform"
  | "superseded"
  | "other";

export interface RoleQuestion {
  id?: string;
  question: string;
  /** What the organisation hopes for. Reported, never used to filter. */
  expected: boolean | null;
}

export interface Role {
  id: string;
  organization_id: string;
  status: RoleStatus;

  title: string;
  description: string;
  engagement: Engagement;

  location: RoleLocation;
  address_id: string | null;

  currency: string;
  yearly_ctc: number | null;
  yearly_base: number | null;
  hourly_rate: number | null;
  expected_hours: number | null;

  /** Empty means anywhere (ADR-0019 §6). */
  eligible_countries: string[];

  min_office_yoe: number | null;
  min_oss_yoe: number | null;

  requires_online_test: boolean | null;
  max_interview_rounds: number | null;
  avg_days_to_offer: number | null;

  questions: RoleQuestion[];

  opened_at: string | null;
  /** Somebody asked to close it. The role is STILL OPEN. */
  close_requested_at: string | null;
  closed_at: string | null;
  close_reason: CloseReason | null;
  close_note?: string;

  hires: Array<{ user_id: string; recorded_at: string }>;

  /** Whether a contributor can currently see this role (ADR-0020). */
  advertised: boolean;

  /** The role this one replaced. An open role is immutable, so a change is a new row. */
  supersedes: string | null;

  created_at: string;
  updated_at: string;
}

/** What a role draft is written from. Every optional field stays nullable so
 *  "not stated" and "zero" remain different answers. */
export interface RoleDraft {
  title: string;
  description: string;
  engagement: Engagement;
  location: RoleLocation;
  address_id?: string;

  currency: string;
  yearly_ctc: number | null;
  yearly_base: number | null;
  hourly_rate: number | null;
  expected_hours: number | null;

  eligible_countries: string[];
  min_office_yoe: number | null;
  min_oss_yoe: number | null;

  requires_online_test: boolean | null;
  max_interview_rounds: number | null;
  avg_days_to_offer: number | null;

  questions: RoleQuestion[];
}

/** Who in an organisation may do what with a role (ADR-0019 §11). */
export type RoleAuthority = "any_hirer" | "draft_and_approve" | "owners_only";

export interface OrgSettings {
  role_create_authority: RoleAuthority;
  role_update_authority: RoleAuthority;
  role_close_authority: RoleAuthority;
}

export interface ShareLink {
  id: string;
  token: string;
  url: string;
  created_at: string;
}

/**
 * A hirer's own review.
 *
 * `organization.id` is here and NOT on `/me`, whose shape is pinned by the
 * approved fixtures. That makes this the one authenticated read a screen can
 * recover the organisation id from — which the roster and seat lists need.
 */
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

/* --- the roster (ADR-0016) ----------------------------------------------- */

export type OrgRole = "owner" | "member";

/**
 * One address an organisation is willing to seat.
 *
 * There is no token here, deliberately: an entry is an allowlist fact, not a
 * credential. Being listed is guessable, so it is never sufficient — the person
 * proves control of the address before a seat exists.
 */
export interface RosterEntry {
  id: string;
  email: string;
  username: string;
  role: OrgRole;
  added_by: HirerRef;
  /** Set once a seat was created from this entry. */
  redeemed_at?: string | null;
  redeemed_by?: string | null;
  created_at: string;
}

export interface RosterResponse {
  entries: RosterEntry[];
}

/** A seat, live or revoked. A revoked one stays listed (ADR-0016 §5a). */
export interface Seat {
  id: string;
  username: string;
  display_name: string;
  email: string;
  role: OrgRole;
  active: boolean;
  disabled_at?: string | null;
}

export interface SeatsResponse {
  seats: Seat[];
}

/** Name and slug only — the public picker discloses nothing else. */
export interface PublicOrganization {
  name: string;
  slug: string;
}

export interface OrganizationsResponse {
  organizations: PublicOrganization[];
}

/* --- organisation onboarding (ADR-0017) ---------------------------------- */

/**
 * How many people work somewhere, approximately.
 *
 * A band rather than a number: the field means "approx", nobody knows whether
 * 47 means 47, and a band is both easier to answer honestly and harder to
 * answer misleadingly.
 */
export type HeadcountBand = "1-10" | "11-50" | "51-200" | "201-1000" | "1000+";

export const HEADCOUNT_BANDS: HeadcountBand[] = ["1-10", "11-50", "51-200", "201-1000", "1000+"];

export interface OnboardingAddress {
  country: string;
  city: string;
  postal_code: string;
  street1: string;
  street2: string;
}

/**
 * A company describing itself.
 *
 * No owner fields. Whoever proves the company address chooses the username and
 * password, so a form anyone can submit cannot name who will own the result.
 */
export interface OnboardingForm {
  name: string;
  description: string;
  email: string;
  phone: string;
  headcount: HeadcountBand | "";
  address: OnboardingAddress;
}

/** A submission awaiting an administrator, as the admin queue shows it. */
export interface OnboardingSubmission {
  id: string;
  name: string;
  description: string;
  email: string;
  phone: string;
  headcount: HeadcountBand | "";
  address: OnboardingAddress | null;
  owner: { username: string; display_name: string };
  supersedes?: string | null;
  created_at: string;
  age_hours: number;
}

export interface OnboardingQueueResponse {
  total: number;
  submissions: OnboardingSubmission[];
}

/* --- the contributor profile (ADR-0018) ---------------------------------- */

/** What shape of work somebody will take. No freelance flag — availability carries it. */
export interface WorkPreferences {
  open_to_remote: boolean;
  open_to_internships: boolean;
  open_to_onsite: boolean;
  open_to_contract: boolean;
  open_to_freelance: boolean;
  current_country: string;
  /** Self-reported. Null is "not said", which is not zero. */
  office_yoe: number | null;
  /** Asked, not derived — and WRITE-ONCE. A second value is a 409. */
  first_pr_url: string;
  /** Editable, because it goes stale by definition. */
  latest_pr_url: string;
}

export interface ContributorProfile {
  preferences: WorkPreferences;
  availability: Availability | null;
  /**
   * Whether to PROMPT this person to fill the form in. True only when they
   * have never answered.
   *
   * There is no `matchable` beside it any more: ADR-0021 removed the state it
   * described. A live window with no preference ticked used to reach nobody;
   * now it reaches everybody, so the only thing worth prompting about is
   * never having answered at all.
   */
  needs_attention: boolean;

  /** Whether a hirer can find them today, and what is in the way (ADR-0022). */
  readiness: Readiness;
}

/**
 * What stands between a contributor and being found.
 *
 * CODES, not sentences: the words belong to the screen, and the server owns
 * which conditions are true — every one of them is a clause of the search
 * query, and a second copy here would drift in the direction that leaves
 * somebody believing they are visible when they are not.
 */
export interface Readiness {
  /** Whether a hirer searching today would see them at all. */
  findable: boolean;
  /** Nobody can find you until these are cleared. */
  blocking: string[];
  /** Findable, but a whole class of role cannot reach you. */
  limiting: string[];
}

/**
 * What a contributor expects to be paid.
 *
 * A HIRER NEVER RECEIVES THIS. Minor units: 4500 is 45.00 of `currency`.
 */
export interface CompensationExpectation {
  currency: string;
  hourly_rate: number | null;
  yearly_amount: number | null;
}

export interface Country {
  code: string;
  name: string;
}

export interface CountriesResponse {
  countries: Country[];
  /** True when the provider could not be reached — let the person type a code. */
  degraded: boolean;
}

/**
 * One currency, from the same kind of third party as a country.
 *
 * ISO 4217 alpha-3. An amount is only compared against another amount in the
 * SAME code, so this is a closed vocabulary rather than a label (ADR-0018
 * amendment 3).
 */
export interface Currency {
  code: string;
  name: string;
}

export interface CurrenciesResponse {
  currencies: Currency[];
  /** True when the provider could not be reached — let the person type a code. */
  degraded: boolean;
}

/* --- public openings (ADR-0020) -------------------------------------------- */

/**
 * One skill and the score it asks for.
 *
 * Named by SLUG, because the catalogue is slug-keyed everywhere a human
 * touches it — search, claims, the leaderboard. The id comes back on a read
 * and is ignored on a write.
 */
export interface OpeningSkill {
  slug: string;
  skill_id?: string;
  name?: string;
  min_score: number;
}

/**
 * The bar on a published role.
 *
 * Null means NO bar, not a bar of zero — a role open to anybody and one that
 * has decided to accept the lowest score on the platform are different
 * invitations. An absent score on the contributor's side clears nothing.
 */
export interface OpeningBar {
  min_overall_score: number | null;
  min_generalist_score: number | null;
  min_oss_yoe: number | null;
  skills: OpeningSkill[];
}

/** An advert, as the hirer who wrote it sees it. */
export interface Opening extends OpeningBar {
  id: string;
  role_id: string;
  /** Published and not withdrawn, reported rather than derived from two dates. */
  live: boolean;
  published_at: string | null;
  withdrawn_at: string | null;
}

/**
 * An advert, as the contributor reading it sees it.
 *
 * No apply route exists and none will: reading one sends nothing to the
 * company — no application, no interest, no view count (ADR-0020 §8).
 */
export interface ContributorOpening {
  id: string;
  organization: string;
  role: ContactRole | null;
  bar: OpeningBar;
  /** The ROLE'S opened_at. A company that advertised late is not fresher. */
  posted_at: string | null;
}

export interface OpeningsResponse {
  openings: ContributorOpening[];
  matched: number;
  /** A bare count of live openings this contributor does not clear. */
  missed: number;
}

/** Somebody already on a round for a role (ADR-0019 amendment 2). */
export interface RoleCandidate {
  user_id: string;
  display_name: string;
  github_login?: string;
  shortlist_id: string;
  shortlist_name: string;
  /** Empty while still staged — told nothing, and removable. */
  contact_status: string;
  notified_at: string | null;
  /** The only state a hire may be recorded from. */
  accepted: boolean;
}
