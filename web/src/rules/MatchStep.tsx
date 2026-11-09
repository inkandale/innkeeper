import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { api, must } from "../api/client";
import { savedSearchesQuery, searchQuery } from "../api/queries";
import { Facets } from "../cards/Facets";
import { TermChips, useFilterInputs } from "../cards/filters";
import { filterFor, parseQuery, toSearch, type CardQuery } from "../cards/query";
import "../cards/Cards.css";
import { count } from "../shell/count";

type Picked = { id: number; name: string };
type Props = { library: number; picked?: number; onPick: (s: Picked) => void; onSaved: (s: Picked) => void; onNext: () => void };

/** MatchStep picks the rule's saved search, or builds one with the Cards page's filters and saves it. */
export function MatchStep({ library, picked, onPick, onSaved, onNext }: Props) {
  const searches = useQuery(savedSearchesQuery(library));
  const [chosen, setChosen] = useState<"build" | "saved">();
  if (searches.error) return <p className="error">{searches.error.message}</p>;
  if (!searches.data) return null;
  const mode = chosen ?? (searches.data.length ? "saved" : "build");
  return (
    <div className="rule-match">
      <fieldset className="row">
        <legend>How to match</legend>
        <label className="check">
          <input type="radio" name="match" checked={mode === "build"} onChange={() => setChosen("build")} /> Build a search
        </label>
        <label className="check">
          <input type="radio" name="match" checked={mode === "saved"} onChange={() => setChosen("saved")} /> Use a saved search
        </label>
      </fieldset>
      {mode === "build" ? (
        <Build library={library} onSaved={onSaved} />
      ) : (
        <fieldset>
          <legend>Saved search</legend>
          {searches.data.length === 0 && <p className="muted">No saved searches yet.</p>}
          {searches.data.map((s) => (
            <label key={s.id} className="check">
              <input type="radio" name="saved-search" checked={picked === s.id} onChange={() => onPick({ id: s.id, name: s.name })} /> {s.name}
            </label>
          ))}
          <button className="button primary" disabled={!picked} onClick={onNext}>
            Next
          </button>
        </fieldset>
      )}
    </div>
  );
}

/** Build is the Cards page's filters on local state, saved as a search under the name given. */
function Build({ library, onSaved }: { library: number; onSaved: (s: Picked) => void }) {
  const qc = useQueryClient();
  const [q, setQ] = useState<CardQuery>(() => parseQuery(new URLSearchParams(), library));
  const [name, setName] = useState("");
  const inputs = useFilterInputs(q);
  const filter = filterFor(q.terms, q.sharesSource, q.all);
  const matches = useQuery({ ...searchQuery("card", { ...toSearch(q), page: { size: 1, number: 1 } }), enabled: filter !== undefined });
  const save = useMutation({
    mutationFn: async () => must(await api.POST("/saved-searches", { body: { name: name.trim(), filter: filter!, sort: "" } })),
    onSuccess: (s) => {
      void qc.invalidateQueries({ queryKey: ["saved-searches"] });
      onSaved({ id: s.id, name: s.name });
    },
    meta: { toast: false },
  });
  const submit = (e: FormEvent) => {
    e.preventDefault();
    save.mutate();
  };
  return (
    <div className="rule-build">
      <Facets kind="card" q={q} names={inputs.names} browse={inputs.browse} onChange={setQ} />
      <form className="rule-form" onSubmit={submit}>
        <div className="chips">
          <TermChips q={q} names={inputs.names} onChange={setQ} />
        </div>
        <p role="status">
          {!filter
            ? "Pick filters for the cards the rule acts on."
            : matches.error
              ? matches.error.message
              : matches.data
                ? `${count(matches.data.total, "card")} match in this library.`
                : "Counting cards…"}
        </p>
        <label>
          Search name <input className="input" value={name} required onChange={(e) => setName(e.target.value)} />
        </label>
        {save.error && <p className="error">{save.error.message}</p>}
        <button className="button primary" disabled={!filter || !name.trim() || save.isPending}>
          Save and continue
        </button>
      </form>
    </div>
  );
}
