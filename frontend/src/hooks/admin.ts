// The queues only an administrator drains.

import { endpoints } from "../api/endpoints";
import type {
  OnboardingQueueResponse,
  ReevaluationRequest,
  SkillRequest,
  SweepResult,
  VerificationRequest,
} from "../contract";
import { useClient } from "./session";
import { useAction, useAsync } from "./useAsync";

export function useVerificationQueue() {
  const client = useClient();
  return useAsync<{ requests: VerificationRequest[] }>(
    (signal) =>
      client.get<{ requests: VerificationRequest[] }>(endpoints.admin.verifications(), signal),
    [client],
  );
}

export function useDecideVerification() {
  const client = useClient();
  return useAction((id: string, approve: boolean, reason: string, paymentVerified: boolean) =>
    client.post<unknown>(endpoints.admin.decideVerification(id), {
      decision: approve ? "approved" : "rejected",
      reason,
      payment_verified: paymentVerified,
    }),
  );
}

/**
 * Companies awaiting a decision (ADR-0017).
 *
 * Its own queue rather than a third subject on the verification one: approving
 * here CREATES a company and can fail on a name already taken, where deciding a
 * verification stamps a column. Same screen, different operation.
 */
export function useOnboardingQueue() {
  const client = useClient();
  return useAsync<OnboardingQueueResponse>(
    (signal) => client.get<OnboardingQueueResponse>(endpoints.admin.onboarding(), signal),
    [client],
  );
}

export function useDecideOnboarding() {
  const client = useClient();
  return useAction((id: string, approve: boolean, reason: string) =>
    client.post<unknown>(endpoints.admin.decideOnboarding(id), {
      decision: approve ? "approved" : "rejected",
      reason,
    }),
  );
}

export function useSkillRequestQueue() {
  const client = useClient();
  return useAsync<{ requests: SkillRequest[] }>(
    (signal) => client.get<{ requests: SkillRequest[] }>(endpoints.admin.skillRequests(), signal),
    [client],
  );
}

export function useDecideSkillRequest() {
  const client = useClient();
  return useAction(
    (
      id: string,
      approve: boolean,
      reason: string,
      skill: { slug: string; name: string; category: string } | null,
    ) =>
      client.post<unknown>(endpoints.admin.decideSkillRequest(id), {
        decision: approve ? "approved" : "rejected",
        reason,
        ...(skill ? { skill } : {}),
      }),
  );
}

export function useReevaluationQueue() {
  const client = useClient();
  return useAsync<{ requests: ReevaluationRequest[] }>(
    (signal) =>
      client.get<{ requests: ReevaluationRequest[] }>(endpoints.admin.reevaluations(), signal),
    [client],
  );
}

export function useDecideReevaluation() {
  const client = useClient();
  return useAction((id: string, accept: boolean, note: string) =>
    client.post<unknown>(endpoints.admin.decideReevaluation(id), {
      decision: accept ? "accepted" : "rejected",
      note,
    }),
  );
}

/** Enqueue-only: the whole scored population leaves search until it drains. */
export function useSweep() {
  const client = useClient();
  return useAction((toRubricVersion: string, reason: string) =>
    client.post<SweepResult>(endpoints.admin.sweep(), {
      to_rubric_version: toRubricVersion,
      reason,
    }),
  );
}
