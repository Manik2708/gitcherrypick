// The hirer console's reads and writes.
//
// Every filter name here is a `saved_searches.filters` key, and an unknown one
// is a 422 rather than a silent drop (ADR-0008 §1) — so the query builder emits
// only names the server knows.

import { endpoints } from "../api/endpoints";
import type {
  CloseReason,
  ConfirmResult,
  LeaderboardResponse,
  Opening,
  OpeningBar,
  OrgSettings,
  RoleCandidate,
  Role,
  RoleDraft,
  SavedSearch,
  Scorecard,
  SearchResponse,
  Shortlist,
  ShortlistSummary,
  VerificationStatus,
} from "../contract";
import { useClient, useSession } from "./session";
import { useAction, useAsync } from "./useAsync";

export interface SearchFilters {
  skills?: string[];
  q?: string;
  minSkillScore?: number;
  minOverallScore?: number;
  minGeneralistScore?: number;

  /**
   * What a contributor said about themselves (ADR-0018), as opposed to what
   * their evidence shows.
   *
   * Every one is opt-in, so an unstated value does NOT clear a stated minimum.
   * And there is deliberately no pay filter: a hirer able to filter on what
   * somebody expects would learn an upper bound across a few searches, and an
   * expectation would stop being a floor and become a ceiling (ADR-0018 §5).
   */
  minOfficeYoe?: number;
  minOssYoe?: number;
  countries?: string[];
  openTo?: string[];

  /**
   * Exclude everybody already on a round for this job.
   *
   * The only filter here about the HIRER'S own history rather than about the
   * contributor. It must name one of their own roles; the server refuses any
   * other, because the count of who was excluded would otherwise say how many
   * people matching this query a competitor has already approached.
   */
  forRole?: string;

  includeInactive?: boolean;
  page?: number;
  perPage?: number;
}

/** Only server-known keys, and only when they carry a value. */
export function toQuery(f: SearchFilters): string {
  const q = new URLSearchParams();
  if (f.skills?.length) q.set("skills", f.skills.join(","));
  if (f.q?.trim()) q.set("q", f.q.trim());
  if (f.minSkillScore) q.set("min_skill_score", String(f.minSkillScore));
  if (f.minOverallScore) q.set("min_overall_score", String(f.minOverallScore));
  if (f.minGeneralistScore) q.set("min_generalist_score", String(f.minGeneralistScore));
  if (f.minOfficeYoe) q.set("min_office_yoe", String(f.minOfficeYoe));
  if (f.minOssYoe) q.set("min_oss_yoe", String(f.minOssYoe));
  if (f.countries?.length) q.set("countries", f.countries.join(","));
  if (f.openTo?.length) q.set("open_to", f.openTo.join(","));
  if (f.forRole) q.set("for_role", f.forRole);
  if (f.includeInactive) q.set("include_inactive", "true");
  if (f.page && f.page > 1) q.set("page", String(f.page));
  if (f.perPage) q.set("per_page", String(f.perPage));
  const s = q.toString();
  return s ? `?${s}` : "";
}

export function isEmpty(f: SearchFilters): boolean {
  return toQuery({ ...f, page: undefined, perPage: undefined }) === "";
}

export function pageCount(total: number, perPage: number): number {
  if (perPage <= 0) return 1;
  return Math.max(1, Math.ceil(total / perPage));
}

export function useSearch(filters: SearchFilters, enabled: boolean) {
  const client = useClient();
  const query = toQuery(filters);
  return useAsync<SearchResponse>(
    (signal) => client.get<SearchResponse>(endpoints.discovery.search(query), signal),
    [client, query],
    enabled,
  );
}

export function useScorecard(userId: string | undefined) {
  const client = useClient();
  return useAsync<Scorecard>(
    (signal) => client.get<Scorecard>(endpoints.discovery.scorecard(userId ?? ""), signal),
    [client, userId],
    Boolean(userId),
  );
}

