// The country picker, in one place.
//
// The list comes from a third party behind port.PlaceService (ADR-0018 §9),
// because a country code is a CLOSED VOCABULARY two systems have to agree on:
// a contributor's `current_country` is matched against a role's eligible
// countries and against a hirer's search, so "UK" and "GB" being two strings
// for one country is a bug rather than a cosmetic difference.
//
// IT FAILS OPEN. The endpoint answers 200 with an empty list and
// `degraded: true` when the provider is down, so an empty list is not "there
// are no countries" — it is "the picker could not be filled". Every caller
// then has to let somebody type a code instead, which is exactly the rule that
// gets implemented differently in each place if it lives in each place. It
// lives here.

import { endpoints } from "../api/endpoints";
import type { CountriesResponse } from "../contract";
import { useClient } from "../hooks/session";
import { useAsync } from "../hooks/useAsync";
import { Field } from "./primitives";

/** The list itself. Public: the onboarding form needs it and has no account. */
export function useCountries() {
  const client = useClient();
  return useAsync<CountriesResponse>(
    (signal) => client.get<CountriesResponse>(endpoints.public.countries(), signal),
    [client],
  );
}

/**
 * Whether the provider answered. False means: let them type.
 *
 * `countries` is read defensively rather than trusted. This whole component
 * exists because the list can fail, and a body that arrived without the array
 * at all — a proxy error page, an older server, a stubbed response — is one
 * more way for it to fail. Taking `.length` off it crashed the search page
 * into its error boundary, which is a worse outcome than the outage it was
 * written to survive.
 */
function usable(data: CountriesResponse | undefined, error: unknown): boolean {
  return Boolean(data && !data.degraded && (data.countries?.length ?? 0) > 0 && !error);
}

/** One country, or none. */
export function CountryPicker({
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
  const countries = useCountries();
  const ok = usable(countries.data, countries.error);

  return (
    <Field
      label={label}
      htmlFor={id}
      help={ok ? help : "The country list could not be loaded. Type a two-letter code, such as GB."}
    >
      {ok ? (
        <select
          className="input"
          id={id}
          value={value}
          onChange={(event) => onChange(event.target.value)}
        >
          <option value="">Prefer not to say</option>
          {(countries.data?.countries ?? []).map((c) => (
            <option key={c.code} value={c.code}>
              {c.name}
            </option>
          ))}
        </select>
      ) : (
        <input
          className="input"
          id={id}
          maxLength={2}
          value={value}
          onChange={(event) => onChange(event.target.value.toUpperCase())}
        />
      )}
    </Field>
  );
}

/**
 * Several countries, as an OR.
 *
 * Picking from the dropdown ADDS one; the chosen ones sit below as removable
 * chips. A native multi-select over 250 options is unusable with a mouse and
 * worse with a keyboard, and the chips also show what is currently being asked
 * without the reader scrolling a list to find the highlighted rows.
 */
export function CountryList({
  id,
  label,
  help,
  values,
  onChange,
}: {
  id: string;
  label: string;
  help?: string;
  values: string[];
  onChange: (codes: string[]) => void;
}) {
  const countries = useCountries();
  const ok = usable(countries.data, countries.error);
  const all = countries.data?.countries ?? [];

  const nameOf = (code: string) => all.find((c) => c.code === code)?.name ?? code;
  const remaining = all.filter((c) => !values.includes(c.code));

  return (
    <>
      <Field
        label={label}
        htmlFor={id}
        help={
          ok
            ? help
            : "The country list could not be loaded. Type two-letter codes separated by commas, such as GB, DE."
        }
      >
        {ok ? (
          <select
            className="input"
            id={id}
            value=""
            onChange={(event) => {
              if (event.target.value) onChange([...values, event.target.value]);
            }}
          >
            <option value="">Add a country…</option>
            {remaining.map((c) => (
              <option key={c.code} value={c.code}>
                {c.name}
              </option>
            ))}
          </select>
        ) : (
          <input
            className="input"
            id={id}
            placeholder="GB, DE, IN"
            value={values.join(", ")}
            onChange={(event) =>
              onChange(
                event.target.value
                  .split(",")
                  .map((c) => c.trim().toUpperCase())
                  .filter(Boolean),
              )
            }
          />
        )}
      </Field>

      {ok && values.length > 0 ? (
        <div className="od-row" style={{ ["--od-gap" as string]: "6px", flexWrap: "wrap" }}>
          {values.map((code) => (
            <button
              key={code}
              type="button"
              className="btn btn--sm"
              onClick={() => onChange(values.filter((c) => c !== code))}
              aria-label={`Remove ${nameOf(code)}`}
            >
              {nameOf(code)} ×
            </button>
          ))}
        </div>
      ) : null}
    </>
  );
}
