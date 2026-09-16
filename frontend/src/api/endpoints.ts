// Every path the client knows, in one file.
//
// CLAUDE.md requires this: endpoint strings live in a single place and the base
// URL comes from the environment, so a deployment retargets without a grep
// through components.

// Relative by default, so requests go through the dev proxy and stay
// same-origin — the API sends no CORS headers, and the OAuth state cookie is
// SameSite=Lax. Set VITE_API_BASE_URL only to point at an API on its own
// origin, which then needs CORS on the server.
const base = (import.meta.env.VITE_API_BASE_URL as string | undefined) ?? "/api";

export const API_BASE_URL = base.replace(/\/$/, "");

export const endpoints = {
  auth: {
    githubStart: () => "/auth/github/start",
    githubCallback: (code: string, state: string) =>
      `/auth/github/callback?code=${encodeURIComponent(code)}&state=${encodeURIComponent(state)}`,
    hirerGithubStart: () => "/auth/hirer/github/start",
    hirerGithubCallback: (code: string, state: string) =>
      `/auth/hirer/github/callback?code=${encodeURIComponent(code)}&state=${encodeURIComponent(state)}`,
    hirerLogin: () => "/auth/hirer/login",

    // Redeeming a roster entry: the second way a hirer account comes to exist
    // (ADR-0016). Unauthenticated — the caller has no account until the
    // second of these succeeds.
    redeemStart: () => "/auth/hirer/redeem/start",
    redeemComplete: () => "/auth/hirer/redeem/complete",
    resendVerification: () => "/auth/hirer/verify/resend",
    adminLogin: () => "/auth/admin/login",
    hirerRegister: () => "/auth/hirer/register",
    refresh: () => "/auth/refresh",
    logout: () => "/auth/logout",
  },
  me: {
    root: () => "/me",
    skills: () => "/me/skills",
    rank: () => "/me/rank",
    verification: () => "/me/verification",
    reevaluationStatus: () => "/me/reevaluation-status",
    availability: () => "/me/availability",

    // The contributor profile (ADR-0018). Compensation is a separate pair so a
    // client updating what work it will take never sends a salary.
    profile: () => "/me/profile",
    compensation: () => "/me/compensation",
    shareLink: () => "/me/share-link",
    revokeShareLink: (id: string) => `/me/share-link/${encodeURIComponent(id)}`,
    contactRequests: () => "/me/contact-requests",

    // What is open to this person (ADR-0019). Under /me because the filter
    // reads their profile — including the pay they expect, which is why the
    // result can only be theirs.
    roles: () => "/me/roles",
    respondContact: (id: string, accept: boolean) =>
      `/me/contact-requests/${encodeURIComponent(id)}/${accept ? "accept" : "decline"}`,
  },
  claims: {
    list: () => "/claims",
    create: () => "/claims",
    one: (id: string) => `/claims/${encodeURIComponent(id)}`,
    prEvidence: (id: string) => `/claims/${encodeURIComponent(id)}/evidence/prs`,
    projectEvidence: (id: string) => `/claims/${encodeURIComponent(id)}/evidence/projects`,
    skills: (id: string) => `/claims/${encodeURIComponent(id)}/skills`,
    submit: (id: string) => `/claims/${encodeURIComponent(id)}/submit`,
    withdrawPreview: (id: string) => `/claims/${encodeURIComponent(id)}/withdraw-preview`,
    withdraw: (id: string) => `/claims/${encodeURIComponent(id)}/withdraw`,
    reevaluation: (id: string) => `/claims/${encodeURIComponent(id)}/reevaluation`,
    decideSuggestion: (id: string, skillId: string, accept: boolean) =>
      `/claims/${encodeURIComponent(id)}/suggestions/${encodeURIComponent(skillId)}/${
        accept ? "accept" : "dismiss"
      }`,
  },
  discovery: {
    search: (query: string) => `/search${query}`,
    leaderboard: (kind: string, skill?: string) =>
      `/leaderboard?kind=${encodeURIComponent(kind)}${
        skill ? `&skill=${encodeURIComponent(skill)}` : ""
      }`,
    scorecard: (userId: string) => `/contributors/${encodeURIComponent(userId)}/scorecard`,
    savedSearches: () => "/saved-searches",
    savedSearchResults: (id: string) => `/saved-searches/${encodeURIComponent(id)}/results`,
    deleteSavedSearch: (id: string) => `/saved-searches/${encodeURIComponent(id)}`,
  },
  shortlists: {
    list: () => "/shortlists",
    create: () => "/shortlists",
    one: (id: string) => `/shortlists/${encodeURIComponent(id)}`,
    close: (id: string) => `/shortlists/${encodeURIComponent(id)}/close`,
    confirm: (id: string) => `/shortlists/${encodeURIComponent(id)}/confirm`,
    entries: (id: string) => `/shortlists/${encodeURIComponent(id)}/entries`,
    removeEntry: (id: string, userId: string) =>
      `/shortlists/${encodeURIComponent(id)}/entries/${encodeURIComponent(userId)}`,
    contactRequests: (id: string) => `/shortlists/${encodeURIComponent(id)}/contact-requests`,
  },
  skills: {
    search: (q: string) => `/skills?q=${encodeURIComponent(q)}`,
    request: () => "/skill-requests",
  },
  admin: {
    verifications: () => "/admin/verifications?status=pending",
    decideVerification: (id: string) => `/admin/verifications/${encodeURIComponent(id)}/decide`,
    skillRequests: () => "/admin/skill-requests",
    decideSkillRequest: (id: string) => `/admin/skill-requests/${encodeURIComponent(id)}/decide`,
    reevaluations: () => "/admin/reevaluations?status=pending",
    decideReevaluation: (id: string) => `/admin/reevaluations/${encodeURIComponent(id)}/decide`,
    sweep: () => "/admin/evaluations/sweep",
    onboarding: () => "/admin/onboarding",
    decideOnboarding: (id: string) => `/admin/onboarding/${encodeURIComponent(id)}/decide`,
  },
  orgs: {
    roster: (orgId: string) => `/orgs/${encodeURIComponent(orgId)}/roster`,
    removeRosterEntry: (orgId: string, entryId: string) =>
      `/orgs/${encodeURIComponent(orgId)}/roster/${encodeURIComponent(entryId)}`,
    seats: (orgId: string) => `/orgs/${encodeURIComponent(orgId)}/seats`,
  },

  // Openings (ADR-0019). Mounted at /org-roles rather than /orgs because the
  // server allows one handler per prefix and the roster already holds that one;
  // the paths beneath carry the same {orgId} shape.
  roles: {
    list: (orgId: string, status?: string) =>
      `/org-roles/${encodeURIComponent(orgId)}/roles${
        status ? `?status=${encodeURIComponent(status)}` : ""
      }`,
    create: (orgId: string) => `/org-roles/${encodeURIComponent(orgId)}/roles`,
    one: (orgId: string, id: string) =>
      `/org-roles/${encodeURIComponent(orgId)}/roles/${encodeURIComponent(id)}`,
    update: (orgId: string, id: string) =>
      `/org-roles/${encodeURIComponent(orgId)}/roles/${encodeURIComponent(id)}`,

    // A change to an OPEN role writes a successor rather than editing it, so
    // everybody already contacted keeps reading the role they were shown.
    revise: (orgId: string, id: string) =>
      `/org-roles/${encodeURIComponent(orgId)}/roles/${encodeURIComponent(id)}/revise`,
    open: (orgId: string, id: string) =>
      `/org-roles/${encodeURIComponent(orgId)}/roles/${encodeURIComponent(id)}/open`,
    close: (orgId: string, id: string) =>
      `/org-roles/${encodeURIComponent(orgId)}/roles/${encodeURIComponent(id)}/close`,

    settings: (orgId: string) => `/org-roles/${encodeURIComponent(orgId)}/settings`,
  },
  public: {
    scorecard: (token: string) => `/scorecard/${encodeURIComponent(token)}`,

    // The country picker. Public: the organisation onboarding form needs it
    // and has no account behind it either.
    countries: () => "/places/countries",

    // The picker a redeemer finds their employer in. Verified organizations
    // only, name and slug only (ADR-0016 §3a).
    organizations: () => "/organizations",

    // Onboarding a company (ADR-0017). All three are public: the person
    // describing a company has no account, and onboarding is what eventually
    // produces one.
    onboardOrganization: () => "/organizations",
    verifyOrganization: () => "/organizations/verify",
    reviseOrganization: () => "/organizations/revise",
  },
} as const;
