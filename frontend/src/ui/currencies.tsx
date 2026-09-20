// The currency picker, in one place.
//
// The list comes from a third party behind port.MoneyService (ADR-0018
// amendment 3), for exactly the reason the country list does: an ISO 4217 code
// is a CLOSED VOCABULARY two systems have to agree on. Every amount here is
// minor units plus a code, and an amount is only ever compared against another
// amount in the same code — a role's salary against what a contributor expects,
// in search and in the public-opening gate. "usd", "US$" and "dollars" are
// three salaries that match nothing, and the form that accepted them said so
// to nobody.
//
// IT FAILS OPEN, like the country picker: the endpoint answers 200 with an
// empty list and `degraded: true` when the provider is down, so an empty list
// means "the picker could not be filled" and the caller must let somebody type
// a code. That rule lives here rather than in each form that needs it.

import { endpoints } from "../api/endpoints";
import type { CurrenciesResponse } from "../contract";
import { useClient } from "../hooks/session";
import { useAsync } from "../hooks/useAsync";
import { Field } from "./primitives";

/** The list itself. Public: a profile form asks before anybody signs in to it. */
export function useCurrencies() {
  const client = useClient();
  return useAsync<CurrenciesResponse>(
    (signal) => client.get<CurrenciesResponse>(endpoints.public.currencies(), signal),
    [client],
  );
}

/** Whether the provider answered. False means: let them type. */
function usable(data: CurrenciesResponse | undefined, error: unknown): boolean {
  return Boolean(data && !data.degraded && (data.currencies?.length ?? 0) > 0 && !error);
}

/**
 * One currency, or none.
 *
 * The code is shown beside the name — `GBP · Pound sterling` — because the code
 * is what is stored, what a salary is compared on, and what somebody reading
 * the role afterwards sees. A picker that showed only the name would be hiding
 * the value it writes.
 */
export function CurrencyPicker({
  id,
  label,
  help,
  value,
  onChange,
}: {
  id: string;
  label: string;
  help?: string;
  value: string;
  onChange: (code: string) => void;
}) {
  const currencies = useCurrencies();
  const ok = usable(currencies.data, currencies.error);
  const all = currencies.data?.currencies ?? [];

  // A code that is already saved but is not in the provider's list still has
  // to be selectable, or opening the form would silently blank it. This is not
  // hypothetical: the stand-in serves fifteen currencies and there are about
  // 180, so any real list is a superset of what was typed before the picker
  // existed.
  const missing = ok && value !== "" && !all.some((c) => c.code === value);

  return (
    <Field
      label={label}
      htmlFor={id}
      help={
        ok ? help : "The currency list could not be loaded. Type a three-letter code, such as GBP."
      }
    >
      {ok ? (
        <select
          className="input"
          id={id}
          value={value}
          onChange={(event) => onChange(event.target.value)}
        >
          <option value="">Not stated</option>
          {missing ? <option value={value}>{value}</option> : null}
          {all.map((c) => (
            <option key={c.code} value={c.code}>
              {c.code} · {c.name}
            </option>
          ))}
        </select>
      ) : (
        <input
          className="input"
          id={id}
          maxLength={3}
          value={value}
          onChange={(event) => onChange(event.target.value.toUpperCase())}
        />
      )}
    </Field>
  );
}
