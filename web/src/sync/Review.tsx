import { useMutation, useQueries, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Link, useLocation, useNavigate, useParams, useSearchParams } from "react-router";
import { api, ApiError, must, thumb } from "../api/client";
import type { components } from "../api/schema";
import { show } from "../card/draft";
import { entryOf } from "../card/History";
import { useToast } from "../shell/Toasts";
import {
  compareItem,
  decidable,
  optionKey,
  optionLabel,
  resolution,
  resolveItem,
  rows,
  snapshotOf,
  type Comparison,
  type Item,
  type ItemKind,
  type Option,
} from "./sync";
import "./Sync.css";

type Field = components["schemas"]["Field"];
type Hunk = components["schemas"]["Hunk"];
type Choices = Record<string, string>;

const sideText = (v: unknown) => (v === undefined ? <em className="muted">absent</em> : show(v));

function HunkChoice(props: { scope: string; hunk: Hunk; label: string; list: boolean; choice: string; onChoose: (c: string) => void }) {
  const { scope, hunk, label, list, choice, onChoose } = props;
  const sides = list ? ["library", "app", "both"] : ["library", "app"];
  return (
    <div className="hunk-pair">
      {label && <span className="muted">{label}</span>}
      <div>
        <h4>Library</h4>
        <p className="hunks">{sideText(hunk.library)}</p>
      </div>
      <div>
        <h4>App</h4>
        <p className="hunks">{sideText(hunk.app)}</p>
      </div>
      <span className="row" role="radiogroup" aria-label={`Keep for ${hunk.id}`}>
        {sides.map((s) => (
          <label key={s} className="check">
            <input type="radio" name={`${scope}/${hunk.id}`} checked={choice === s} onChange={() => onChoose(s)} />{" "}
            {s === "library" ? "Library" : s === "app" ? "App" : "Both"}
          </label>
        ))}
      </span>
    </div>
  );
}

function FieldChoices({ scope, field, choices, onChoose }: { scope: string; field: Field; choices: Choices; onChoose: (id: string, c: string) => void }) {
  const hunks = field.hunks ?? [];
  const differs = hunks.some((h) => h.status !== "same");
  const label = (h: Hunk) => {
    if (field.kind !== "entries") return "";
    const e = entryOf(h.id);
    return e.field ? `${e.entry} · ${e.field}` : e.entry;
  };
  return (
    <details className="field" open={differs}>
      <summary>
        {field.path}
        {!differs && <span className="muted"> · same</span>}
      </summary>
      {hunks.map((h) =>
        h.status === "same" ? (
          <p key={h.id} className="hunks same">
            {show(h.library)}
          </p>
        ) : decidable(h) ? (
          <HunkChoice
            key={h.id}
            scope={scope}
            hunk={h}
            label={label(h)}
            list={field.kind === "list"}
            choice={choices[h.id] ?? "library"}
            onChoose={(c) => onChoose(h.id, c)}
          />
        ) : (
          <p key={h.id} className="hunks">
            <span className="muted">{h.status === "auto_app" ? "App's change, merged: " : "Library's change, kept: "}</span>
            {show(h.status === "auto_app" ? h.app : h.library)}
          </p>
        ),
      )}
    </details>
  );
}

/** Diff lists a comparison field by field, with a choice on each hunk the owner must decide. */
function Diff({ scope, cmp, choices, onChoose }: { scope: string; cmp: Comparison; choices: Choices; onChoose: (id: string, c: string) => void }) {
  const fields = cmp.fields ?? [];
  const a = cmp.avatar;
  return (
    <>
      {a && (
        <div className="hunk-pair">
          <div>
            <h4>Library image</h4>
            {a.library ? <img className="history-thumb" src={thumb(a.library, 160)} alt="Library" /> : "None"}
          </div>
          <div>
            <h4>App image</h4>
            {a.app ? <img className="history-thumb" src={thumb(a.app, 160)} alt="App" /> : "None"}
          </div>
          <span className="row" role="radiogroup" aria-label="Keep for avatar">
            {(a.app ? ["library", "app"] : ["library"]).map((s) => (
              <label key={s} className="check">
                <input type="radio" name={`${scope}/avatar`} checked={(choices.avatar ?? "library") === s} onChange={() => onChoose("avatar", s)} />{" "}
                {s === "library" ? "Library" : "App"}
              </label>
            ))}
          </span>
        </div>
      )}
      {!a && fields.every((f) => (f.hunks ?? []).every((h) => h.status === "same")) && <p>No differences.</p>}
      {fields.map((f) => (
        <FieldChoices key={f.path} scope={scope} field={f} choices={choices} onChoose={onChoose} />
      ))}
    </>
  );
}

