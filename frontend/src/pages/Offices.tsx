// Where an organisation has offices, or that it has none (ADR-0024).
//
// Every seat reads this page; only owners change it (decision 8). An office
// organisation lists its addresses, adds more unreviewed, removes any but the
// main office, and may go fully remote. A fully remote one has no addresses
// and asks an administrator for its first — the request's state, and a
// rejection's reason, are shown to every seat (amendment 1 §1.3).

import { useState } from "react";
import { Link } from "react-router-dom";
import type { AddressParts, AddressesResponse, BlockingRole, OrgAddress } from "../contract";
import type { ApiError } from "../api/http";
import {
  useAddAddress,
  useAddresses,
  useGoFullyRemote,
  useOrgID,
  useRemoveAddress,
  useRequestOffice,
  useSeatRole,
} from "../hooks/hirer";
import * as format from "../logic/format";
import { addressLine } from "../logic/roleLocations";
import { CountryPicker, useCountryNames } from "../ui/countries";
import {
  Banner,
  Button,
  Card,
  Checkbox,
  Empty,
  Failure,
  Field,
  Loading,
  PageHead,
  Pill,
  SectionTitle,
} from "../ui/primitives";

const BLANK: AddressParts = { country: "", city: "", postal_code: "", street1: "", street2: "" };

export function OfficesPage() {
  const org = useOrgID();
  const addresses = useAddresses(org.id);
  const role = useSeatRole(org.id);
  const owner = role === "owner";

  if (org.loading || (addresses.loading && !addresses.data)) {
    return (
      <div className="page">
        <PageHead eyebrow="Organisation" title="Offices" />
        <Loading what="your offices" />
      </div>
    );
  }

  if (!org.id) {
    return (
      <div className="page">
        <PageHead eyebrow="Organisation" title="Offices" />
        <Empty title="No organisation" icon="people">
          An independent hirer has no offices.
        </Empty>
      </div>
    );
  }

  const data = addresses.data;

  return (
    <div className="page">
      <PageHead eyebrow="Organisation" title="Offices" />

      {addresses.error ? (
        <Failure message={addresses.error.message} onRetry={addresses.reload} />
      ) : null}

      {data?.workplace === "office" ? (
        <>
          <AddressList
            orgId={org.id}
            addresses={data.addresses}
            owner={owner}
            onChanged={addresses.reload}
          />
          {owner ? <AddAddress orgId={org.id} onAdded={addresses.reload} /> : null}
          {owner ? <GoFullyRemote orgId={org.id} onDone={addresses.reload} /> : null}
        </>
      ) : null}

      {data?.workplace === "fully_remote" ? (
        <FullyRemote
          orgId={org.id}
          request={data.office_request}
          owner={owner}
          onRequested={addresses.reload}
        />
      ) : null}
    </div>
  );
}

/* --- an office organisation ------------------------------------------------- */

function AddressList({
  orgId,
  addresses,
  owner,
  onChanged,
}: {
  orgId: string;
  addresses: OrgAddress[];
  owner: boolean;
  onChanged: () => void;
}) {
  const remove = useRemoveAddress(orgId);
  const nameOf = useCountryNames();
  // Which row the last refusal was about, so it is shown on that row.
  const [removing, setRemoving] = useState<string | null>(null);

  return (
    <Card>
      <div className="section__head">
        <h2 className="section__title">Addresses</h2>
        <span className="section__note mono">{addresses.length}</span>
      </div>

      <div className="rows">
        {addresses.map((a) => (
          <div className="row" key={a.id}>
            <span className="row__ident">
              <span className="row__name">
                {a.city}, {nameOf(a.country)}
              </span>
              <span className="row__meta">
                {a.is_main_office ? <Pill tone="violet">Main office</Pill> : null}
                <span>{addressLine(a)}</span>
              </span>
              {removing === a.id && remove.error ? <Refusal error={remove.error} /> : null}
            </span>
            {owner && !a.is_main_office ? (
              <span className="row__right">
                <span className="row__actions">
                  <Button
                    variant="danger"
                    size="sm"
                    disabled={remove.pending}
                    onClick={() => {
                      setRemoving(a.id);
                      void remove.run(a.id).then((ok) => ok !== null && onChanged());
                    }}
                  >
                    Remove
                  </Button>
                </span>
              </span>
            ) : null}
          </div>
        ))}
      </div>
    </Card>
  );
}

function AddAddress({ orgId, onAdded }: { orgId: string; onAdded: () => void }) {
  const add = useAddAddress(orgId);
  const [address, setAddress] = useState<AddressParts>(BLANK);

  return (
    <Card>
      <SectionTitle>Add an office</SectionTitle>
      <AddressFields value={address} onChange={setAddress} error={add.error} idPrefix="add" />
      {add.error && !add.error.field ? <Failure message={add.error.message} /> : null}
      <Button
        variant="primary"
        disabled={add.pending}
        onClick={() =>
          void add.run(address).then((created) => {
            if (!created) return;
            setAddress(BLANK);
            onAdded();
          })
        }
      >
        {add.pending ? "Adding…" : "Add the office"}
      </Button>
    </Card>
  );
}

