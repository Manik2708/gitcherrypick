// The hirer console's reads and writes.
//
// Every filter name here is a `saved_searches.filters` key, and an unknown one
// is a 422 rather than a silent drop (ADR-0008 §1) — so the query builder emits
// only names the server knows.

import { endpoints } from "../api/endpoints";
import type {
  ConfirmResult,
  LeaderboardResponse,
  SavedSearch,
  Scorecard,
  SearchResponse,
  Shortlist,
  ShortlistSummary,
} from "../contract";
import { useClient } from "./session";
import { useAction, useAsync } from "./useAsync";

export interface SearchFilters {
  skills?: string[];
  q?: string;
  availability?: string[];
  minSkillScore?: number;
  minOverallScore?: number;
  minGeneralistScore?: number;
  evidenceWithinMonths?: number;
  includeInactive?: boolean;
  page?: number;
  perPage?: number;
}

/** Only server-known keys, and only when they carry a value. */
export function toQuery(f: SearchFilters): string {
  const q = new URLSearchParams();
  if (f.skills?.length) q.set("skills", f.skills.join(","));
  if (f.q?.trim()) q.set("q", f.q.trim());
  if (f.availability?.length) q.set("availability", f.availability.join(","));
  if (f.minSkillScore) q.set("min_skill_score", String(f.minSkillScore));
  if (f.minOverallScore) q.set("min_overall_score", String(f.minOverallScore));
  if (f.minGeneralistScore) q.set("min_generalist_score", String(f.minGeneralistScore));
  if (f.evidenceWithinMonths) q.set("evidence_within_months", String(f.evidenceWithinMonths));
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
    (signal) =>
      client.get<{ shortlists: ShortlistSummary[] }>(endpoints.shortlists.list(), signal),
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
  return useAction((name: string, tentativeResultDate: string, description: string) =>
    client.post<Shortlist>(endpoints.shortlists.create(), {
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
  return useAction(() =>
    client.post<ConfirmResult>(endpoints.shortlists.confirm(shortlistId)),
  );
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
  if (f.availability?.length) out.availability = f.availability.join(",");
  if (f.minSkillScore) out.min_skill_score = f.minSkillScore;
  if (f.minOverallScore) out.min_overall_score = f.minOverallScore;
  if (f.minGeneralistScore) out.min_generalist_score = f.minGeneralistScore;
  if (f.evidenceWithinMonths) out.evidence_within_months = f.evidenceWithinMonths;
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
    availability: csv("availability"),
    minSkillScore: num("min_skill_score"),
    minOverallScore: num("min_overall_score"),
    minGeneralistScore: num("min_generalist_score"),
    evidenceWithinMonths: num("evidence_within_months"),
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
  if (f.evidenceWithinMonths) bits.push(`evidence within ${f.evidenceWithinMonths}mo`);
  if (f.availability?.length) bits.push(f.availability.join(" / "));
  if (f.includeInactive) bits.push("including quiet");
  return bits.length ? bits.join(" · ") : "everyone";
}