function OtherMatch({ kind, onPick }: { kind: ItemKind; onPick: (id: number) => void }) {
  const [text, setText] = useState("");
  const [q, setQ] = useState("");
  const hits = useQuery({
    enabled: !!q,
    queryKey: ["search", kind, "other-match", q],
    queryFn: async () => {
      const body = { text: q, page: { size: 10 } };
      return must(kind === "items" ? await api.POST("/search", { body }) : await api.POST("/lorebooks/search", { body }));
    },
  });
  return (
    <form
      className="row"
      role="search"
      onSubmit={(e) => {
        e.preventDefault();
        setQ(text.trim());
      }}
    >
      <input className="input" aria-label="Other match" placeholder="Other match…" value={text} onChange={(e) => setText(e.target.value)} />
      <button className="button">Search</button>
      {hits.data && !hits.data.hits?.length && <span className="muted">No matches</span>}
      {(hits.data?.hits ?? []).map((h) => (
        <button key={h.id} type="button" className="button" onClick={() => onPick(h.id)}>
          {String(h.fields?.name ?? "#" + h.id)}
        </button>
      ))}
    </form>
  );
}

/** Review is the diff editor: one app item against a library card, decided hunk by hunk, with the card's world books nested. */
export function Review() {
  const params = useParams();
  const instance = Number(params.instance);
  const kind: ItemKind = params.kind === "lorebook-items" ? "lorebook-items" : "items";
  const remoteId = params.remoteId ?? "";
  const [search, setSearch] = useSearchParams();
  const jobId = Number(search.get("job")) || 0;
  const navigate = useNavigate();
  const location = useLocation();
  const qc = useQueryClient();
  const toast = useToast();

  const job = useQuery({
    enabled: !!jobId,
    queryKey: ["job", jobId],
    queryFn: async () => must(await api.GET("/jobs/{id}", { params: { path: { id: jobId } } })),
  });
  const snap = snapshotOf(job.data);
  const item = snap && rows(snap).find((r) => r.kind === kind && r.item.remote_id === remoteId)?.item;
  const suggested = item?.suggested;
  const card =
    Number(search.get("card")) || (suggested?.action === "bind" ? suggested.card_id : item?.candidates?.[0]?.card_id) || 0;

  const [action, setAction] = useState<Option["action"]>();
  // Only the snapshot's options are offered: an error item can only be skipped, and a bound one shouldn't default to a duplicate import.
  const actions = (["bind", "import", "skip"] as const).filter((a) => !item || (item.options ?? []).some((o) => o.action === a));
  const act =
    action ?? suggested?.action ?? (!item || item.verdict === "bound" ? "bind" : (actions[0] ?? "skip"));
  const [choices, setChoices] = useState<Choices>({});
  const nested = kind === "items" ? (item?.lorebooks ?? []) : [];
  const [nestedPicks, setNestedPicks] = useState<Record<string, string>>({});
  const [nestedChoices, setNestedChoices] = useState<Record<string, Choices>>({});
  const nestedPick = (lb: Item) => {
    const key = nestedPicks[lb.remote_id] ?? (lb.suggested && optionKey(lb.suggested));
    return (lb.options ?? []).find((o) => optionKey(o) === key);
  };

  const cmp = useQuery({
    enabled: card > 0,
    queryKey: ["compare", instance, kind, remoteId, card],
    gcTime: 0,
    queryFn: () => compareItem(instance, kind, remoteId, card),
  });
  const nestedCmps = useQueries({
    queries: nested.map((lb) => {
      const o = nestedPick(lb);
      return {
        enabled: o?.action === "bind",
        queryKey: ["compare", instance, "lorebook-items", lb.remote_id, o?.card_id ?? 0],
        gcTime: 0,
        queryFn: () => compareItem(instance, "lorebook-items", lb.remote_id, o?.card_id ?? 0),
      };
    }),
  });

  const pickCard = (id: number) => {
    setSearch(
      (s) => {
        s.set("card", String(id));
        return s;
      },
      { replace: true },
    );
    setAction("bind");
    setChoices({});
  };

  const resolve = useMutation({
    mutationFn: async () => {
      const body = resolution(remoteId, cmp.data?.app_hash ?? item?.app_hash ?? "", act, cmp.data, choices);
      const books = nested.flatMap((lb, i) => {
        const o = nestedPick(lb);
        return o ? [resolution(lb.remote_id, lb.app_hash, o.action, nestedCmps[i]?.data, nestedChoices[lb.remote_id])] : [];
      });
      if (books.length && act !== "skip") body.lorebooks = books;
      return resolveItem(instance, kind, remoteId, body);
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["search"] });
      toast({ text: `Resolved ${item?.name || remoteId}. Push to send the library's side to the app.` });
      if (jobId) navigate(`/sync/reconciles/${jobId}`);
      // Opened directly, there's no history entry to go back to.
      else if (location.key === "default") navigate("/sync");
      else navigate(-1);
    },
    onError: (e) => {
      if (e instanceof ApiError && e.status === 409) {
        // Hunk IDs may differ in the fresh comparison, so the old choices can't carry over.
        setChoices({});
        setNestedChoices({});
        void qc.invalidateQueries({ queryKey: ["compare", instance] });
      }
      // Nested world book imports carry the snapshot's hash, which only a new reconcile refreshes.
      const more = e instanceof ApiError && e.status === 409 ? " If this keeps happening, run Reconcile again." : "";
      toast({ text: e.message + more, error: true });
    },
  });

  const ready =
    act === "skip" ||
    ((act !== "bind" || !!cmp.data) &&
      (!!item || !!cmp.data) &&
      nested.every((lb, i) => nestedPick(lb)?.action !== "bind" || !!nestedCmps[i]?.data));
  const name = item?.name || remoteId;

  if (jobId && job.isPending) return <p className="page">Loading…</p>;
  if (job.isError) return <p className="error page">{job.error.message}</p>;
  return (
    <div className="review">
      <p>{jobId ? <Link to={`/sync/reconciles/${jobId}`}>← Reconcile #{jobId}</Link> : <Link to="/sync">← Sync</Link>}</p>
      <section aria-label={name}>
        <div className="row">
          <h1>{name}</h1>
          {item && <span className="muted">{item.verdict}</span>}
          <span className="row" role="radiogroup" aria-label="Action">
            {actions.map((a) => (
              <label key={a} className="check">
                <input type="radio" name="action" checked={act === a} onChange={() => setAction(a)} />{" "}
                {a === "bind" ? "Bind" : a === "import" ? "Import as new" : "Skip"}
              </label>
            ))}
          </span>
          <button className="button primary" disabled={!ready || resolve.isPending} onClick={() => resolve.mutate()}>
            Resolve
          </button>
        </div>
        {suggested && (
          <p className="muted">
            Suggested: {optionLabel(suggested, item?.candidates)}
            {suggested.why && ` (${suggested.why})`}
          </p>
        )}
        <p className="muted">Resolving changes the library and the binding, not the app. Push afterwards.</p>
        <div className="row">
          {!!item?.candidates?.length && (
            <select className="input" aria-label="Library match" value={card} onChange={(e) => pickCard(Number(e.target.value))}>
              {!item.candidates.some((c) => c.card_id === card) && card > 0 && <option value={card}>#{card}</option>}
              {item.candidates.map((c) => (
                <option key={c.card_id} value={c.card_id}>
                  {c.name}
                  {c.bound_to ? ` (bound to ${c.bound_to})` : ""}
                </option>
              ))}
            </select>
          )}
          <OtherMatch kind={kind} onPick={pickCard} />
        </div>
        {card === 0 ? (
          <p>No library match. Find one above, or import it.</p>
        ) : cmp.isError ? (
          <p className="error">{cmp.error.message}</p>
        ) : !cmp.data ? (
          <p>Comparing…</p>
        ) : (
          act === "bind" && <Diff scope={`${kind === "items" ? "card" : "book"}:${remoteId}`} cmp={cmp.data} choices={choices} onChoose={(id, c) => setChoices({ ...choices, [id]: c })} />
        )}
      </section>
      {act !== "skip" &&
        nested.map((lb, i) => {
          const o = nestedPick(lb);
          const q = nestedCmps[i];
          const own = nestedChoices[lb.remote_id] ?? {};
          return (
            <section key={lb.remote_id} className="nested-item" aria-label={`World book ${lb.name || lb.remote_id}`}>
              <h2>
                {lb.name || lb.remote_id} <span className="muted">{lb.verdict}</span>
              </h2>
              {lb.error && <p className="error">{lb.error}</p>}
              {!!lb.options?.length && (
                <select
                  className="input"
                  aria-label={`Action for ${lb.name || lb.remote_id}`}
                  value={o ? optionKey(o) : ""}
                  onChange={(e) => {
                    setNestedPicks({ ...nestedPicks, [lb.remote_id]: e.target.value });
                    setNestedChoices({ ...nestedChoices, [lb.remote_id]: {} });
                  }}
                >
                  {lb.options.map((opt) => (
                    <option key={optionKey(opt)} value={optionKey(opt)}>
                      {optionLabel(opt, lb.candidates)}
                    </option>
                  ))}
                </select>
              )}
              {o?.action === "bind" &&
                (q?.data ? (
                  <Diff
                    scope={`book:${lb.remote_id}`}
                    cmp={q.data}
                    choices={own}
                    onChoose={(id, c) => setNestedChoices({ ...nestedChoices, [lb.remote_id]: { ...own, [id]: c } })}
                  />
                ) : q?.isError ? (
                  <p className="error">{q.error.message}</p>
                ) : (
                  <p>Comparing…</p>
                ))}
            </section>
          );
        })}
    </div>
  );
}