function GoFullyRemote({ orgId, onDone }: { orgId: string; onDone: () => void }) {
  const go = useGoFullyRemote(orgId);
  const [acknowledged, setAcknowledged] = useState(false);

  return (
    <Card>
      <SectionTitle>Go fully remote</SectionTitle>
      <Banner icon="warn">
        Every address is removed, the main office included. Getting an office back is reviewed by an
        administrator.
      </Banner>
      <Checkbox label="Remove every address" checked={acknowledged} onChange={setAcknowledged} />
      {go.error ? <Refusal error={go.error} /> : null}
      <Button
        variant="danger"
        disabled={!acknowledged || go.pending}
        onClick={() => void go.run().then((ok) => ok && onDone())}
      >
        {go.pending ? "Switching…" : "Go fully remote"}
      </Button>
    </Card>
  );
}

/* --- a fully remote organisation -------------------------------------------- */

function FullyRemote({
  orgId,
  request,
  owner,
  onRequested,
}: {
  orgId: string;
  request: AddressesResponse["office_request"];
  owner: boolean;
  onRequested: () => void;
}) {
  const ask = useRequestOffice(orgId);
  const [address, setAddress] = useState<AddressParts>(BLANK);
  const pending = request?.status === "pending";

  return (
    <>
      <Card>
        <SectionTitle>Fully remote</SectionTitle>
        <p className="field__help">No offices. Roles are remote only.</p>

        {request?.status === "pending" ? (
          <Banner icon="clock">
            Office request waiting for review since {format.date(request.requested_at)}:{" "}
            {addressLine(request.address)}.
          </Banner>
        ) : null}

        {request?.status === "rejected" ? (
          <Banner icon="warn">
            <p>
              Office request not approved
              {request.decided_at ? ` on ${format.date(request.decided_at)}` : ""}:{" "}
              {addressLine(request.address)}.
            </p>
            <p>
              <b>Reason:</b> {request.reason}
            </p>
          </Banner>
        ) : null}
      </Card>

      {owner && !pending ? (
        <Card>
          <SectionTitle>Ask for an office</SectionTitle>
          <p className="field__help">An administrator reviews it. It becomes your main office.</p>
          <AddressFields value={address} onChange={setAddress} error={ask.error} idPrefix="ask" />
          {ask.error && !ask.error.field ? <Failure message={ask.error.message} /> : null}
          <Button
            variant="primary"
            disabled={ask.pending}
            onClick={() =>
              void ask.run(address).then((created) => {
                if (!created) return;
                setAddress(BLANK);
                onRequested();
              })
            }
          >
            {ask.pending ? "Sending…" : "Send for review"}
          </Button>
        </Card>
      ) : null}
    </>
  );
}

/* --- shared ------------------------------------------------------------------ */

/**
 * The address body, as POST …/addresses and POST …/office-request take it.
 * `invalid_address` names a top-level field (amendment 1 §1.8).
 */
function AddressFields({
  value,
  onChange,
  error,
  idPrefix,
}: {
  value: AddressParts;
  onChange: (value: AddressParts) => void;
  error: ApiError | null;
  idPrefix: string;
}) {
  const refused = (field: string) =>
    error?.code === "invalid_address" && error.field === field ? error.message : null;
  const set = (key: keyof AddressParts, v: string) => onChange({ ...value, [key]: v });

  return (
    <>
      <CountryPicker
        id={`${idPrefix}-country`}
        label="Country"
        blank="Choose a country"
        value={value.country}
        onChange={(code) => set("country", code)}
        error={refused("country")}
      />
      <Field label="City" htmlFor={`${idPrefix}-city`} error={refused("city")}>
        <input
          className="input"
          id={`${idPrefix}-city`}
          value={value.city}
          onChange={(e) => set("city", e.target.value)}
        />
      </Field>
      <Field label="Street" htmlFor={`${idPrefix}-street1`} error={refused("street1")}>
        <input
          className="input"
          id={`${idPrefix}-street1`}
          value={value.street1}
          onChange={(e) => set("street1", e.target.value)}
        />
      </Field>
      <Field label="Street, continued" htmlFor={`${idPrefix}-street2`}>
        <input
          className="input"
          id={`${idPrefix}-street2`}
          value={value.street2}
          onChange={(e) => set("street2", e.target.value)}
        />
      </Field>
      <Field
        label="Postcode"
        htmlFor={`${idPrefix}-postal`}
        help="Leave blank if your country has none."
      >
        <input
          className="input"
          id={`${idPrefix}-postal`}
          value={value.postal_code}
          onChange={(e) => set("postal_code", e.target.value)}
        />
      </Field>
    </>
  );
}

/** A refusal, with the roles that block it where the server names them. */
function Refusal({ error }: { error: ApiError }) {
  const roles: BlockingRole[] = error.roles;
  return (
    <div className="notice" role="alert">
      <p className="notice__title">That did not work</p>
      <div className="notice__body">
        <p>{error.message}</p>
        {roles.length > 0 ? (
          <ul className="facts">
            {roles.map((r) => (
              <li className="fact" key={r.id}>
                <Pill tone={r.status === "open" ? "primary" : "secondary"}>{r.status}</Pill>
                <Link to="/roles">{r.title}</Link>
              </li>
            ))}
          </ul>
        ) : null}
      </div>
    </div>
  );
}