export function useLeaderboard(kind: string, skill?: string) {
  const client = useClient();
  // A skill board without a skill is not a request worth making: the API
  // answers 422 required_for_kind_skill, correctly. Asking anyway turns
  // "you have not chosen yet" into "that did not work", which is what the
  // contributor sees the moment they remove the skill chip.
  const ready = kind !== "skill" || Boolean(skill);
  return useAsync<LeaderboardResponse>(
    (signal) =>
      client.get<LeaderboardResponse>(endpoints.discovery.leaderboard(kind, skill), signal),
    [client, kind, skill],
    ready,
  );
}

export function useShortlists() {
  const client = useClient();
  return useAsync<{ shortlists: ShortlistSummary[] }>(
    (signal) => client.get<{ shortlists: ShortlistSummary[] }>(endpoints.shortlists.list(), signal),
    [client],
  );
}

export function useShortlist(id: string | undefined) {
  const client = useClient();
  return useAsync<Shortlist>(
    (signal) => client.get<Shortlist>(endpoints.shortlists.one(id ?? ""), signal),
    [client, id],
    Boolean(id),
  );
}

export function useCreateShortlist() {
  const client = useClient();
  return useAction(
    (roleId: string, name: string, tentativeResultDate: string, description: string) =>
      client.post<Shortlist>(endpoints.shortlists.create(), {
        role_id: roleId,
        name,
        description,
        tentative_result_date: tentativeResultDate,
      }),
  );
}

export function useStageEntry(shortlistId: string) {
  const client = useClient();
  return useAction((userId: string, note = "") =>
    client.post<unknown>(endpoints.shortlists.entries(shortlistId), { user_id: userId, note }),
  );
}

export function useRemoveEntry(shortlistId: string) {
  const client = useClient();
  return useAction((userId: string) =>
    client.delete<unknown>(endpoints.shortlists.removeEntry(shortlistId, userId)),
  );
}

/** Irreversible. Nothing the platform offers takes a confirmation back. */
export function useConfirmShortlist(shortlistId: string) {
  const client = useClient();
  return useAction(() => client.post<ConfirmResult>(endpoints.shortlists.confirm(shortlistId)));
}

export function useCloseShortlist(shortlistId: string) {
  const client = useClient();
  return useAction(() => client.post<unknown>(endpoints.shortlists.close(shortlistId)));
}

export function useSavedSearches() {
  const client = useClient();
  return useAsync<{ saved_searches: SavedSearch[] }>(
    (signal) =>
      client.get<{ saved_searches: SavedSearch[] }>(endpoints.discovery.savedSearches(), signal),
    [client],
  );
}

export function useSaveSearch() {
  const client = useClient();
  return useAction((name: string, filters: SearchFilters) =>
    client.post<SavedSearch>(endpoints.discovery.savedSearches(), {
      name,
      filters: rawFilters(filters),
    }),
  );
}

export function useDeleteSavedSearch() {
  const client = useClient();
  return useAction((id: string) =>
    client.delete<unknown>(endpoints.discovery.deleteSavedSearch(id)),
  );
}

/** Replayed by the server against the stored set, as the caller. */
export function useSavedSearchResults(id: string | undefined) {
  const client = useClient();
  return useAsync<SearchResponse>(
    (signal) =>
      client.get<SearchResponse>(endpoints.discovery.savedSearchResults(id ?? ""), signal),
    [client, id],
    Boolean(id),
  );
}

/** A saved search stores the question verbatim, in the server's own key names. */
function rawFilters(f: SearchFilters): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  if (f.skills?.length) out.skills = f.skills.join(",");
  if (f.q?.trim()) out.q = f.q.trim();
  if (f.minSkillScore) out.min_skill_score = f.minSkillScore;
  if (f.minOverallScore) out.min_overall_score = f.minOverallScore;
  if (f.minGeneralistScore) out.min_generalist_score = f.minGeneralistScore;
  if (f.minOfficeYoe) out.min_office_yoe = f.minOfficeYoe;
  if (f.minOssYoe) out.min_oss_yoe = f.minOssYoe;
  if (f.countries?.length) out.countries = f.countries.join(",");
  if (f.openTo?.length) out.open_to = f.openTo.join(",");
  if (f.forRole) out.for_role = f.forRole;
  if (f.includeInactive) out.include_inactive = true;
  return out;
}

