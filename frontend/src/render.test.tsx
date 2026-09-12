// Does it render at all?
//
// A build succeeding says the types line up, not that a component runs. These
// mount the real screens against a stubbed transport and assert something a
// person would look for — the cheapest way to catch a crash typechecking
// cannot see.

import { fireEvent, render, screen } from "@testing-library/react";
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
      organization: { name: "Northfield Labs", verified: true },
    }),
  );
}

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

  // The stand-in's picker offers a hirer link for every GitHub identity, and
  // that return used to land on the SPA's 404 because no route claimed it.
  it("has a route for the hirer GitHub return, not just the contributor one", () => {
    mount("/auth/hirer/github/callback?code=e2e-dave&state=s");
    // The callback page is what should claim this path — it sits on "Loading
    // your session…" while the exchange runs. The 404 prints the unmatched
    // path in a <code>, so its absence is the assertion that matters.
    expect(screen.getByText("Loading your session…")).toBeTruthy();
    expect(screen.queryByText("/auth/hirer/github/callback")).toBeNull();
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
        return new Response(JSON.stringify({ kind: "overall", rubric_version: "v1", entries: [] }), {
          status: 200,
          headers: { "content-type": "application/json" },
        });
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
    // inactive_hidden is reported beside the toggle that would fill the gaps.
    expect(screen.getAllByText(/hidden because their availability lapsed/).length).toBe(1);
    // A null generalist reads as a dash, never as zero.
    expect(screen.getAllByText("—").length).toBeGreaterThan(0);
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
});
