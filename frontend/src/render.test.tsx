// Does it render at all?
//
// A build succeeding says the types line up, not that a component runs. These
// mount the real screens against a stubbed transport and assert something a
// person would look for — the cheapest way to catch a crash typechecking
// cannot see.

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { App } from "./App";
import { SessionProvider } from "./hooks/session";

function mount(path: string) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <SessionProvider>
        <App />
      </SessionProvider>
    </MemoryRouter>,
  );
}

function asHirer() {
  sessionStorage.setItem("gcp.access", "token");
  sessionStorage.setItem("gcp.refresh", "refresh");
  sessionStorage.setItem(
    "gcp.principal",
    JSON.stringify({
      id: "h1",
      display_name: "Maya Renner",
      principal_type: "hirer",
      organization: { id: "o1", name: "Northfield Labs", verified: true },
    }),
  );
}

// A role, as the roles list returns one. Shared so a test naming three of them
// does not carry three copies of twenty-five fields.
const ROLE_STUB = {
  organization_id: "o1",
  description: "",
  engagement: "full_time",
  locations: [{ mode: "remote", country: null, address: null }],
  currency: "",
  yearly_ctc: null,
  yearly_base: null,
  hourly_rate: null,
  expected_hours: null,
  min_office_yoe: null,
  min_oss_yoe: null,
  requires_online_test: null,
  max_interview_rounds: null,
  avg_days_to_offer: null,
  questions: [],
  opened_at: null,
  close_requested_at: null,
  closed_at: null,
  close_reason: null,
  hires: [],
  advertised: false,
  supersedes: null,
  created_at: "2026-09-01T00:00:00Z",
  updated_at: "2026-09-01T00:00:00Z",
};

