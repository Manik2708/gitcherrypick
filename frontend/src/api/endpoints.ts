// Every path the client knows, in one file.
//
// CLAUDE.md requires this: endpoint strings live in a single place and the base
// URL comes from the environment, so a deployment retargets without a grep
// through components.

const base = (import.meta.env.VITE_API_BASE_URL as string | undefined) ?? "http://127.0.0.1:8080";

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
    shareLink: () => "/me/share-link",
    revokeShareLink: (id: string) => `/me/share-link/${encodeURIComponent(id)}`,
    contactRequests: () => "/me/contact-requests",
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
    decideVerification: (id: string) =>
      `/admin/verifications/${encodeURIComponent(id)}/decide`,
    skillRequests: () => "/admin/skill-requests",
    decideSkillRequest: (id: string) =>
      `/admin/skill-requests/${encodeURIComponent(id)}/decide`,
    reevaluations: () => "/admin/reevaluations?status=pending",
    decideReevaluation: (id: string) =>
      `/admin/reevaluations/${encodeURIComponent(id)}/decide`,
    sweep: () => "/admin/evaluations/sweep",
  },
  public: {
    scorecard: (token: string) => `/scorecard/${encodeURIComponent(token)}`,
  },
} as const;
