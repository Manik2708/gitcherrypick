// A contributor's own reads and writes.

import { endpoints } from "../api/endpoints";
import type {
  CompensationExpectation,
  ContributorProfile,
  WorkPreferences,
  Claim,
  ClaimSummary,
  ContactRequest,
  Demotion,
  MyRank,
  OpeningsResponse,
  MySkills,
  ReevaluationStatus,
  ShareLink,
  SkillMatch,
} from "../contract";
import { useClient } from "./session";
import { useAction, useAsync } from "./useAsync";

export interface PRInput {
  position: number;
  url: string;
}

export function useMySkills() {
  const client = useClient();
  return useAsync<MySkills>(
    (signal) => client.get<MySkills>(endpoints.me.skills(), signal),
    [client],
  );
}

export function useMyRank() {
  const client = useClient();
  return useAsync<MyRank>((signal) => client.get<MyRank>(endpoints.me.rank(), signal), [client]);
}

export function useMyClaims() {
  const client = useClient();
  return useAsync<{ claims: ClaimSummary[] }>(
    (signal) => client.get<{ claims: ClaimSummary[] }>(endpoints.claims.list(), signal),
    [client],
  );
}

export function useClaim(id: string | undefined) {
  const client = useClient();
  return useAsync<Claim>(
    (signal) => client.get<Claim>(endpoints.claims.one(id ?? ""), signal),
    [client, id],
    Boolean(id),
  );
}

export function useCreateClaim() {
  const client = useClient();
  return useAction(() => client.post<Claim>(endpoints.claims.create(), {}));
}

export function useSetEvidence(claimId: string) {
  const client = useClient();
  return useAction((prs: PRInput[]) =>
    client.post<unknown>(endpoints.claims.prEvidence(claimId), { prs }),
  );
}

export function useSetSkills(claimId: string) {
  const client = useClient();
  return useAction((skills: { slug: string; nominated_primary: boolean }[]) =>
    client.post<unknown>(endpoints.claims.skills(claimId), { skills }),
  );
}

export function useSubmitClaim(claimId: string) {
  const client = useClient();
  return useAction(() => client.post<unknown>(endpoints.claims.submit(claimId)));
}

export function useWithdrawPreview(claimId: string | undefined) {
  const client = useClient();
  return useAsync<{ demotions: Demotion[] }>(
    (signal) =>
      client.get<{ demotions: Demotion[] }>(
        endpoints.claims.withdrawPreview(claimId ?? ""),
        signal,
      ),
    [client, claimId],
    Boolean(claimId),
  );
}

export function useWithdrawClaim(claimId: string) {
  const client = useClient();
  return useAction((confirmDemotion: boolean) =>
    client.post<unknown>(endpoints.claims.withdraw(claimId), {
      confirm_demotion: confirmDemotion,
    }),
  );
}

export function useDisputeJudgement(claimId: string) {
  const client = useClient();
  return useAction((reason: string) =>
    client.post<unknown>(endpoints.claims.reevaluation(claimId), { reason }),
  );
}

export function useDecideSuggestion(claimId: string) {
  const client = useClient();
  return useAction((skillId: string, accept: boolean) =>
    client.post<unknown>(endpoints.claims.decideSuggestion(claimId, skillId, accept)),
  );
}

export function useSkillSearch(query: string) {
  const client = useClient();
  return useAsync<{ results: SkillMatch[] }>(
    (signal) => client.get<{ results: SkillMatch[] }>(endpoints.skills.search(query), signal),
    [client, query],
    query.trim().length > 0,
  );
}

export function useRequestSkill() {
  const client = useClient();
  return useAction((proposedName: string, rationale: string) =>
    client.post<unknown>(endpoints.skills.request(), {
      proposed_name: proposedName,
      rationale,
    }),
  );
}

export function useReevaluationStatus() {
  const client = useClient();
  return useAsync<ReevaluationStatus>(
    (signal) => client.get<ReevaluationStatus>(endpoints.me.reevaluationStatus(), signal),
    [client],
  );
}

export function useSetAvailability() {
  const client = useClient();
  return useAction((status: string) =>
    client.put<unknown>(endpoints.me.availability(), { status }),
  );
}

export function useMyContactRequests() {
  const client = useClient();
  return useAsync<{ requests: ContactRequest[] }>(
    (signal) => client.get<{ requests: ContactRequest[] }>(endpoints.me.contactRequests(), signal),
    [client],
  );
}

export function useRespondToContact() {
  const client = useClient();
  return useAction((id: string, accept: boolean) =>
    client.post<unknown>(endpoints.me.respondContact(id, accept)),
  );
}

export function useMintShareLink() {
  const client = useClient();
  return useAction(() => client.post<ShareLink>(endpoints.me.shareLink()));
}

export function useRevokeShareLink() {
  const client = useClient();
  return useAction((id: string) => client.delete<unknown>(endpoints.me.revokeShareLink(id)));
}

/* --- the profile (ADR-0018) ----------------------------------------------- */

export function useMyProfile() {
  const client = useClient();
  return useAsync<ContributorProfile>(
    (signal) => client.get<ContributorProfile>(endpoints.me.profile(), signal),
    [client],
  );
}

export function useSaveProfile() {
  const client = useClient();
  return useAction((body: WorkPreferences) =>
    client.put<ContributorProfile>(endpoints.me.profile(), body),
  );
}

export function useMyCompensation() {
  const client = useClient();
  return useAsync<CompensationExpectation>(
    (signal) => client.get<CompensationExpectation>(endpoints.me.compensation(), signal),
    [client],
  );
}

export function useSaveCompensation() {
  const client = useClient();
  return useAction((body: CompensationExpectation) =>
    client.put<CompensationExpectation>(endpoints.me.compensation(), body),
  );
}

/**
 * The published roles open to this contributor (ADR-0020).
 *
 * Only the ones they clear, plus a bare count of the ones they do not. The
 * count exists because an empty list on its own lies about why it is empty —
 * nobody hiring, or nothing they qualify for — and it names no shortfall and
 * exposes no company's bar.
 *
 * Reading this sends nothing to anybody. There is no apply call to pair it
 * with, deliberately.
 */
export function useOpenings() {
  const client = useClient();
  return useAsync<OpeningsResponse>(
    (signal) => client.get<OpeningsResponse>(endpoints.openings.list(), signal),
    [client],
  );
}
