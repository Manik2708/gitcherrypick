// Choosing a skill, on either side of the platform.
//
// Skills are a catalogue, not free text: standing is derived per skill, and a
// typed slug that is off by a character silently matches nothing. The hirer
// side used to take "skill slugs, comma separated" in a text box, which put the
// burden of knowing the catalogue's exact spelling on the person least likely
// to know it, and failed quietly rather than saying the word was not a skill.
//
// The matches are a dropdown over the page rather than a list inside it. An
// inline list pushes everything below it down on every keystroke, which moves
// the thing you are aiming at while you type — a menu that overlays cannot.
//
// The search endpoint resolves aliases, so "postgresql" finds `postgres` and
// the row says which name the catalogue uses.

import { useEffect, useRef, useState } from "react";
import { useSkillSearch } from "../hooks/contributor";
import { Field, Icon } from "./primitives";

export function SkillPicker({
  chosen,
  onChange,
  label = "Add a skill",
  help,
  id = "skill-picker",
  disabled = false,
}: {
  chosen: string[];
  onChange: (next: string[]) => void;
  label?: string;
  help?: React.ReactNode;
  id?: string;
  disabled?: boolean;
}) {
  const [query, setQuery] = useState("");
  const [open, setOpen] = useState(false);
  const [active, setActive] = useState(0);
  const box = useRef<HTMLDivElement>(null);
  const matches = useSkillSearch(query);

  const results = (query.trim() && matches.data ? matches.data.results : []).filter(
    (m) => !chosen.includes(m.slug),
  );

  // A click anywhere else closes it, the way a menu should.
  useEffect(() => {
    if (!open) return;
    const away = (e: MouseEvent) => {
      if (!box.current?.contains(e.target as Node)) setOpen(false);
    };
    document.addEventListener("mousedown", away);
    return () => document.removeEventListener("mousedown", away);
  }, [open]);

  const add = (slug: string) => {
    if (!chosen.includes(slug)) onChange([...chosen, slug]);
    setQuery("");
    setOpen(false);
    setActive(0);
  };

  const onKey = (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (e.key === "Escape") return setOpen(false);
    if (!results.length) return;
    if (e.key === "ArrowDown") {
      e.preventDefault();
      setOpen(true);
      setActive((i) => (i + 1) % results.length);
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      setActive((i) => (i - 1 + results.length) % results.length);
    } else if (e.key === "Enter") {
      e.preventDefault();
      const pick = results[active] ?? results[0];
      if (pick) add(pick.slug);
    }
  };

  const listId = `${id}-listbox`;
  const showing = open && query.trim().length > 0;

  return (
    <div className="skillpick" ref={box}>
      <Field label={label} htmlFor={id} help={help}>
        <div className="skillpick__control">
          <input
            className="input"
            id={id}
            role="combobox"
            aria-expanded={showing}
            aria-controls={listId}
            aria-autocomplete="list"
            autoComplete="off"
            placeholder="Search the catalogue"
            value={query}
            disabled={disabled}
            onChange={(e) => {
              setQuery(e.target.value);
              setOpen(true);
              setActive(0);
            }}
            onFocus={() => setOpen(true)}
            onKeyDown={onKey}
          />

          {showing ? (
            <ul className="pick pick--menu" id={listId} role="listbox">
              {results.length > 0 ? (
                results.map((m, i) => (
                  <li key={m.slug} role="option" aria-selected={i === active}>
                    <button
                      type="button"
                      className={`pick__row${i === active ? " is-active" : ""}`}
                      // mousedown, not click: the input's blur would otherwise
                      // close the menu before the click ever lands.
                      onMouseDown={(e) => {
                        e.preventDefault();
                        add(m.slug);
                      }}
                      onMouseEnter={() => setActive(i)}
                    >
                      <span className="pick__name">{m.name}</span>
                      {m.matched_via === "alias" ? (
                        <span className="pick__meta">
                          matched “{m.matched_alias}” — catalogued as{" "}
                          <span className="mono">{m.slug}</span>
                        </span>
                      ) : null}
                    </button>
                  </li>
                ))
              ) : (
                <li className="pick__none">Nothing in the catalogue matches that.</li>
              )}
            </ul>
          ) : null}
        </div>
      </Field>

      {chosen.length > 0 ? (
        <ul className="skillpick__chosen">
          {chosen.map((slug) => (
            <li key={slug}>
              <span className="mono">{slug}</span>
              <button
                type="button"
                className="skillpick__drop"
                aria-label={`Remove ${slug}`}
                onClick={() => onChange(chosen.filter((s) => s !== slug))}
              >
                <Icon name="slash" size="sm" />
              </button>
            </li>
          ))}
        </ul>
      ) : null}
    </div>
  );
}