export function fromSaved(filters: Record<string, unknown>): SearchFilters {
  const str = (k: string) => (typeof filters[k] === "string" ? (filters[k] as string) : undefined);
  const num = (k: string) => (typeof filters[k] === "number" ? (filters[k] as number) : undefined);
  const csv = (k: string) => {
    const v = str(k);
    return v ? v.split(",").filter(Boolean) : undefined;
  };
  return {
    skills: csv("skills"),
    q: str("q"),
    minSkillScore: num("min_skill_score"),
    minOverallScore: num("min_overall_score"),
    minGeneralistScore: num("min_generalist_score"),
    minOfficeYoe: num("min_office_yoe"),
    minOssYoe: num("min_oss_yoe"),
    countries: csv("countries"),
    openTo: csv("open_to"),
    forRole: str("for_role"),
    includeInactive: filters.include_inactive === true,
  };
}

/** A one-line summary of what was asked, for a results header or a saved row. */
export function describe(f: SearchFilters): string {
  const bits: string[] = [];
  if (f.q?.trim()) bits.push(`“${f.q.trim()}”`);
  if (f.skills?.length) bits.push(f.skills.join(" + "));
  if (f.minSkillScore) bits.push(`skill ≥ ${f.minSkillScore}`);
  if (f.minOverallScore) bits.push(`overall ≥ ${f.minOverallScore}`);
  if (f.minGeneralistScore) bits.push(`generalist ≥ ${f.minGeneralistScore}`);
  if (f.minOfficeYoe) bits.push(`${f.minOfficeYoe}y in a job`);
  if (f.minOssYoe) bits.push(`${f.minOssYoe}y open source`);
  if (f.countries?.length) bits.push(f.countries.join(" / "));
  if (f.openTo?.length) bits.push(f.openTo.join(" / "));
  if (f.forRole) bits.push("not yet on this round");
  if (f.includeInactive) bits.push("including quiet");
  return bits.length ? bits.join(" · ") : "everyone";
}

/* --- roles (ADR-0019) ------------------------------------------------------ */

/**
 * The organisation id, recovered the same way Team.tsx recovers it.
 *
 * `/me` does not carry one — that shape is pinned by the approved fixtures — so
 * a session rehydrated from storage written by an older build has a name and no
 * id. /me/verification does carry it, which makes the fallback one request
 * rather than a sign-out.
 */
export function useOrgID(): { id: string | undefined; loading: boolean } {
  const { client, principal } = useSession();
  const stored = principal?.organization?.id;
  const recovered = useAsync<VerificationStatus>(
    (signal) => client.get<VerificationStatus>(endpoints.me.verification(), signal),
    [client],
    !stored,
  );
  return {
    id: stored ?? recovered.data?.organization?.id,
    loading: !stored && recovered.loading,
  };
}

export function useRoles(orgId: string | undefined, status?: string) {
  const client = useClient();
  return useAsync<{ roles: Role[] }>(
    (signal) => client.get<{ roles: Role[] }>(endpoints.roles.list(orgId ?? "", status), signal),
    [client, orgId, status],
    Boolean(orgId),
  );
}

export function useRole(orgId: string | undefined, id: string | undefined) {
  const client = useClient();
  return useAsync<Role>(
    (signal) => client.get<Role>(endpoints.roles.one(orgId ?? "", id ?? ""), signal),
    [client, orgId, id],
    Boolean(orgId && id),
  );
}

/** Creates a DRAFT. Opening it is a separate act, and may need an owner. */
export function useCreateRole(orgId: string) {
  const client = useClient();
  return useAction((draft: RoleDraft) =>
    client.post<Role>(endpoints.roles.create(orgId), draft as unknown as Record<string, unknown>),
  );
}