describe("the app renders", () => {
  beforeEach(() => {
    sessionStorage.clear();
    vi.restoreAllMocks();
  });

  it("shows the sign-in page to a visitor with no session", () => {
    mount("/signin");
    expect(screen.getAllByText("Sign in with GitHub").length).toBeGreaterThan(0);
  });

  // The root used to redirect a visitor into a login form, which asked for a
  // GitHub identity before saying what it would be used for.
  it("explains itself at the root instead of demanding a sign-in", () => {
    mount("/");
    expect(screen.getByText("Prove what you can do with merged code.")).toBeTruthy();
    expect(screen.getByText("How a claim becomes a standing")).toBeTruthy();
    // The way in is offered, not forced.
    expect(screen.getAllByText("Sign in with GitHub").length).toBeGreaterThan(0);
  });

  // The bug this pins: a bare fragment link resolves against the current path,
  // so "#how" from /signin went to /signin#how — a route with no such section,
  // which renders as a dead page.
  it("points its section links at the landing route, not the current one", () => {
    mount("/signin");
    const how = screen.getByText("How it works").closest("a");
    expect(how?.getAttribute("href")).toBe("/#how");
    expect(screen.getByText("For hirers").closest("a")?.getAttribute("href")).toBe("/#hirers");
    expect(screen.getByText("Fairness").closest("a")?.getAttribute("href")).toBe("/#fair");
  });

  it("renders the section a hash names when arriving at it directly", () => {
    mount("/#hirers");
    // The route is the landing page, and the named section is present to scroll to.
    expect(screen.getByText("For people hiring")).toBeTruthy();
    expect(document.getElementById("hirers")).toBeTruthy();
  });

  // The deck hands over from one walkthrough to the other on its own. Taking
  // hold of it must stop that, or it slides away under the visitor mid-look.
  it("lets a visitor drive the walkthrough deck, and stops auto-advancing once they do", () => {
    mount("/");
    const track = document.querySelector(".deck__track") as HTMLElement;
    expect(track.style.transform).toBe("translateX(calc(0% + 0px))");

    // Dragging is a pointer gesture, so the dots and the arrow keys are the
    // only ways across for a keyboard visitor. Both have to work.
    fireEvent.click(screen.getByLabelText("Show the hirer walkthrough"));
    expect(track.style.transform).toBe("translateX(calc(-100% + 0px))");

    fireEvent.keyDown(screen.getByLabelText("Walkthroughs"), { key: "ArrowLeft" });
    expect(track.style.transform).toBe("translateX(calc(0% + 0px))");

    // Nothing that looks like a nav button is left in the deck.
    expect(screen.queryByLabelText("Next walkthrough")).toBeNull();
  });

  // A hirer signs in with Google, so its return needs a route of its own.
  it("has a route for the Google return, not just the GitHub one", () => {
    mount("/auth/google/callback?code=e2e-hank&state=s");
    // The callback page is what should claim this path — it sits on "Loading
    // your session…" while the exchange runs. The 404 prints the unmatched
    // path in a <code>, so its absence is the assertion that matters.
    expect(screen.getByText("Loading your session…")).toBeTruthy();
    expect(screen.queryByText("/auth/google/callback")).toBeNull();
  });

  // Removing the skill chip used to fire GET /leaderboard?kind=skill with no
  // skill, which the API refuses with 422 — so "nothing chosen yet" rendered
  // as "That did not work".
  it("asks for a skill instead of erroring when the skill board has none", async () => {
    asHirer();
    const calls: string[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: string) => {
        calls.push(url);
        return new Response(
          JSON.stringify({ kind: "overall", rubric_version: "v1", entries: [] }),
          {
            status: 200,
            headers: { "content-type": "application/json" },
          },
        );
      }),
    );
    mount("/leaderboard");
    await screen.findByText("Overall");
    fireEvent.click(screen.getByText("By skill"));
    // The board defaults to a skill, so the empty state is reached by
    // REMOVING the chip — which is the path that used to 422.
    fireEvent.click(await screen.findByLabelText("Remove go"));
    expect(await screen.findByText("Pick a skill")).toBeTruthy();
    expect(calls.some((u) => /kind=skill(&|$)/.test(u) && !/skill=[^&]/.test(u))).toBe(false);
  });

  it("sends an unauthenticated visitor away from a private route", () => {
    mount("/search");
    expect(screen.getAllByText("Sign in with GitHub").length).toBeGreaterThan(0);
    expect(screen.queryByText("Search the pool")).toBeNull();
  });

  it("renders search results, keeping the gaps in the global rank sequence", async () => {
    asHirer();
    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: string) => {
        const body = url.includes("/shortlists")
          ? { shortlists: [] }
          : {
              total: 37,
              inactive_hidden: 6,
              ranked_by: "overall",
              page: 1,
              per_page: 20,
              results: [
                {
                  rank: 3,
                  id: "u1",
                  display_name: "Ada Okonkwo",
                  github_login: "aokonkwo",
                  active: true,
                  skills: [{ slug: "go", standing: "primary", score: 84.1 }],
                  overall_score: 78.4,
                  generalist_score: 141.2,
                },
                {
                  rank: 17,
                  id: "u2",
                  display_name: "Wen Li",
                  github_login: "wenli",
                  active: false,
                  skills: [{ slug: "go", standing: "primary", score: 75.5 }],
                  overall_score: 73.9,
                  generalist_score: null,
                },
              ],
            };
        return new Response(JSON.stringify(body), {
          status: 200,
          headers: { "content-type": "application/json" },
        });
      }),
    );

    mount("/search");

    expect(await screen.findByText("Ada Okonkwo")).toBeTruthy();
    // Rank is a label, not an index: the second row is #17, not #2.
    expect(screen.getByText("#17")).toBeTruthy();
    // inactive_hidden is still REPORTED — as a count beside the total, since
    // the banner that used to say it was dropped. A gap in the rank sequence
    // with nothing accounting for it reads as a broken filter.
    expect(screen.getByText(/6 hidden/)).toBeTruthy();
    // A null generalist reads as a dash, never as zero.
    expect(screen.getAllByText("—").length).toBeGreaterThan(0);

    // THE BUG THIS EXISTS TO CATCH. That stub returns no open roles, so there
    // is nowhere to put anybody — and the Shortlist button used to sit
    // ENABLED beside every result and do nothing at all when pressed, which
    // is indistinguishable from the feature being broken.
    const shortlist = screen.getAllByRole("button", { name: /Shortlist/ })[0];
    expect(shortlist).toHaveProperty("disabled", true);
    expect(screen.getByText(/no open roles to shortlist into/)).toBeTruthy();
  });

  // Three stacked lists put every draft and every closed role between a hirer
  // and the open one they came to look at. Filtered by status instead — and
  // by SEVERAL statuses, because comparing what you have open against what
  // you drafted is the case a single tab makes hardest.
  it("filters roles by any combination of statuses, and searches them", async () => {
    asHirer();
    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: string) => {
        const body = url.includes("/org-roles")
          ? {
              roles: [
                { ...ROLE_STUB, id: "r1", title: "Open one", status: "open" },
                { ...ROLE_STUB, id: "r2", title: "Draft one", status: "draft" },
                { ...ROLE_STUB, id: "r3", title: "Closed one", status: "closed" },
              ],
            }
          : { organization: { id: "o1", name: "Acme", verified: true } };
        return new Response(JSON.stringify(body), {
          status: 200,
          headers: { "content-type": "application/json" },
        });
      }),
    );

    mount("/roles");

    // Open by default: the only status a hirer acts on.
    expect(await screen.findByText("Open one")).toBeTruthy();
    expect(screen.queryByText("Draft one")).toBeNull();
    expect(screen.queryByText("Closed one")).toBeNull();

    // Adding a status WIDENS rather than replacing.
    fireEvent.click(screen.getByRole("button", { name: /Drafts/ }));
    expect(await screen.findByText("Draft one")).toBeTruthy();
    expect(screen.getByText("Open one")).toBeTruthy();

    // All three at once.
    fireEvent.click(screen.getByRole("button", { name: /Closed/ }));
    expect(await screen.findByText("Closed one")).toBeTruthy();

    // And the search narrows across whatever is ticked.
    fireEvent.change(screen.getByLabelText(/Search roles/), {
      target: { value: "closed" },
    });
    expect(await screen.findByText("Closed one")).toBeTruthy();
    expect(screen.queryByText("Open one")).toBeNull();
    expect(screen.queryByText("Draft one")).toBeNull();
  });

  // Scoped to one role. A hirer asking "did I already approach her for this
  // job" is asking about one job, so the box must not quietly search the
  // whole pool — and must say so when it finds nobody.
  it("searches the candidates on a role, within that role only", async () => {
    asHirer();
    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: string) => {
        let body: unknown = { organization: { id: "o1", name: "Acme", verified: true } };
        if (url.includes("/candidates")) {
          body = {
            candidates: [
              {
                user_id: "u1",
                display_name: "Alice Okafor",
                shortlist_name: "Round one",
                contact_status: "",
                notified_at: null,
                accepted: false,
              },
              {
                user_id: "u2",
                display_name: "Bob Nakamura",
                shortlist_name: "Round two",
                contact_status: "accepted",
                notified_at: "2026-09-01T00:00:00Z",
                accepted: true,
                email: "bob@example.com",
              },
            ],
          };
        } else if (url.includes("/org-roles")) {
          body = {
            roles: [{ ...ROLE_STUB, id: "r1", title: "Open one", status: "open" }],
          };
        }
        return new Response(JSON.stringify(body), {
          status: 200,
          headers: { "content-type": "application/json" },
        });
      }),
    );

    mount("/roles");
    fireEvent.click(await screen.findByRole("button", { name: /Who is on it/ }));

    expect(await screen.findByText("Alice Okafor")).toBeTruthy();
    expect(screen.getByText("Bob Nakamura")).toBeTruthy();

    // A name is a way through to the scorecard, everywhere it appears — the
    // evidence a hirer is deciding on should not be further away here than it
    // is in search.
    expect(screen.getByRole("link", { name: "Alice Okafor" })).toHaveProperty(
      "pathname",
      "/contributors/u1",
    );

    fireEvent.change(screen.getByLabelText(/Search candidates on Open one/), {
      target: { value: "bob" },
    });
    expect(await screen.findByText("Bob Nakamura")).toBeTruthy();
    expect(screen.queryByText("Alice Okafor")).toBeNull();

    // And an empty result says what was searched — the people on THIS role.
    fireEvent.change(screen.getByLabelText(/Search candidates on Open one/), {
      target: { value: "nobody" },
    });
    expect(await screen.findByText(/Nobody here matches/)).toBeTruthy();
    expect(screen.getByText(/already on\s+this role/)).toBeTruthy();

    // The address is carried ONLY for somebody who accepted, and it is on the
    // row rather than a screen away (ADR-0019 amendment 4).
    fireEvent.change(screen.getByLabelText(/Search candidates on Open one/), {
      target: { value: "" },
    });
    expect(await screen.findByText("bob@example.com")).toBeTruthy();

    // Filtering by stage. Alice is staged, Bob accepted — so ticking Accepted
    // leaves one of them, and no address belonging to the other appears.
    fireEvent.click(screen.getByRole("button", { name: "Accepted" }));
    expect(screen.queryByText("Alice Okafor")).toBeNull();
    expect(screen.getByText("Bob Nakamura")).toBeTruthy();

    // Untick and both are back: nothing ticked is no filter, not an empty list.
    fireEvent.click(screen.getByRole("button", { name: "Accepted" }));
    expect(screen.getByText("Alice Okafor")).toBeTruthy();
  });

  it("says why a search found nothing, rather than showing a blank list", async () => {
    asHirer();
    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: string) => {
        const body = url.includes("/org-roles")
          ? { roles: [{ ...ROLE_STUB, id: "r1", title: "Open one", status: "open" }] }
          : { organization: { id: "o1", name: "Acme", verified: true } };
        return new Response(JSON.stringify(body), {
          status: 200,
          headers: { "content-type": "application/json" },
        });
      }),
    );

    mount("/roles");
    await screen.findByText("Open one");

    fireEvent.change(screen.getByLabelText(/Search roles/), {
      target: { value: "nothing like this" },
    });

    // Naming the query, and pointing at the status ticks — a match hiding in
    // a closed role behind an unticked filter is the likeliest reason.
    expect(await screen.findByText(/Nothing matches/)).toBeTruthy();
    expect(screen.getByText(/statuses you have ticked/)).toBeTruthy();
  });

  // Staging somebody and then searching again used to offer them straight
  // back: the exclusion only applies when a role is named, and choosing where
  // to shortlist INTO never named one.
  it("naming the role to shortlist into excludes who is already on it", async () => {
    asHirer();
    const fetchMock = vi.fn(async (url: string) => {
      const body = url.includes("/org-roles")
        ? { roles: [{ ...ROLE_STUB, id: "r1", title: "Platform hiring", status: "open" }] }
        : {
            total: 0,
            inactive_hidden: 0,
            ranked_by: "overall",
            page: 1,
            per_page: 20,
            results: [],
          };
      return new Response(JSON.stringify(body), {
        status: 200,
        headers: { "content-type": "application/json" },
      });
    });
    vi.stubGlobal("fetch", fetchMock);

    mount("/search");
    fireEvent.change(await screen.findByLabelText(/Shortlist into/), {
      target: { value: "r1" },
    });

    await waitFor(() => {
      const calls = fetchMock.mock.calls as unknown as Array<[string]>;
      expect(calls.some(([url]) => url.includes("for_role=r1"))).toBe(true);
    });
  });

  // Going back to no role has to drop the EXCLUSION, not just the highlight.
  // It used to keep hiding people who were on a round for a role that was no
  // longer being searched, and the banner went on counting them under "this
  // role" — on a role with nobody staged at all.
  it("clearing the role it was filled from stops excluding that role's candidates", async () => {
    asHirer();
    const fetchMock = vi.fn(async (url: string) => {
      const body = url.includes("/org-roles")
        ? { roles: [{ ...ROLE_STUB, id: "r1", title: "Open one", status: "open" }] }
        : url.includes("/shortlists")
          ? { shortlists: [] }
          : {
              total: 0,
              inactive_hidden: 0,
              already_shortlisted: 4,
              ranked_by: "overall",
              page: 1,
              per_page: 20,
              results: [],
            };
      return new Response(JSON.stringify(body), {
        status: 200,
        headers: { "content-type": "application/json" },
      });
    });
    vi.stubGlobal("fetch", fetchMock);

    mount("/search");

    const picker = await screen.findByLabelText(/Start from a role/);
    fireEvent.change(picker, { target: { value: "r1" } });
    fireEvent.click(screen.getByRole("button", { name: /^Search$/ }));
    await waitFor(() => {
      const calls = fetchMock.mock.calls as unknown as Array<[string]>;
      expect(calls.some(([url]) => url.includes("for_role=r1"))).toBe(true);
    });

    fetchMock.mockClear();
    fireEvent.change(picker, { target: { value: "" } });
    fireEvent.click(screen.getByRole("button", { name: /^Search$/ }));

    await waitFor(() => {
      const calls = fetchMock.mock.calls as unknown as Array<[string]>;
      const searches = calls.filter(([url]) => url.includes("/search"));
      expect(searches.length).toBeGreaterThan(0);
      expect(searches.every(([url]) => !url.includes("for_role"))).toBe(true);
    });
  });

  // The role's own list is where a hirer works now (ADR-0023): who is on the
  // job, what each of them said, and the button that tells the ones who have
  // been told nothing.
  it("renders the people on a role, with the contact step and its acknowledgement", async () => {
    asHirer();
    const fetchMock = vi.fn(async (url: string) => {
      const body = url.includes("/candidates")
        ? {
            candidates: [
              {
                user_id: "u1",
                display_name: "Alice Okafor",
                github_login: "aliceok",
                note: "Strong retry semantics.",
                added_by: { id: "h1", username: "maya", display_name: "Maya Renner", active: true },
                added_at: "2026-09-01T00:00:00Z",
                notified_at: null,
                contact_status: "",
                accepted: false,
              },
              {
                user_id: "u2",
                display_name: "Bob Nakamura",
                github_login: "bobn",
                added_by: { id: "h1", username: "maya", display_name: "Maya Renner", active: true },
                added_at: "2026-09-01T00:00:00Z",
                notified_at: "2026-09-02T00:00:00Z",
                contact_status: "accepted",
                accepted: true,
                email: "bob@example.com",
              },
            ],
          }
        : { roles: [{ ...ROLE_STUB, id: "r1", title: "Open one", status: "open" }] };
      return new Response(JSON.stringify(body), {
        status: 200,
        headers: { "content-type": "application/json" },
      });
    });
    vi.stubGlobal("fetch", fetchMock);

    mount("/roles");
    fireEvent.click(await screen.findByRole("button", { name: /Who is on it/ }));

    expect(await screen.findByText("Alice Okafor")).toBeTruthy();
    expect(screen.getByText("Bob Nakamura")).toBeTruthy();

    // Both names go through to the scorecard.
    expect(screen.getByRole("link", { name: "Alice Okafor" })).toHaveProperty(
      "pathname",
      "/contributors/u1",
    );

    // An address appears only once somebody accepted.
    expect(screen.getByText(/bob@example.com/)).toBeTruthy();

    // Removal turns on notified_at, which is the boundary the SERVER
    // enforces: Alice was never told, Bob was.
    expect(screen.getAllByRole("button", { name: /Remove/ }).length).toBe(1);

    // THE IRREVERSIBLE STEP. It reaches only the person who has been told
    // nothing, and it stays disabled until a date is given and the warning is
    // acknowledged.
    const tell = screen.getByRole("button", { name: /Tell 1/ });
    expect(tell).toHaveProperty("disabled", true);
    fireEvent.change(screen.getByLabelText(/expect to have an answer by/), {
      target: { value: "2026-12-01" },
    });
    fireEvent.click(screen.getByLabelText(/cannot be undone/));
    expect(screen.getByRole("button", { name: /Tell 1/ })).toHaveProperty("disabled", false);
  });

  it("renders a contributor's standing, and never prints a null score as zero", async () => {
    sessionStorage.setItem("gcp.access", "t");
    sessionStorage.setItem("gcp.refresh", "r");
    sessionStorage.setItem(
      "gcp.principal",
      JSON.stringify({ id: "u1", display_name: "Ada Okonkwo", principal_type: "contributor" }),
    );

    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: string) => {
        const body = url.includes("/me/rank")
          ? {
              rubric_version: "v1",
              ranked: true,
              active: true,
              overall: { score: null, rank: 12, out_of: 480 },
              generalist: { score: null, rank: 6, out_of: 480 },
            }
          : {
              skills: [
                {
                  slug: "go",
                  standing: "primary",
                  distinct_pr_count: 7,
                  score: 84.1,
                  rubric_version: "v1",
                },
                {
                  slug: "postgres",
                  standing: "secondary",
                  distinct_pr_count: 3,
                  score: 62,
                  rubric_version: "v1",
                },
              ],
              overall_score: null,
              generalist_score: null,
              rubric_version: "v1",
            };
        return new Response(JSON.stringify(body), {
          status: 200,
          headers: { "content-type": "application/json" },
        });
      }),
    );

    mount("/");

    expect(await screen.findByText("Your standing")).toBeTruthy();
    // A secondary skill is shown, and told apart from a ranked one.
    expect(screen.getByText("Not yet ranked")).toBeTruthy();
    expect(screen.getByText(/2 more make this primary/)).toBeTruthy();
    // overall_score is null: it must read as a dash, never 0.0.
    expect(screen.queryByText("0.0")).toBeNull();
  });

  /* --- ADR-0016: usernames, the roster, redemption --------------------- */

  // Email stopped being an identity: two seats may share one, and a work
  // address outlives the person who held it. A form still asking for an email
  // would send an address where the server reads a username.
  it("asks a hirer for a username, not an email", async () => {
    const sent: unknown[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(async (_url: string, init: RequestInit) => {
        sent.push(JSON.parse(String(init.body)));
        return new Response(
          JSON.stringify({
            hirer: {
              id: "h1",
              display_name: "Maya Renner",
              principal_type: "hirer",
              organization: { id: "o1", name: "Northfield Labs", verified: true },
            },
            access_token: "a",
            refresh_token: "r",
          }),
          { status: 200, headers: { "content-type": "application/json" } },
        );
      }),
    );

    mount("/signin");
    fireEvent.change(screen.getByLabelText("Username"), { target: { value: "maya" } });
    fireEvent.change(screen.getAllByLabelText("Password")[0]!, { target: { value: "pw" } });
    fireEvent.click(screen.getByText("Sign in as a hirer"));

    await screen.findByText("Search the pool");
    expect(sent).toEqual([{ username: "maya", password: "pw" }]);
  });

  // The admin path still keys on an email, and the two credentials must not
  // share a box: one field would quietly send an address where a username goes.
  it("keeps the admin credential separate and still an email", () => {
    mount("/signin");
    expect(screen.getByLabelText("Username")).toBeTruthy();
    expect(screen.getByLabelText("Email")).toBeTruthy();
  });

  // Redemption must not become an oracle for who a company is hiring. The
  // server answers the same whether or not an address is rostered, and the
  // screen must not claim more than the server said.
  it("never says whether an address was on the roster", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: string) => {
        if (url.includes("/organizations")) {
          return new Response(JSON.stringify({ organizations: [{ name: "Acme", slug: "acme" }] }), {
            status: 200,
            headers: { "content-type": "application/json" },
          });
        }
        return new Response(null, { status: 202 });
      }),
    );

    mount("/redeem");
    fireEvent.change(await screen.findByLabelText("Your company"), {
      target: { value: "acme" },
    });
    fireEvent.change(screen.getByLabelText("Your work email"), {
      target: { value: "stranger@nowhere.example" },
    });
    fireEvent.click(screen.getByText("Send me a link"));

    expect(await screen.findByText(/is on that organisation/)).toBeTruthy();
    expect(screen.getByText(/do not say whether the address was listed/)).toBeTruthy();
  });

  // The username comes from the roster entry. A field for it here would let a
  // redeemer take a colleague's name.
  it("offers no username field when redeeming", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => new Response(null, { status: 202 })),
    );

    mount("/redeem?token=proof");
    expect(await screen.findByText("Set your password")).toBeTruthy();
    expect(screen.queryByLabelText("Username")).toBeNull();
    expect(screen.getByText(/your organisation pinned it/)).toBeTruthy();
  });

  // A revoked seat is listed, not dropped: a round from two years ago carries
  // their name, and a list that hid them would leave it unresolvable.
  it("lists a revoked seat, marked inactive", async () => {
    asHirer();
    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: string) => {
        const body = url.includes("/seats")
          ? {
              seats: [
                {
                  id: "h1",
                  username: "maya",
                  display_name: "Maya Renner",
                  email: "maya@northfield.example",
                  role: "owner",
                  active: true,
                },
                {
                  id: "h2",
                  username: "rita",
                  display_name: "Rita Sandoval",
                  email: "rita@northfield.example",
                  role: "member",
                  active: false,
                  disabled_at: "2026-01-04T00:00:00Z",
                },
              ],
            }
          : { entries: [] };
        return new Response(JSON.stringify(body), {
          status: 200,
          headers: { "content-type": "application/json" },
        });
      }),
    );

    mount("/team");
    expect(await screen.findByText("Rita Sandoval")).toBeTruthy();
    expect(screen.getByText("Access revoked")).toBeTruthy();
    expect(screen.getByText(/old rounds still carry their name/)).toBeTruthy();
  });

  // Removal revokes; it never deletes. The button must not promise otherwise.
  it("calls removing a claimed entry a revocation, not a deletion", async () => {
    asHirer();
    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: string) => {
        const body = url.includes("/seats")
          ? { seats: [] }
          : {
              entries: [
                {
                  id: "r1",
                  email: "rita@northfield.example",
                  username: "rita",
                  role: "member",
                  added_by: "h1",
                  redeemed_at: "2026-01-02T00:00:00Z",
                  created_at: "2026-01-01T00:00:00Z",
                },
              ],
            };
        return new Response(JSON.stringify(body), {
          status: 200,
          headers: { "content-type": "application/json" },
        });
      }),
    );

    mount("/team");
    expect(await screen.findByText("Revoke access")).toBeTruthy();
    expect(screen.queryByText("Delete")).toBeNull();
  });

  /* --- ADR-0017: listing a company ------------------------------------- */

  // Onboarding is how an OWNER comes to exist, so it must be reachable with no
  // account at all. A signed-out visitor hitting a redirect here could never
  // start a company.
  it("lets a visitor with no session list an organisation", () => {
    mount("/organisation");
    // By ROLE: the public nav carries a button with the same words, and
    // getByText would match both.
    expect(screen.getByRole("heading", { name: "List your organisation" })).toBeTruthy();
    expect(screen.getByLabelText("Company name")).toBeTruthy();
    // Sign-in is not forced on the way.
    expect(screen.queryByText("Sign in as a hirer")).toBeNull();
  });

  // The form must not name who will own the result. Whoever proves the company
  // address chooses that, which is what stops a submission handing someone
  // else's company to its author.
  it("asks for no username or password when submitting a company", () => {
    mount("/organisation");
    expect(screen.queryByLabelText("Username")).toBeNull();
    expect(screen.queryByLabelText("Password")).toBeNull();
  });

  // Headcount is a band. A free-text number invites false precision on a field
  // whose own name says "approx".
  it("offers headcount as a range rather than a number", () => {
    mount("/organisation");
    const select = screen.getByLabelText(/how many people/i) as HTMLSelectElement;
    expect(select.tagName).toBe("SELECT");
    expect([...select.options].map((o) => o.value)).toContain("11-50");
  });

  // The single most important thing this screen says. A submitter who believed
  // their company now existed would stop watching for the email, and would also
  // believe the name was theirs.
  it("says plainly that nothing has been created yet", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(async () => new Response(null, { status: 202 })),
    );

    mount("/organisation");
    fireEvent.change(screen.getByLabelText("Company name"), {
      target: { value: "Acme Corp" },
    });
    fireEvent.change(screen.getByLabelText("Company email"), {
      target: { value: "hiring@acme.example" },
    });
    fireEvent.click(screen.getByText("Send me a confirmation code"));

    expect(await screen.findByText("Check your email")).toBeTruthy();
    expect(screen.getByText(/Nothing has been created yet/)).toBeTruthy();
    expect(screen.getByText(/Nothing has been created yet/)).toBeTruthy();
  });

  // The verify screen is where a username IS chosen — and it must not imply an
  // account now exists, because approval is what creates one.
  it("chooses the owner's credentials at verify, and says no account exists yet", () => {
    mount("/organisation/verify?token=abc");
    expect(screen.getByLabelText("Username")).toBeTruthy();
    expect(screen.getByLabelText("Password")).toBeTruthy();
    expect(screen.getByText(/No account exists yet/)).toBeTruthy();
  });

  // Registration is the INDEPENDENT hirer's route now. An organisation field
  // here would create a second way to make a company.
  it("asks an independent hirer for no organisation", () => {
    mount("/register");
    expect(screen.getByText("Register as an independent hirer")).toBeTruthy();
    expect(screen.queryByText("Your organisation")).toBeNull();
  });

  // Approving a company CREATES it, which is unlike every other admin decision
  // on this screen. The queue has to say so, or an administrator reads it as
  // one more rubber stamp.
  it("tells an administrator that approving a company creates it", async () => {
    sessionStorage.setItem("gcp.access", "t");
    sessionStorage.setItem("gcp.refresh", "r");
    sessionStorage.setItem(
      "gcp.principal",
      JSON.stringify({ id: "ad1", display_name: "Root", principal_type: "admin" }),
    );

    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: string) => {
        const body = url.includes("/admin/onboarding")
          ? {
              total: 1,
              submissions: [
                {
                  id: "01920000-0000-7000-8000-00000000e001",
                  name: "Acme Corp",
                  description: "We build things.",
                  email: "hiring@acme.example",
                  phone: "+44 20 7946 0958",
                  headcount: "11-50",
                  address: null,
                  owner: { username: "jo", display_name: "Jo Mensah" },
                  created_at: "2026-09-13T09:00:00Z",
                  age_hours: 4,
                },
              ],
            }
          : { requests: [] };
        return new Response(JSON.stringify(body), {
          status: 200,
          headers: { "content-type": "application/json" },
        });
      }),
    );

    mount("/admin");
    expect(await screen.findByText("Acme Corp")).toBeTruthy();
    expect(screen.getByText(/Nothing here exists yet/)).toBeTruthy();
    // No address given is a fact about the company, not a blank.
    expect(screen.getByText("No office address given")).toBeTruthy();
    // The name the approval will make permanent is shown before it is made.
    expect(screen.getByText("jo")).toBeTruthy();
  });

  /* --- ADR-0018: the contributor profile -------------------------------- */

  function asContributor() {
    sessionStorage.setItem("gcp.access", "t");
    sessionStorage.setItem("gcp.refresh", "r");
    sessionStorage.setItem(
      "gcp.principal",
      JSON.stringify({ id: "u1", display_name: "Ada Okonkwo", principal_type: "contributor" }),
    );
  }

  function profileStub(over: Record<string, unknown> = {}) {
    return vi.fn(async (url: string) => {
      let body: unknown = {};
      if (url.includes("/me/profile")) {
        body = {
          preferences: {
            open_to_remote: false,
            open_to_internships: false,
            open_to_onsite: false,
            open_to_contract: false,
            open_to_freelance: false,
            current_country: "",
            office_yoe: null,
            first_pr_url: "",
            latest_pr_url: "",
          },
          needs_attention: false,
          readiness: { findable: true, blocking: [], limiting: [] },
          availability: { status: "looking", expires_at: "2026-12-01T00:00:00Z" },
          ...over,
        };
      } else if (url.includes("/me/compensation")) {
        body = { currency: "", hourly_rate: null, yearly_amount: null };
      } else if (url.includes("/places/countries")) {
        body = { countries: [{ code: "GB", name: "United Kingdom" }], degraded: false };
      } else if (url.includes("/me/contact-requests")) {
        body = { requests: [] };
      } else {
        body = { id: "u1", display_name: "Ada Okonkwo", principal_type: "contributor" };
      }
      return new Response(JSON.stringify(body), {
        status: 200,
        headers: { "content-type": "application/json" },
      });
    });
  }

  // The trap that USED to be here: looking, having ticked nothing, invisible to
  // every role and undiagnosable from outside. ADR-0021 removed the state
  // rather than the warning — looking now means open to anything — so the
  // assertion worth keeping is that the preferences no longer gate anything.
  it("does not warn a contributor who has ticked no preference", async () => {
    asContributor();
    vi.stubGlobal("fetch", profileStub());

    mount("/being-found");
    await screen.findByText(/What you are open to/);
    expect(screen.queryByText(/no role can reach you/)).toBeNull();
  });

  // THE BUG THIS EXISTS TO CATCH: ticking a preference and having nothing to
  // press. The shapes lived in one card and the only Save at the bottom of a
  // second, so a tick produced no visible save and nothing was ever written —
  // reported as "not persistent in the frontend or the database", which is
  // exactly what it looked like from outside.
  it("a ticked preference reaches the server", async () => {
    asContributor();
    const fetchMock = profileStub();
    vi.stubGlobal("fetch", fetchMock);

    mount("/being-found");
    await screen.findByText(/What you are open to/);

    // Nothing to save before anything changes.
    expect(screen.getByRole("button", { name: /Saved/ })).toHaveProperty("disabled", true);

    fireEvent.click(screen.getByRole("checkbox", { name: /Freelance work/ }));
    fireEvent.click(await screen.findByRole("button", { name: /Save your profile/ }));

    await waitFor(() => {
      const calls = fetchMock.mock.calls as unknown as Array<[string, RequestInit?]>;
      const put = calls.find(
        ([url, init]) => url.includes("/me/profile") && init?.method === "PUT",
      );
      expect(put).toBeTruthy();
      expect(JSON.parse(String(put?.[1]?.body)).open_to_freelance).toBe(true);
    });
  });

  // The question the page is about. Somebody could satisfy every field on it
  // and still be in no search result, because search reads overall_score —
  // and nothing said so.
  it("says plainly when nobody can find you, and why", async () => {
    asContributor();
    vi.stubGlobal(
      "fetch",
      profileStub({
        readiness: {
          findable: false,
          blocking: ["no_scored_claim"],
          limiting: ["no_country"],
        },
      }),
    );

    mount("/being-found");
    expect(await screen.findByText(/Can hirers find you/)).toBeTruthy();
    expect(screen.getByText(/None of your work has been scored yet/)).toBeTruthy();
    // And the blocker is separated from the thing that merely narrows.
    expect(screen.getByText(/You have not said where you are/)).toBeTruthy();
    expect(screen.getByText(/stopping you being found/)).toBeTruthy();
  });

  it("says so when the profile is complete", async () => {
    asContributor();
    vi.stubGlobal("fetch", profileStub());

    mount("/being-found");
    expect(await screen.findByText(/Your profile is complete/)).toBeTruthy();
  });

  it("offers freelance as a preference, not as an availability state", async () => {
    // It moved: availability used to carry freelance twice over, which is why
    // ADR-0018 refused a flag for it. Availability no longer says it at all.
    asContributor();
    vi.stubGlobal("fetch", profileStub());

    mount("/being-found");
    expect(await screen.findByRole("checkbox", { name: /Freelance work/ })).toBeTruthy();
    expect(screen.getByRole("checkbox", { name: /Looking for opportunities/ })).toBeTruthy();
  });

  // A hirer never sees expected pay, and the person typing it should be told so
  // where they type it — otherwise the honest answer is the one that costs them.
  it("says plainly that no hirer sees expected pay", async () => {
    asContributor();
    vi.stubGlobal("fetch", profileStub());

    mount("/being-found");
    expect(await screen.findByText(/No hirer ever sees this/)).toBeTruthy();
    expect(screen.getByText(/No hirer ever sees this/)).toBeTruthy();
  });

  // The first pull request is ASKED, not read off a claim — a contributor's
  // first contribution is often years old in a repository they never claimed
  // here. And it is write-once, so the field must say so BEFORE somebody types
  // into it and gets a 409 back.
  it("locks the first pull request once it is recorded", async () => {
    asContributor();
    vi.stubGlobal(
      "fetch",
      profileStub({
        preferences: {
          open_to_remote: true,
          open_to_internships: false,
          open_to_onsite: false,
          open_to_contract: false,
          current_country: "GB",
          office_yoe: 6,
          first_pr_url: "https://github.com/acme/platform/pull/1",
          latest_pr_url: "https://github.com/acme/platform/pull/9",
        },
      }),
    );

    mount("/being-found");
    const first = (await screen.findByLabelText("Your first pull request")) as HTMLInputElement;
    expect(first.readOnly).toBe(true);
    expect(screen.getByText(/cannot be changed/)).toBeTruthy();

    // The latest one is meant to change.
    const latest = screen.getByLabelText("Your latest pull request") as HTMLInputElement;
    expect(latest.readOnly).toBe(false);
  });

  it("leaves the first pull request editable until it is set", async () => {
    asContributor();
    vi.stubGlobal("fetch", profileStub());

    mount("/being-found");
    const first = (await screen.findByLabelText("Your first pull request")) as HTMLInputElement;
    expect(first.readOnly).toBe(false);
    expect(screen.getByText(/Once saved it cannot be changed/)).toBeTruthy();
  });

  // The picker fails open: a provider that is down must not block the form.
  it("lets a contributor type a country code when the picker is degraded", async () => {
    asContributor();
    vi.stubGlobal(
      "fetch",
      vi.fn(async (url: string) => {
        const body = url.includes("/places/countries")
          ? { countries: [], degraded: true }
          : url.includes("/me/profile")
            ? {
                preferences: {
                  open_to_remote: true,
                  open_to_internships: false,
                  open_to_onsite: false,
                  open_to_contract: false,
                  current_country: "",
                  office_yoe: null,
                  first_pr_url: "",
                  latest_pr_url: "",
                },
                needs_attention: false,
                availability: { status: "looking", expires_at: "2026-12-01T00:00:00Z" },
              }
            : url.includes("/me/compensation")
              ? { currency: "", hourly_rate: null, yearly_amount: null }
              : url.includes("/me/contact-requests")
                ? { requests: [] }
                : { id: "u1", display_name: "Ada", principal_type: "contributor" };
        return new Response(JSON.stringify(body), {
          status: 200,
          headers: { "content-type": "application/json" },
        });
      }),
    );

    mount("/being-found");
    const field = (await screen.findByLabelText("Where you are")) as HTMLElement;
    expect(field.tagName).toBe("INPUT");
    expect(screen.getByText(/type a two-letter code/)).toBeTruthy();
  });

  /* --- ADR-0024: role locations and organisation addresses -------------- */

  const BENGALURU = {
    id: "ad1",
    country: "IN",
    city: "Bengaluru",
    postal_code: "560001",
    street1: "1 MG Road",
    street2: "Floor 4",
    is_main_office: true,
    removed: false,
  };
  const AUSTIN = { ...BENGALURU, id: "ad2", country: "US", city: "Austin", is_main_office: false };

  type Reply = { status?: number; body?: unknown };

  /**
   * A transport answering by the first matching path fragment and method. A
   * function lets a test answer a write differently from a read.
   */
  function routes(table: Array<[string, Reply | ((init: RequestInit | undefined) => Reply)]>) {
    return vi.fn(async (url: string, init?: RequestInit) => {
      const hit = table.find(([fragment]) => url.includes(fragment));
      const reply = hit ? (typeof hit[1] === "function" ? hit[1](init) : hit[1]) : { body: {} };
      const status = reply.status ?? 200;
      return new Response(status === 204 ? null : JSON.stringify(reply.body ?? {}), {
        status,
        headers: { "content-type": "application/json" },
      });
    });
  }

  const seat = (role: "owner" | "member") => ({
    seats: [
      {
        id: "h1",
        username: "maya",
        display_name: "Maya Renner",
        email: "maya@northfield.example",
        role,
        active: true,
      },
    ],
  });

  const COUNTRIES = {
    countries: [
      { code: "IN", name: "India" },
      { code: "US", name: "United States" },
      { code: "GB", name: "United Kingdom" },
    ],
    degraded: false,
  };

  it("disables Onsite for a fully remote organisation, and says why to any seat", async () => {
    asHirer();
    vi.stubGlobal(
      "fetch",
      routes([
        ["/seats", { body: seat("member") }],
        ["/places/countries", { body: COUNTRIES }],
        [
          "/addresses",
          {
            body: {
              workplace: "fully_remote",
              addresses: [],
              office_request: {
                id: "q1",
                status: "rejected",
                address: { ...BENGALURU },
                requested_at: "2026-10-01T00:00:00Z",
                decided_at: "2026-10-02T00:00:00Z",
                reason: "The office lease is unsigned.",
              },
            },
          },
        ],
        ["/org-roles", { body: { roles: [] } }],
      ]),
    );

    mount("/roles");
    fireEvent.click(await screen.findByRole("button", { name: /Write a role/ }));

    const onsite = await screen.findByRole("checkbox", { name: /Onsite/ });
    await waitFor(() => expect(onsite).toHaveProperty("disabled", true));
    expect(
      screen.getByText(/The office request was not approved: The office lease is unsigned/),
    ).toBeTruthy();
    // Remote is always there.
    expect(screen.getByRole("checkbox", { name: "Remote" })).toHaveProperty("checked", true);
  });

  it("sends locations, and shows a refusal beside the office it names", async () => {
    asHirer();
    const posts: unknown[] = [];
    const fetchMock = routes([
      ["/seats", { body: seat("member") }],
      ["/places/countries", { body: COUNTRIES }],
      ["/money/currencies", { body: { currencies: [], degraded: true } }],
      [
        "/addresses",
        { body: { workplace: "office", addresses: [BENGALURU, AUSTIN], office_request: null } },
      ],
      [
        "/org-roles",
        (init) => {
          if (init?.method !== "POST") return { body: { roles: [] } };
          posts.push(JSON.parse(String(init.body)));
          return {
            status: 422,
            body: { error: "address_removed", field: "locations", position: 2 },
          };
        },
      ],
    ]);
    vi.stubGlobal("fetch", fetchMock);

    mount("/roles");
    fireEvent.click(await screen.findByRole("button", { name: /Write a role/ }));
    fireEvent.change(screen.getByLabelText("Title"), { target: { value: "Field engineer" } });

    // Remote in a country, and onsite at one office.
    fireEvent.click(screen.getByRole("radio", { name: /Only in certain countries/ }));
    // The picker is a text box until the list arrives; wait for the list.
    await screen.findByRole("option", { name: "United Kingdom" });
    fireEvent.change(screen.getByLabelText("Countries"), { target: { value: "GB" } });
    fireEvent.click(screen.getByRole("checkbox", { name: /Onsite/ }));
    fireEvent.click(await screen.findByRole("checkbox", { name: /Austin, United States/ }));

    fireEvent.click(screen.getByRole("button", { name: /Save as a draft/ }));

    await waitFor(() => expect(posts.length).toBe(1));
    const body = posts[0] as Record<string, unknown>;
    expect(body.locations).toEqual([
      { mode: "remote", country: "GB" },
      { mode: "onsite", address_id: "ad2" },
    ]);
    // The three retired keys are refused as unknown, so none may be sent.
    expect(body).not.toHaveProperty("location");
    expect(body).not.toHaveProperty("address_id");
    expect(body).not.toHaveProperty("eligible_countries");

    // Position 2 is Austin, so the message sits under Austin.
    const austin = screen.getByRole("checkbox", { name: /Austin, United States/ });
    await waitFor(() =>
      expect(austin.closest("div")?.textContent).toMatch(/That office has been removed/),
    );
    const bengaluru = screen.getByRole("checkbox", { name: /Bengaluru, India/ });
    expect(bengaluru.closest("div")?.textContent).not.toMatch(/removed\./);
  });

  it("creates and opens a role in one go for a seat that may open", async () => {
    asHirer();
    const calls: string[] = [];
    vi.stubGlobal(
      "fetch",
      routes([
        ["/seats", { body: seat("owner") }],
        ["/addresses", { body: { workplace: "office", addresses: [], office_request: null } }],
        [
          "/settings",
          {
            body: {
              role_create_authority: "draft_and_approve",
              role_update_authority: "draft_and_approve",
              role_close_authority: "any_hirer",
            },
          },
        ],
        [
          "/open",
          (init) => {
            calls.push(`${init?.method} open`);
            return { body: { id: "r9", status: "open" } };
          },
        ],
        [
          "/org-roles",
          (init) => {
            if (init?.method !== "POST") return { body: { roles: [] } };
            calls.push("POST create");
            return { status: 201, body: { id: "r9", status: "draft" } };
          },
        ],
      ]),
    );

    mount("/roles");
    fireEvent.click(await screen.findByRole("button", { name: /Write a role/ }));
    fireEvent.change(screen.getByLabelText("Title"), { target: { value: "Platform engineer" } });
    fireEvent.click(await screen.findByRole("button", { name: /Create and open/ }));

    await waitFor(() => expect(calls).toEqual(["POST create", "POST open"]));
  });

  it("offers only a draft to a member who may not open", async () => {
    asHirer();
    const fetchMock = routes([
      ["/seats", { body: seat("member") }],
      ["/addresses", { body: { workplace: "office", addresses: [], office_request: null } }],
      [
        "/settings",
        {
          body: {
            role_create_authority: "draft_and_approve",
            role_update_authority: "draft_and_approve",
            role_close_authority: "any_hirer",
          },
        },
      ],
      ["/org-roles", { body: { roles: [] } }],
    ]);
    vi.stubGlobal("fetch", fetchMock);

    mount("/roles");
    fireEvent.click(await screen.findByRole("button", { name: /Write a role/ }));
    await screen.findByRole("button", { name: /Save as a draft/ });
    // Absent because the member may not open — not merely because the
    // settings had not arrived yet.
    const read = (fragment: string) =>
      fetchMock.mock.calls.some(([url]) => String(url).includes(fragment));
    await waitFor(() => expect(read("/settings") && read("/seats")).toBe(true));
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(screen.queryByRole("button", { name: /Create and open/ })).toBeNull();
  });

  it("puts an invalid_role beside the field it names", async () => {
    asHirer();
    vi.stubGlobal(
      "fetch",
      routes([
        ["/seats", { body: seat("member") }],
        ["/addresses", { body: { workplace: "office", addresses: [], office_request: null } }],
        ["/money/currencies", { body: { currencies: [], degraded: true } }],
        [
          "/org-roles",
          (init) =>
            init?.method === "POST"
              ? { status: 422, body: { error: "invalid_role", field: "yearly_base" } }
              : { body: { roles: [] } },
        ],
      ]),
    );

    mount("/roles");
    fireEvent.click(await screen.findByRole("button", { name: /Write a role/ }));
    fireEvent.change(screen.getByLabelText("Title"), { target: { value: "Impossible" } });
    fireEvent.click(screen.getByRole("button", { name: /Save as a draft/ }));

    const base = screen.getByLabelText("Base a year").closest(".field");
    await waitFor(() => expect(base?.textContent).toMatch(/no more than the total package/));
    expect(
      screen.getByLabelText("Total package a year").closest(".field")?.textContent,
    ).not.toMatch(/no more than/);
  });

  it("shows a member the offices and none of the owner's controls", async () => {
    asHirer();
    vi.stubGlobal(
      "fetch",
      routes([
        ["/places/countries", { body: COUNTRIES }],
        ["/seats", { body: seat("member") }],
        [
          "/addresses",
          { body: { workplace: "office", addresses: [BENGALURU, AUSTIN], office_request: null } },
        ],
      ]),
    );

    mount("/offices");
    expect(await screen.findByText("Bengaluru, India")).toBeTruthy();
    expect(screen.getByText("Main office")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Remove" })).toBeNull();
    expect(screen.queryByText("Add an office")).toBeNull();
    expect(screen.queryByRole("button", { name: /Go fully remote/ })).toBeNull();
  });

  it("lets an owner remove any office but the main one, naming the roles that block it", async () => {
    asHirer();
    vi.stubGlobal(
      "fetch",
      routes([
        ["/places/countries", { body: COUNTRIES }],
        ["/seats", { body: seat("owner") }],
        [
          "/addresses/ad2",
          {
            status: 409,
            body: {
              error: "address_in_use",
              roles: [
                { id: "r1", title: "Austin field engineer", status: "draft" },
                { id: "r2", title: "Austin support lead", status: "open" },
              ],
            },
          },
        ],
        [
          "/addresses",
          { body: { workplace: "office", addresses: [BENGALURU, AUSTIN], office_request: null } },
        ],
      ]),
    );

    mount("/offices");
    await screen.findByText("Austin, United States");
    // One Remove: Austin's. The main office has none.
    const remove = await screen.findAllByRole("button", { name: "Remove" });
    expect(remove.length).toBe(1);

    fireEvent.click(remove[0]!);
    expect(await screen.findByText("Austin field engineer")).toBeTruthy();
    expect(screen.getByText("Austin support lead")).toBeTruthy();
    expect(screen.getByText(/Draft or open roles still use this office/)).toBeTruthy();
  });

  it("names the onsite roles that stop an organisation going fully remote", async () => {
    asHirer();
    vi.stubGlobal(
      "fetch",
      routes([
        ["/places/countries", { body: COUNTRIES }],
        ["/seats", { body: seat("owner") }],
        [
          "/fully-remote",
          {
            status: 409,
            body: {
              error: "onsite_roles_exist",
              roles: [{ id: "r2", title: "Austin support lead", status: "open" }],
            },
          },
        ],
        [
          "/addresses",
          { body: { workplace: "office", addresses: [BENGALURU], office_request: null } },
        ],
      ]),
    );

    mount("/offices");
    const go = await screen.findByRole("button", { name: /Go fully remote/ });
    expect(go).toHaveProperty("disabled", true);
    fireEvent.click(screen.getByRole("checkbox", { name: /Remove every address/ }));
    fireEvent.click(screen.getByRole("button", { name: /Go fully remote/ }));

    expect(await screen.findByText("Austin support lead")).toBeTruthy();
    expect(screen.getByText(/still onsite/)).toBeTruthy();
  });

  it("shows every seat a pending office request, and offers no second one", async () => {
    asHirer();
    vi.stubGlobal(
      "fetch",
      routes([
        ["/places/countries", { body: COUNTRIES }],
        ["/seats", { body: seat("owner") }],
        [
          "/addresses",
          {
            body: {
              workplace: "fully_remote",
              addresses: [],
              office_request: {
                id: "q1",
                status: "pending",
                address: { ...BENGALURU },
                requested_at: "2026-10-01T00:00:00Z",
              },
            },
          },
        ],
      ]),
    );

    mount("/offices");
    expect(await screen.findByText(/Office request waiting for review/)).toBeTruthy();
    expect(screen.queryByText("Ask for an office")).toBeNull();
  });

  it("lets an owner ask again after a rejection, and shows the reason", async () => {
    asHirer();
    const posted: unknown[] = [];
    vi.stubGlobal(
      "fetch",
      routes([
        ["/places/countries", { body: COUNTRIES }],
        ["/seats", { body: seat("owner") }],
        [
          "/office-request",
          (init) => {
            posted.push(JSON.parse(String(init?.body)));
            return { status: 422, body: { error: "invalid_address", field: "city" } };
          },
        ],
        [
          "/addresses",
          {
            body: {
              workplace: "fully_remote",
              addresses: [],
              office_request: {
                id: "q1",
                status: "rejected",
                address: { ...BENGALURU },
                requested_at: "2026-10-01T00:00:00Z",
                decided_at: "2026-10-02T00:00:00Z",
                reason: "The office lease is unsigned.",
              },
            },
          },
        ],
      ]),
    );

    mount("/offices");
    expect(await screen.findByText("The office lease is unsigned.")).toBeTruthy();
    expect(screen.getByText("Ask for an office")).toBeTruthy();

    fireEvent.change(await screen.findByLabelText("Country"), { target: { value: "IN" } });
    fireEvent.click(screen.getByRole("button", { name: /Send for review/ }));

    await waitFor(() => expect(posted.length).toBe(1));
    expect(posted[0]).toEqual({
      country: "IN",
      city: "",
      postal_code: "",
      street1: "",
      street2: "",
    });
    // invalid_address names a top-level field; the message sits under City.
    await waitFor(() =>
      expect(screen.getByLabelText("City").closest(".field")?.textContent).toMatch(/Give a city/),
    );
  });

  it("disables the address when a company is fully remote, and sends none", async () => {
    const sent: unknown[] = [];
    vi.stubGlobal(
      "fetch",
      vi.fn(async (_url: string, init?: RequestInit) => {
        sent.push(JSON.parse(String(init?.body)));
        return new Response(null, { status: 202 });
      }),
    );

    mount("/organisation");
    fireEvent.change(screen.getByLabelText("Company name"), { target: { value: "Driftwood" } });
    fireEvent.change(screen.getByLabelText("Company email"), {
      target: { value: "hello@driftwood.example" },
    });
    fireEvent.change(screen.getByLabelText("City"), { target: { value: "Cardiff" } });
    fireEvent.change(screen.getByLabelText("Office or fully remote"), {
      target: { value: "fully_remote" },
    });

    expect(screen.getByLabelText("City")).toHaveProperty("disabled", true);
    expect(screen.getByLabelText("Street")).toHaveProperty("disabled", true);

    fireEvent.click(screen.getByText("Send me a confirmation code"));
    await screen.findByText("Check your email");
    expect(sent[0]).toMatchObject({ name: "Driftwood", workplace: "fully_remote" });
    expect(sent[0]).not.toHaveProperty("address");
  });

  it("puts an onboarding refusal beside the field it names", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(
        async () =>
          new Response(JSON.stringify({ error: "invalid_onboarding", field: "address.street1" }), {
            status: 422,
            headers: { "content-type": "application/json" },
          }),
      ),
    );

    mount("/organisation/revise?token=t");
    fireEvent.change(screen.getByLabelText("Company name"), { target: { value: "Quayside" } });
    fireEvent.change(screen.getByLabelText("Company email"), {
      target: { value: "owner@quayside.example" },
    });
    fireEvent.change(screen.getByLabelText("Office or fully remote"), {
      target: { value: "office" },
    });
    fireEvent.click(screen.getByText("Send me a confirmation code"));

    await waitFor(() =>
      expect(screen.getByLabelText("Street").closest(".field")?.textContent).toMatch(
        /Give the office's street/,
      ),
    );
  });

  it("tells a contributor a city and a country for an opening, never a street", async () => {
    sessionStorage.setItem("gcp.access", "t");
    sessionStorage.setItem("gcp.refresh", "r");
    sessionStorage.setItem(
      "gcp.principal",
      JSON.stringify({ id: "u1", display_name: "Ada Okonkwo", principal_type: "contributor" }),
    );
    vi.stubGlobal(
      "fetch",
      routes([
        ["/places/countries", { body: COUNTRIES }],
        [
          "/openings",
          {
            body: {
              matched: 1,
              missed: 0,
              openings: [
                {
                  id: "op1",
                  organization: "Acme Corp",
                  posted_at: "2026-10-01T00:00:00Z",
                  bar: {
                    min_overall_score: null,
                    min_generalist_score: null,
                    min_oss_yoe: null,
                    skills: [],
                  },
                  role: {
                    id: "r1",
                    title: "Payments engineer",
                    description: "",
                    engagement: "full_time",
                    locations: [
                      { mode: "remote", country: "IN" },
                      { mode: "onsite", country: "IN", city: "Bengaluru" },
                    ],
                    currency: "",
                    yearly_ctc: null,
                    yearly_base: null,
                    hourly_rate: null,
                    expected_hours: null,
                    requires_online_test: null,
                    max_interview_rounds: null,
                    avg_days_to_offer: null,
                    questions: [],
                  },
                },
              ],
            },
          },
        ],
      ]),
    );

    mount("/openings");
    expect(await screen.findByText("Payments engineer")).toBeTruthy();
    expect(await screen.findByText("Remote in India · Onsite in Bengaluru, India.")).toBeTruthy();
  });

  it("gives an administrator the office-request queue, a rejection needing a reason", async () => {
    sessionStorage.setItem("gcp.access", "t");
    sessionStorage.setItem("gcp.refresh", "r");
    sessionStorage.setItem(
      "gcp.principal",
      JSON.stringify({ id: "ad1", display_name: "Root", principal_type: "admin" }),
    );
    const decided: unknown[] = [];
    vi.stubGlobal(
      "fetch",
      routes([
        [
          "/decide",
          (init) => {
            decided.push(JSON.parse(String(init?.body)));
            return {
              body: {
                id: "q2",
                status: "rejected",
                reviewed_at: "2026-10-17T00:00:00Z",
                reviewed_by: "ad1",
              },
            };
          },
        ],
        [
          "/admin/office-requests",
          {
            body: {
              total: 1,
              office_requests: [
                {
                  id: "q2",
                  organization: { id: "o2", name: "Tiny Studio" },
                  address: {
                    country: "PT",
                    city: "Lisbon",
                    postal_code: "1100-053",
                    street1: "Rua Augusta 100",
                    street2: "3rd floor",
                  },
                  requested_by: "sam",
                  created_at: "2026-10-17T00:00:00Z",
                  supersedes: "q1",
                },
              ],
            },
          },
        ],
        ["/admin/onboarding", { body: { total: 0, submissions: [] } }],
        ["/admin/", { body: { requests: [] } }],
      ]),
    );

    mount("/admin");
    expect(await screen.findByText("Tiny Studio")).toBeTruthy();
    expect(screen.getByText(/Rua Augusta 100/)).toBeTruthy();
    expect(screen.getByText("Asked again")).toBeTruthy();

    const reject = screen.getAllByRole("button", { name: "Reject" })[0]!;
    expect(reject).toHaveProperty("disabled", true);
    fireEvent.change(screen.getByLabelText("Reason", { selector: "#office-reason-q2" }), {
      target: { value: "Not a real office." },
    });
    fireEvent.click(screen.getAllByRole("button", { name: "Reject" })[0]!);
    await waitFor(() =>
      expect(decided).toEqual([{ decision: "rejected", reason: "Not a real office." }]),
    );
  });
});
