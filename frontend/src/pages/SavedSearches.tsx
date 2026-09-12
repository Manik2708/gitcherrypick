// Filter sets a hirer keeps.
//
// A saved search stores the FILTERS verbatim, not the results (ADR-0008 §5).
// Replaying one ranks the population as it is now — which is the point: the
// same question asked next month has a different answer, and a stored result set
// would quietly stop being true.

import { useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import {
  describe,
  fromSaved,
  useDeleteSavedSearch,
  useSavedSearchResults,
  useSavedSearches,
} from "../hooks/hirer";
import * as format from "../logic/format";
import {
  Avatar,
  Button,
  Card,
  Empty,
  Failure,
  Icon,
  Loading,
  PageHead,
} from "../ui/primitives";

export function SavedSearchesPage() {
  const saved = useSavedSearches();
  const remove = useDeleteSavedSearch();
  const navigate = useNavigate();
  const [replaying, setReplaying] = useState<string | undefined>(undefined);
  const replayed = useSavedSearchResults(replaying);

  if (saved.loading) return <div className="page"><Loading what="your saved searches" /></div>;
  if (saved.error) {
    return (
      <div className="page page--narrow">
        <Failure message={saved.error.message} onRetry={saved.reload} />
      </div>
    );
  }

  const searches = saved.data?.saved_searches ?? [];

  return (
    <div className="page">
      <PageHead
        eyebrow="Saved"
        title="Saved searches"
        lede="These store the question, not the answer. Replaying one ranks the pool as it stands today."
      />

      {remove.error ? <Failure message={remove.error.message} /> : null}

      {searches.length === 0 ? (
        <Empty title="Nothing saved yet" icon="list">
          Run a search and save the filters from there.
        </Empty>
      ) : (
        <Card>
          <ul className="rows">
            {searches.map((search) => {
              const filters = fromSaved(search.filters);
              return (
                <li className="row" key={search.id}>
                  <div className="row__mid">
                    <div className="row__ident">
                      <span className="row__name">{search.name}</span>
                    </div>
                    <p className="row__headline mono small">{describe(filters)}</p>
                    <div className="row__meta">
                      <span>Saved {format.date(search.created_at)}</span>
                    </div>
                  </div>
                  <div className="row__right">
                    <div className="row__actions">
                      <Button variant="primary" size="sm" onClick={() => setReplaying(search.id)}>
                        <Icon name="replay" size="sm" />
                        Replay
                      </Button>
                      <Button
                        variant="quiet"
                        size="sm"
                        disabled={remove.pending}
                        onClick={() => void remove.run(search.id).then(() => saved.reload())}
                      >
                        <Icon name="trash" size="sm" />
                        Delete
                      </Button>
                    </div>
                  </div>
                </li>
              );
            })}
          </ul>
        </Card>
      )}

      {replaying ? (
        <Card>
          <div className="section__head">
            <h2 className="section__title">Replayed</h2>
            <span className="section__note">
              Run by the server against the stored filter set, so a filter this client does not
              know about still takes effect.
            </span>
          </div>

          {replayed.loading ? <Loading what="results" /> : null}
          {replayed.error ? <Failure message={replayed.error.message} /> : null}

          {replayed.data ? (
            replayed.data.results.length === 0 ? (
              <Empty title="Nobody matches it today">
                The question is unchanged; the population moved.
              </Empty>
            ) : (
              <ul className="rows">
                {replayed.data.results.map((result) => (
                  <li className="row" key={result.id}>
                    <div className="rank">
                      <span className="rank__n mono">#{result.rank}</span>
                    </div>
                    <Avatar name={result.display_name} size="sm" tint={(result.rank % 6) + 1} />
                    <div className="row__mid">
                      <div className="row__ident">
                        <Link className="row__name" to={`/contributors/${result.id}`}>
                          {result.display_name}
                        </Link>
                        <span className="row__login mono">@{result.github_login}</span>
                      </div>
                    </div>
                    <div className="row__right">
                      <div className="row__scores">
                        <span className="stat">
                          <span className="stat__n mono">{format.n1(result.overall_score)}</span>
                          <span className="stat__label">Overall</span>
                        </span>
                      </div>
                      <Button variant="ghost" size="sm" onClick={() => navigate(`/contributors/${result.id}`)}>
                        Scorecard
                      </Button>
                    </div>
                  </li>
                ))}
              </ul>
            )
          ) : null}
        </Card>
      ) : null}
    </div>
  );
}