/** Drafts only. An open role is immutable — use useReviseRole. */
export function useUpdateRole(orgId: string, id: string) {
  const client = useClient();
  return useAction((draft: RoleDraft) =>
    client.patch<Role>(
      endpoints.roles.update(orgId, id),
      draft as unknown as Record<string, unknown>,
    ),
  );
}

/**
 * Publishes a SUCCESSOR to an open role and closes the original.
 *
 * What comes back is a new role with a new id, and the caller has to follow it:
 * the role they were editing is now closed, and continuing to show it would be
 * showing a withdrawn opening.
 */
export function useReviseRole(orgId: string, id: string) {
  const client = useClient();
  return useAction((draft: RoleDraft) =>
    client.post<Role>(
      endpoints.roles.revise(orgId, id),
      draft as unknown as Record<string, unknown>,
    ),
  );
}

export function useOpenRole(orgId: string) {
  const client = useClient();
  return useAction((id: string) => client.post<Role>(endpoints.roles.open(orgId, id)));
}

/**
 * Closes a role, or records that somebody asked to.
 *
 * Both answer 200 with the role. Under draft_and_approve the caller staged a
 * request and the role is still open, which the body says through `status` and
 * `close_requested_at` — a client must read those rather than assume.
 */
export function useCloseRole(orgId: string) {
  const client = useClient();
  return useAction((id: string, reason: CloseReason, note: string, hired: string[]) =>
    client.post<Role>(endpoints.roles.close(orgId, id), { reason, note, hired }),
  );
}

export function useOrgSettings(orgId: string | undefined) {
  const client = useClient();
  return useAsync<OrgSettings>(
    (signal) => client.get<OrgSettings>(endpoints.roles.settings(orgId ?? ""), signal),
    [client, orgId],
    Boolean(orgId),
  );
}

/** Owners only, under every combination — otherwise a member could grant
 *  themselves the authority the setting exists to withhold. */
export function useSaveOrgSettings(orgId: string) {
  const client = useClient();
  return useAction((settings: OrgSettings) =>
    client.put<OrgSettings>(
      endpoints.roles.settings(orgId),
      settings as unknown as Record<string, unknown>,
    ),
  );
}

/* --- public openings (ADR-0020) -------------------------------------------- */

/** Everybody already on a round for this role. */
export function useRoleCandidates(orgId: string | undefined, roleId: string | undefined) {
  const client = useClient();
  return useAsync<{ candidates: RoleCandidate[] }>(
    (signal) =>
      client.get<{ candidates: RoleCandidate[] }>(
        endpoints.roles.candidates(orgId ?? "", roleId ?? ""),
        signal,
      ),
    [client, orgId, roleId],
    Boolean(orgId && roleId),
  );
}

export function useOpening(orgId: string | undefined, roleId: string | undefined) {
  const client = useClient();
  return useAsync<Opening>(
    (signal) => client.get<Opening>(endpoints.roles.opening(orgId ?? "", roleId ?? ""), signal),
    [client, orgId, roleId],
    Boolean(orgId && roleId),
  );
}

/** Writes the bar. Does NOT publish — that is a separate, owner-level act. */
export function useSaveOpening(orgId: string, roleId: string) {
  const client = useClient();
  return useAction((bar: OpeningBar) =>
    client.put<Opening>(
      endpoints.roles.opening(orgId, roleId),
      bar as unknown as Record<string, unknown>,
    ),
  );
}

export function usePublishOpening(orgId: string, roleId: string) {
  const client = useClient();
  return useAction(() => client.post<Opening>(endpoints.roles.publishOpening(orgId, roleId)));
}

export function useWithdrawOpening(orgId: string, roleId: string) {
  const client = useClient();
  return useAction(() => client.post<Opening>(endpoints.roles.withdrawOpening(orgId, roleId)));
}
