// Searching the ranked pool.
//
// Three things the design makes visible on purpose, because they are the ones
// implementations lose:
//
//   Rank is GLOBAL. Filtering does not recompute it, so this list has gaps in
//   its rank sequence and reports `inactive_hidden` beside the toggle that
//   fills them. Any client assuming results[i].rank === i + 1 is wrong.
//
//   Only PRIMARY standing is searchable. A skill with four surviving PRs shows
//   on a scorecard and never in this filter — so the filter says so rather than
//   letting a hirer conclude nobody has it.
//
//   The gate is on the ACCOUNT, not the query. An unverified hiring account is
//   refused here, and saying so plainly beats an empty result set that looks
//   like nobody matched.

import { useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import type { SearchResult } from "../contract";
import {
  describe,
  isEmpty,
  pageCount,
  useOrgID,
  useRoles,
  useSaveSearch,
  useSearch,
  useShortlists,
  useStageEntry,
} from "../hooks/hirer";
import type { SearchFilters } from "../hooks/hirer";
import { applyRole } from "../logic/roleFilters";
import * as format from "../logic/format";
import { CountryList } from "../ui/countries";
import { SkillPicker } from "../ui/SkillPicker";
import {
  Avatar,
  Banner,
  Button,
  Card,
  Checkbox,
  Empty,
  Failure,
  Field,
  Icon,
  Loading,
  PageHead,
  Pill,
  Stat,
} from "../ui/primitives";

const AVAILABILITY_CHOICES = [
  ["looking_for_job", "Looking for a job"],
  ["looking_for_freelance", "Looking for freelance work"],
  ["open_to_freelance", "Open to freelance work"],
] as const;

// What a contributor said they would take (ADR-0018 §2). Independent, because
// somebody open to a remote contract and an onsite permanent role is stating
// two things and a single choice would make them pick.
const SHAPE_CHOICES = [
  ["remote", "Remote"],
  ["onsite", "Onsite"],
  ["contract", "Contract"],
  ["internship", "Internship"],
] as const;

/** The gate codes that mean "this account", not "this query". */
function gated(code: string): boolean {
  return (
    code === "hiring_capability_required" || code === "verification_needed" || code === "forbidden"
  );
}

function SkillPill({ slug, name, standing, score }: SearchResult["skills"][number]) {
  return (
    <span className={standing === "primary" ? "skill-pill skill-pill--primary" : "skill-pill"}>
      {name ?? slug}
      <span className="skill-pill__score mono">{format.n1(score)}</span>
    </span>
  );
}

function Row({
  result,
  rankedBy,
  onStage,
  staged,
  staging,
}: {
  result: SearchResult;
  rankedBy: string;
  onStage: (userId: string) => void;
  staged: boolean;
  staging: boolean;
}) {
  const quiet = !result.active;

  return (
    <li className={quiet ? "row row--inactive" : "row"}>
      <div className={result.rank <= 3 ? "rank rank--top" : "rank"}>
        <span className="rank__n mono">#{result.rank}</span>
        <span className="rank__label">{rankedBy}</span>
      </div>

      <Avatar name={result.display_name} size="lg" tint={(result.rank % 6) + 1} />

      <div className="row__mid">
        <div className="row__ident">
          <Link className="row__name" to={`/contributors/${result.id}`}>
            {result.display_name}
          </Link>
          <span className="row__login mono">@{result.github_login}</span>
        </div>

        <div className="row__skills">
          {result.skills.slice(0, 4).map((skill) => (
            <SkillPill key={skill.slug} {...skill} />
          ))}
        </div>

        <div className="row__meta">
          {result.availability ? (
            <span>{format.availability(result.availability.status)}</span>
          ) : (
            <span>Availability not stated</span>
          )}
          {quiet && result.availability?.inactive_for_days != null ? (
            <span>
              Quiet for <b className="mono">{result.availability.inactive_for_days}</b> days
            </span>
          ) : null}
        </div>
      </div>

      <div className="row__right">
        <div className="row__scores">
          <Stat
            n={<span className="mono">{format.n1(result.overall_score)}</span>}
            label="Overall"
          />
          <Stat
            n={<span className="mono">{format.n1(result.generalist_score)}</span>}
            label="Generalist"
            muted
          />
        </div>

        {quiet ? <Pill tone="stale">Quiet</Pill> : null}

        <div className="row__actions">
          <Link className="btn btn--ghost btn--sm" to={`/contributors/${result.id}`}>
            Scorecard
          </Link>
          <Button
            variant="primary"
            size="sm"
            disabled={staged || staging}
            onClick={() => onStage(result.id)}
          >
            {staged ? (
              <>
                <Icon name="check" size="sm" />
                Staged
              </>
            ) : (
              <>
                <Icon name="plus" size="sm" />
                Shortlist
              </>
            )}
          </Button>
        </div>
      </div>
    </li>
  );
}

export function SearchPage() {
  const navigate = useNavigate();

  const [draft, setDraft] = useState<SearchFilters>({ perPage: 20 });
  const [applied, setApplied] = useState<SearchFilters>({ perPage: 20 });
  const [searched, setSearched] = useState(true);
  const [saveName, setSaveName] = useState("");

  // The organisation's open roles, offered as a starting point for the filters
  // below. Drafts and closed roles are not offered: a draft is not a
  // commitment and a closed one is a withdrawn opening, so neither is a job
  // anybody should be searched for.
  const org = useOrgID();
  const roles = useRoles(org.id, "open");
  const openRoles = roles.data?.roles ?? [];

  // The role the filters were filled from. Held so the picker SHOWS it: a
  // select that reset itself to "Choose a role…" the moment you chose one
  // looked like the click had failed.
  //
  // It is a note, not a binding. The hirer edits freely afterwards and nothing
  // re-applies, which is why the line below says so rather than leaving them
  // to wonder whether the role is still driving the panel.
  const [fromRole, setFromRole] = useState("");

  const results = useSearch(applied, searched);
  const save = useSaveSearch();

  // Staging discloses nothing, so it is safe to offer straight from a row. The
  // irreversible step lives on the round itself (ADR-0008 §3a).
  const rounds = useShortlists();
  const [target, setTarget] = useState("");
  const stage = useStageEntry(target);
  const [staged, setStaged] = useState<string[]>([]);

  const openRounds = (rounds.data?.shortlists ?? []).filter((r) => r.status !== "closed");

  function apply(next: SearchFilters) {
    setApplied(next);
    setSearched(true);
  }

  function reset() {
    setDraft({ perPage: 20 });
    setFromRole("");
    apply({ perPage: 20 });
  }

  if (results.error && gated(results.error.code)) {
    return (
      <div className="page page--narrow">
        <PageHead eyebrow="Discovery" title="Search the pool" />
        <Card>
          <p>{results.error.message}</p>
          <p className="small muted">
            Search and shortlisting are open to verified hiring accounts only — a scorecard names a
            person and their work, so the account asking for it is checked first.
          </p>
        </Card>
      </div>
    );
  }

  const data = results.data;
  const perPage = applied.perPage ?? 20;
  const pages = data ? pageCount(data.total, data.per_page || perPage) : 1;
  const page = applied.page ?? 1;

  const toggle = (list: string[] | undefined, value: string) => {
    const current = list ?? [];
    return current.includes(value) ? current.filter((v) => v !== value) : [...current, value];
  };

  return (
    <div className="page">
      <PageHead
        eyebrow="Discovery"
        title="Search the pool"
        lede="Every score here rests on merged pull requests a model has read. Filter the ranking, then read the evidence before you commit a round to anyone."
        actions={
          <Link className="btn btn--ghost" to="/leaderboard">
            View the full board
          </Link>
        }
      />

      <div className="discovery">
        <form
          className="filters"
          onSubmit={(e) => {
            e.preventDefault();
            apply({ ...draft, page: 1 });
          }}
        >
          <div className="filters__head">
            <span className="panel-title">Filters</span>
            <Button variant="quiet" size="sm" onClick={reset}>
              Reset
            </Button>
          </div>

          <div className="filters__body">
            {/* START FROM A ROLE. A hirer who has written one has already
                answered most of this panel — where they can employ somebody,
                what experience they need, what shape of work it is — and
                re-typing it is duplicated work that also drifts: the search
                quietly stops matching the job it is for.

                It fills the fields and then lets go. Nothing here re-applies
                afterwards and nothing remembers the role, because filters that
                snapped back to it would be worse than typing them out. */}
            {openRoles.length > 0 ? (
              <div className="fgroup">
                <Field
                  label="Start from a role"
                  htmlFor="from_role"
                  help="Fills in the countries, the minimums and the shape of work, and stops offering people already on a round for it. Change anything you like afterwards."
                >
                  <select
                    className="input"
                    id="from_role"
                    value={fromRole}
                    onChange={(event) => {
                      const id = event.target.value;
                      setFromRole(id);
                      const role = openRoles.find((r) => r.id === id);
                      if (role) setDraft(applyRole(draft, role));
                    }}
                  >
                    <option value="">Choose a role…</option>
                    {openRoles.map((role) => (
                      <option key={role.id} value={role.id}>
                        {role.title}
                      </option>
                    ))}
                  </select>
                </Field>

                {/* Re-picking the same option fires no change event, so a
                    hirer who filled from a role, edited the filters and wanted
                    to start over had no way back to it. This is that way. */}
                {fromRole ? (
                  <p className="field__help">
                    The filters below came from this role and are yours to change.{" "}
                    <button
                      type="button"
                      className="linklike"
                      onClick={() => {
                        const role = openRoles.find((r) => r.id === fromRole);
                        if (role) setDraft(applyRole(draft, role));
                      }}
                    >
                      Fill them in again
                    </button>
                  </p>
                ) : null}
              </div>
            ) : null}

            <div className="fgroup">
              <Field
                label="Find someone by name"
                htmlFor="q"
                help="A name lookup also surfaces contributors who have gone quiet — you already know they exist."
              >
                <div className="search-input">
                  <Icon name="search" size="sm" />
                  <input
                    className="input"
                    id="q"
                    type="search"
                    autoComplete="off"
                    placeholder="Name or GitHub login"
                    value={draft.q ?? ""}
                    onChange={(event) => setDraft({ ...draft, q: event.target.value })}
                  />
                </div>
              </Field>
            </div>

            <div className="fgroup">
              <div className="fgroup__title">
                Ranked skills <span className="muted">· all must match</span>
              </div>
              <SkillPicker
                id="skills"
                label="Add a skill"
                help="Only primary standing — five distinct merged PRs — is searchable. A skill with four is visible on the scorecard and never in this list."
                chosen={draft.skills ?? []}
                onChange={(skills) => setDraft({ ...draft, skills })}
              />
            </div>

            <div className="fgroup">
              <NumberField
                id="min_skill_score"
                label="Minimum skill score"
                help="Applies to every skill named above."
                max={100}
                value={draft.minSkillScore}
                onChange={(v) => setDraft({ ...draft, minSkillScore: v })}
              />
              <NumberField
                id="min_overall_score"
                label="Minimum overall score"
                help="Depth: their best skill, plus headroom for breadth."
                max={100}
                value={draft.minOverallScore}
                onChange={(v) => setDraft({ ...draft, minOverallScore: v })}
              />
              <NumberField
                id="min_generalist_score"
                label="Minimum generalist score"
                help="Breadth, unbounded. Setting this orders the list by generalist."
                max={220}
                value={draft.minGeneralistScore}
                onChange={(v) => setDraft({ ...draft, minGeneralistScore: v })}
              />
            </div>

            <div className="fgroup">
              <div className="fgroup__title">Availability</div>
              {AVAILABILITY_CHOICES.map(([value, label]) => (
                <Checkbox
                  key={value}
                  label={label}
                  checked={draft.availability?.includes(value) ?? false}
                  onChange={() =>
                    setDraft({ ...draft, availability: toggle(draft.availability, value) })
                  }
                />
              ))}
              <label className="switchline">
                <span className="switchline__text">
                  <span className="field__label">Include quiet profiles</span>
                </span>
                <input
                  type="checkbox"
                  checked={draft.includeInactive ?? false}
                  onChange={(event) => {
                    // This one applies on the spot rather than waiting for
                    // Search. It widens the population rather than narrowing
                    // it, so there is nothing to compose it with — and the
                    // empty state's "include quiet profiles" button already
                    // applies it immediately, which made the switch look dead
                    // next to a button that did the same thing and worked.
                    const next = { ...draft, includeInactive: event.target.checked };
                    setDraft(next);
                    apply({ ...next, page: 1 });
                  }}
                />
                <span className="switch" aria-hidden="true" />
              </label>
              <p className="field__help">
                This reveals people who went quiet. It never reveals anyone who said they are not
                looking — that is an opt-out, and no toggle undoes it.
              </p>
            </div>

            {/* What they SAID, as opposed to what they built (ADR-0018).
                Everything here is opt-in, so setting one of these also drops
                everybody who has not filled their profile in — which the help
                text says, because a hirer watching a result count halve
                deserves to know why. */}
            <div className="fgroup">
              <Field
                label="Years in a job, at least"
                htmlFor="office_yoe"
                help="Their own figure, not something we checked. Leaving it blank asks nothing; setting it also drops anybody who has not said."
              >
                <input
                  className="input"
                  id="office_yoe"
                  type="number"
                  min={0}
                  max={80}
                  value={draft.minOfficeYoe ?? ""}
                  onChange={(event) =>
                    setDraft({
                      ...draft,
                      minOfficeYoe: event.target.value ? Number(event.target.value) : undefined,
                    })
                  }
                />
              </Field>
            </div>

            <div className="fgroup">
              <Field
                label="Years in open source, at least"
                htmlFor="oss_yoe"
                help="We check this one: it is dated from when their first pull request was written, not from when somebody got round to merging it. Anybody we could not verify is left out rather than let through."
              >
                <input
                  className="input"
                  id="oss_yoe"
                  type="number"
                  min={0}
                  max={80}
                  value={draft.minOssYoe ?? ""}
                  onChange={(event) =>
                    setDraft({
                      ...draft,
                      minOssYoe: event.target.value ? Number(event.target.value) : undefined,
                    })
                  }
                />
              </Field>
            </div>

            {/* The same list the contributor picked from (ADR-0018 §9). It
                has to be: a code typed here is compared against one stored
                there, and two vocabularies would match nothing while looking
                like an empty market. */}
            <div className="fgroup">
              <CountryList
                id="countries"
                label="Where they are"
                help="Any of them matches. Leave empty for anywhere."
                values={draft.countries ?? []}
                onChange={(countries) => setDraft({ ...draft, countries })}
              />
            </div>

            <div className="fgroup">
              <p className="eyebrow">What they would take</p>
              <p className="field__help">
                Any of these matches. Somebody is available for the shapes they ticked and for
                nothing else, so this composes with availability rather than replacing it.
              </p>
              {SHAPE_CHOICES.map(([value, label]) => (
                <Checkbox
                  key={value}
                  label={label}
                  checked={(draft.openTo ?? []).includes(value)}
                  onChange={(on) => {
                    const current = draft.openTo ?? [];
                    setDraft({
                      ...draft,
                      openTo: on ? [...current, value] : current.filter((s) => s !== value),
                    });
                  }}
                />
              ))}
            </div>

            <div className="fgroup">
              <Field label="Results per page" htmlFor="per_page">
                <select
                  className="select"
                  id="per_page"
                  value={String(draft.perPage ?? 20)}
                  onChange={(event) => setDraft({ ...draft, perPage: Number(event.target.value) })}
                >
                  {[10, 20, 50].map((v) => (
                    <option key={v} value={v}>
                      {v} per page
                    </option>
                  ))}
                </select>
              </Field>
            </div>
          </div>

          <div className="filters__foot">
            <Button variant="primary" size="sm" type="submit">
              <Icon name="search" size="sm" />
              Search
            </Button>
          </div>
        </form>

        <section aria-label="Results">
          <div className="results-head">
            <p className="results-count">
              <strong>{data?.total ?? 0}</strong>{" "}
              {(data?.total ?? 0) === 1 ? "contributor" : "contributors"}
              {data?.inactive_hidden ? (
                <>
                  {" · "}
                  <span className="muted">{data.inactive_hidden} hidden</span>
                </>
              ) : null}
            </p>
            <span className="ranked-by">
              Ranked by{" "}
              <code className="mono">{format.rankedBy(data?.ranked_by ?? "overall")}</code>
            </span>
          </div>

          {results.loading ? <Loading what="results" /> : null}
          {results.error && !gated(results.error.code) ? (
            <Failure message={results.error.message} onRetry={results.reload} />
          ) : null}

          {/* Said plainly rather than left to be noticed. A list that shrank
              with no account of why makes a hirer doubt the filter instead of
              reading the result — the same reason the lapsed-availability
              banner below exists. */}
          {data?.already_shortlisted ? (
            <Banner>
              <b>
                {data.already_shortlisted}{" "}
                {data.already_shortlisted === 1 ? "person is" : "people are"} not shown because they
                are already on a round for this role.
              </b>{" "}
              They keep their place in the global ranking, so the numbers below skip theirs.
            </Banner>
          ) : null}

          {data && data.inactive_hidden > 0 ? (
            <Banner>
              <b>
                {data.inactive_hidden}{" "}
                {data.inactive_hidden === 1 ? "contributor is" : "contributors are"} hidden because
                their availability lapsed.
              </b>{" "}
              They keep their place in the global ranking, so the numbers below skip theirs.{" "}
              <Button
                variant="quiet"
                size="sm"
                onClick={() => {
                  setDraft({ ...draft, includeInactive: true });
                  apply({ ...draft, includeInactive: true, page: 1 });
                }}
              >
                Show them
              </Button>
            </Banner>
          ) : null}

          {data && applied.q?.trim() ? (
            <Banner tone="info" icon="search">
              Name lookup: quiet profiles are included, because you are looking for somebody you
              already know exists.
            </Banner>
          ) : null}

          {data && openRounds.length > 0 ? (
            <Card>
              <Field
                label="Shortlist into"
                htmlFor="target"
                help="Staging tells nobody. Confirming does, and that cannot be undone."
              >
                <select
                  className="select"
                  id="target"
                  value={target}
                  onChange={(event) => setTarget(event.target.value)}
                >
                  <option value="">— choose a round —</option>
                  {openRounds.map((round) => (
                    <option key={round.id} value={round.id}>
                      {round.name}
                    </option>
                  ))}
                </select>
              </Field>
              {stage.error ? <Failure message={stage.error.message} /> : null}
            </Card>
          ) : null}

          {data && data.results.length > 0 ? (
            <ul className="rows">
              {data.results.map((result) => (
                <Row
                  key={result.id}
                  result={result}
                  rankedBy={format.rankedBy(data.ranked_by)}
                  staged={staged.includes(result.id)}
                  staging={stage.pending}
                  onStage={(userId) => {
                    if (!target) return;
                    void stage.run(userId).then((ok) => {
                      if (ok !== null) setStaged([...staged, userId]);
                    });
                  }}
                />
              ))}
            </ul>
          ) : null}

          {data && data.results.length === 0 ? (
            <Empty
              title="Nobody clears every filter"
              actions={
                <>
                  <Button
                    variant="ghost"
                    size="sm"
                    onClick={() => {
                      const loosened = {
                        ...draft,
                        minSkillScore: undefined,
                        minOverallScore: undefined,
                        minGeneralistScore: undefined,
                      };
                      setDraft(loosened);
                      apply({ ...loosened, page: 1 });
                    }}
                  >
                    Loosen the score thresholds
                  </Button>
                  <Button variant="ghost" size="sm" onClick={reset}>
                    Reset all filters
                  </Button>
                </>
              }
            >
              {describe(applied) === "everyone"
                ? "No contributor in the active rubric version matches."
                : `You are asking for ${describe(applied)}. Skills combine with AND, so each extra one narrows hard.`}
            </Empty>
          ) : null}

          {pages > 1 ? (
            <nav className="pager" aria-label="Pagination">
              <p className="small muted">
                Page {page} of {pages}
              </p>
              <div className="pager__pages">
                {Array.from({ length: pages }, (_, i) => i + 1).map((n) => (
                  <button
                    key={n}
                    type="button"
                    aria-current={n === page ? "page" : undefined}
                    onClick={() => apply({ ...applied, page: n })}
                  >
                    {n}
                  </button>
                ))}
              </div>
            </nav>
          ) : null}

          {data && !isEmpty(applied) ? (
            <Card>
              <p className="eyebrow">Save this search</p>
              <p className="small muted">
                A saved search stores the filters, not the results — replaying it later ranks the
                population as it is then.
              </p>
              <Field label="Name" htmlFor="savename">
                <input
                  className="input"
                  id="savename"
                  value={saveName}
                  onChange={(event) => setSaveName(event.target.value)}
                />
              </Field>
              {save.error ? <Failure message={save.error.message} /> : null}
              <Button
                variant="ghost"
                size="sm"
                disabled={save.pending || saveName.trim().length === 0}
                onClick={() =>
                  void save.run(saveName, applied).then(() => navigate("/saved-searches"))
                }
              >
                <Icon name="bookmark" size="sm" />
                Save this search
              </Button>
            </Card>
          ) : null}
        </section>
      </div>
    </div>
  );
}

function NumberField({
  id,
  label,
  help,
  max,
  value,
  onChange,
}: {
  id: string;
  label: string;
  help: string;
  max: number;
  value: number | undefined;
  onChange: (value: number | undefined) => void;
}) {
  return (
    <div className="field">
      <div className="range-head">
        <label className="field__label" htmlFor={id}>
          {label}
        </label>
        <span className="range-value mono">{value && value > 0 ? value : "any"}</span>
      </div>
      <input
        className="range"
        type="range"
        id={id}
        min={0}
        max={max}
        step={1}
        value={value ?? 0}
        onChange={(event) => {
          const next = Number(event.target.value);
          onChange(next > 0 ? next : undefined);
        }}
      />
      <p className="field__help">{help}</p>
    </div>
  );
}
