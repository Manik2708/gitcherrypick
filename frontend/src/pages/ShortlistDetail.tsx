// One round, and the irreversible button.
//
// Confirm is the sharpest action on the platform: it tells every unnotified
// candidate that an organisation is interested, and nothing the platform offers
// takes that back. So it sits behind an explicit acknowledgement that names how
// many people it reaches — not a modal that says "are you sure" — and a
// notified entry loses its Remove control entirely, because the API refuses
// that deletion anyway (ADR-0008 §3a).

import { useState } from "react";
import { useParams } from "react-router-dom";
import type { ShortlistEntry } from "../contract";
import {
  useCloseShortlist,
  useConfirmShortlist,
  useRemoveEntry,
  useShortlist,
} from "../hooks/hirer";
import * as format from "../logic/format";
import {
  Avatar,
  Banner,
  Button,
  By,
  Card,
  Checkbox,
  Empty,
  Failure,
  Icon,
  Loading,
  Notice,
  PageHead,
  Pill,
} from "../ui/primitives";

/** Removal is legal only while nobody has been told. */
function removable(entry: ShortlistEntry): boolean {
  return !entry.notified_at && !entry.contact_status;
}

function EntryRow({
  entry,
  onRemove,
  removing,
}: {
  entry: ShortlistEntry;
  onRemove: (userId: string) => void;
  removing: boolean;
}) {
  return (
    <li className="row">
      <Avatar name={entry.user.display_name} size="sm" tint={3} />
      <div className="row__mid">
        <div className="row__ident">
          <span className="row__name">{entry.user.display_name}</span>
          <span className="row__login mono">@{entry.user.github_login}</span>
        </div>
        {entry.email ? (
          <p className="row__meta mono">{entry.email} — released when they accepted.</p>
        ) : null}
        {entry.added_by ? (
          <p className="row__meta">
            <By who={entry.added_by} verb="Staged by" />
          </p>
        ) : null}
      </div>
      <div className="row__right">
        {entry.contact_status ? (
          <Pill tone={entry.contact_status === "accepted" ? "primary" : "stale"}>
            {entry.contact_status}
          </Pill>
        ) : (
          <Pill tone="secondary">Not told yet</Pill>
        )}
        <div className="row__actions">
          {removable(entry) ? (
            <Button
              variant="quiet"
              size="sm"
              disabled={removing}
              onClick={() => onRemove(entry.user.id)}
            >
              Remove
            </Button>
          ) : (
            <span className="small muted">Permanent — they have been told.</span>
          )}
        </div>
      </div>
    </li>
  );
}

export function ShortlistDetailPage() {
  const { shortlistId } = useParams<{ shortlistId: string }>();
  const round = useShortlist(shortlistId);

  const remove = useRemoveEntry(shortlistId ?? "");
  const confirm = useConfirmShortlist(shortlistId ?? "");
  const close = useCloseShortlist(shortlistId ?? "");

  const [acknowledged, setAcknowledged] = useState(false);

  if (round.loading)
    return (
      <div className="page">
        <Loading what="the round" />
      </div>
    );
  if (round.error) {
    return (
      <div className="page page--narrow">
        <Failure message={round.error.message} onRetry={round.reload} />
      </div>
    );
  }
  if (!round.data) {
    return (
      <div className="page page--narrow">
        <Empty title="No such round" />
      </div>
    );
  }

  const data = round.data;
  const pending = data.entries.filter((e) => !e.notified_at && !e.contact_status);
  const told = data.entries.filter((e) => e.notified_at || e.contact_status);
  const failure = remove.error ?? confirm.error ?? close.error;

  function reload() {
    round.reload();
    setAcknowledged(false);
  }

  return (
    <div className="page">
      <PageHead
        eyebrow="Round"
        title={data.name}
        lede={
          <>
            Result by {format.date(data.tentative_result_date)} ·{" "}
            <span className="mono">{data.status}</span>
            {data.created_by ? (
              <>
                {" · "}
                <By who={data.created_by} verb="opened by" />
              </>
            ) : null}
          </>
        }
      />

      {failure ? <Failure message={failure.message} /> : null}

      <Card>
        <div className="section__head">
          <h2 className="section__title">Candidates</h2>
          <span className="section__note mono">
            {data.entries.length} staged · {pending.length} not told
          </span>
        </div>

        {data.entries.length === 0 ? (
          <Empty title="Nobody staged yet" icon="bookmark">
            Add someone from search. Staging tells them nothing.
          </Empty>
        ) : (
          <ul className="rows">
            {data.entries.map((entry) => (
              <EntryRow
                key={entry.user.id}
                entry={entry}
                removing={remove.pending}
                onRemove={(id) => void remove.run(id).then(reload)}
              />
            ))}
          </ul>
        )}
      </Card>

      {data.status !== "closed" ? (
        <Card>
          <div className="section__head">
            <h2 className="section__title">Confirm — this tells people</h2>
          </div>

          {pending.length === 0 ? (
            <Banner>
              Everyone on this round has already been told. Confirming again sends nothing.
            </Banner>
          ) : (
            <>
              <Notice title="This cannot be undone">
                <p>
                  This tells {pending.length} {pending.length === 1 ? "person" : "people"} that your
                  organisation is interested. It cannot be undone, and they can never be removed
                  from the round afterwards.
                </p>
                {told.length > 0 ? (
                  <p>
                    {told.length} already told. Confirming again reaches only the {pending.length}{" "}
                    who have not been.
                  </p>
                ) : null}
                {data.organization && !data.organization.verified ? (
                  <p>
                    Your organisation has not verified payment capability, and each candidate is
                    told so when they decide whether to release an address.
                  </p>
                ) : null}
              </Notice>

              <Checkbox
                label="I understand this cannot be undone"
                checked={acknowledged}
                onChange={setAcknowledged}
              />

              <Button
                variant="danger"
                disabled={!acknowledged || confirm.pending}
                onClick={() => void confirm.run().then(reload)}
              >
                <Icon name="mail" size="sm" />
                Tell {pending.length} {pending.length === 1 ? "person" : "people"}
              </Button>
            </>
          )}
        </Card>
      ) : null}

      {data.status !== "closed" ? (
        <Card>
          <div className="section__head">
            <h2 className="section__title">Close this round</h2>
            <span className="section__note">
              Closing stops it accepting anyone new. The record of who was told stays.
            </span>
          </div>
          <Button
            variant="ghost"
            disabled={close.pending}
            onClick={() => void close.run().then(reload)}
          >
            Close
          </Button>
        </Card>
      ) : null}
    </div>
  );
}
