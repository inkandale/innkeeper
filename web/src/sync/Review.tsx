import { useMutation, useQueries, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type ReactNode } from "react";
import { Link, useLocation, useNavigate, useParams, useSearchParams } from "react-router";
import { api, ApiError, must, thumb } from "../api/client";
import type { components } from "../api/schema";
import { show, text } from "../card/draft";
import { entryOf } from "../card/History";
import { useToast } from "../shell/Toasts";
import {
  changePath,
  changesFilter,
  changesPath,
  compareItem,
  decidable,
  jsonKey,
  optionKey,
  optionLabel,
  queueReviews,
  resolution,
  resolveItem,
  rows,
  sentenceCount,
  snapshotOf,
  wordDiff,
  type Comparison,
  type Item,
  type ItemKind,
  type Option,
  type Run,
} from "./sync";
import "./Sync.css";

type Field = components["schemas"]["Field"];
type Hunk = components["schemas"]["Hunk"];
type Choices = Record<string, string>;

const absent = <em className="muted">absent</em>;

/** Words renders a word diff: what only the library has struck through, what only the app has marked. */
function Words({ runs }: { runs: Run[] }) {
  return <>{runs.map((r, i) => (r.op === "del" ? <del key={i}>{r.text}</del> : r.op === "ins" ? <ins key={i}>{r.text}</ins> : r.text))}</>;
}

/** HunkText shows one changed hunk, in one column or as a Library | App pair. */
function HunkText({ hunk, inline }: { hunk: Hunk; inline: boolean }) {
  const runs = wordDiff(show(hunk.library), show(hunk.app));
  if (inline)
    return (
      <p className="hunks">
        <Words runs={runs} />
      </p>
    );
  return (
    <>
      <p className="hunks">{hunk.library === undefined ? absent : <Words runs={runs.filter((r) => r.op !== "ins")} />}</p>
      <p className="hunks">{hunk.app === undefined ? absent : <Words runs={runs.filter((r) => r.op !== "del")} />}</p>
    </>
  );
}

function HunkChoice(props: { scope: string; hunk: Hunk; label: string; inline: boolean; list: boolean; choice: string; onChoose?: (c: string) => void }) {
  const { scope, hunk, label, inline, list, choice, onChoose } = props;
  const sides = list ? ["library", "app", "both"] : ["library", "app"];
  return (
    <div className={inline ? "hunk-pair inline decide" : "hunk-pair decide"}>
      {label && <span className="muted">{label}</span>}
      <HunkText hunk={hunk} inline={inline} />
      {onChoose && (
        <span className="row" role="radiogroup" aria-label={`Keep for ${hunk.id}`}>
          {sides.map((s) => (
            <label key={s} className="check">
              <input type="radio" name={`${scope}/${hunk.id}`} checked={choice === s} onChange={() => onChoose(s)} />{" "}
              {s === "library" ? "Library" : s === "app" ? "App" : "Both"}
            </label>
          ))}
        </span>
      )}
    </div>
  );
}

/** Unchanged shows a run both sides share, folding prose longer than two sentences. */
function Unchanged({ text, prose }: { text: string; prose: boolean }) {
  const n = prose ? sentenceCount(text) : 0;
  if (n <= 2) return <p className="hunks same">{text}</p>;
  return (
    <details className="unchanged">
      <summary className="muted">⋯ {n} unchanged sentences</summary>
      <p className="hunks same">{text}</p>
    </details>
  );
}

function FieldChoices({ scope, field, choices, onChoose }: { scope: string; field: Field; choices: Choices; onChoose?: (id: string, c: string) => void }) {
  const hunks = field.hunks ?? [];
  const differs = hunks.some((h) => h.status !== "same");
  // Prose reads best as one column; values and structure read best side by side.
  const inline = (h: Hunk) => field.kind === "prose" || (field.kind === "entries" && entryOf(h.id).field === "content");
  const paired = hunks.some((h) => h.status !== "same" && !inline(h));
  const label = (h: Hunk) => {
    if (field.kind === "json") return jsonKey(field.path, h.id);
    if (field.kind !== "entries") return "";
    const e = entryOf(h.id);
    return e.field ? `${e.entry} · ${e.field}` : e.entry;
  };
  const out: ReactNode[] = [];
  let folded: Hunk[] = [];
  // A JSON field's unchanged keys fold into one line, each keeping its label.
  const fold = () => {
    if (folded.length)
      out.push(
        <details key={`same:${folded[0]?.id}`} className="unchanged">
          <summary className="muted">
            {folded.length} unchanged {folded.length === 1 ? "key" : "keys"}
          </summary>
          {folded.map((h) => (
            <p key={h.id} className="hunks same">
              <span className="hunk-key">{label(h)}</span> {show(h.library)}
            </p>
          ))}
        </details>,
      );
    folded = [];
  };
  for (const h of hunks) {
    if (h.status === "same" && field.kind === "json") {
      folded.push(h);
      continue;
    }
    fold();
    if (h.status === "same") out.push(<Unchanged key={h.id} text={show(h.library)} prose={inline(h)} />);
    else if (decidable(h))
      out.push(
        <HunkChoice
          key={h.id}
          scope={scope}
          hunk={h}
          label={label(h)}
          inline={inline(h)}
          list={field.kind === "list"}
          choice={choices[h.id] ?? "library"}
          onChoose={onChoose && ((c) => onChoose(h.id, c))}
        />,
      );
    else
      out.push(
        <div key={h.id} className={inline(h) ? "hunk-pair inline" : "hunk-pair"}>
          <span className="muted">
            {label(h) && `${label(h)} · `}
            {h.status === "auto_app" ? "App's change, merged" : "Library's change, kept"}
          </span>
          <HunkText hunk={h} inline={inline(h)} />
        </div>,
      );
  }
  fold();
  return (
    <details className="field" open={differs}>
      <summary>
        {field.path}
        {!differs && <span className="muted"> · same</span>}
      </summary>
      {paired && (
        <div className="hunk-pair hunk-head">
          <h4>Library</h4>
          <h4>App</h4>
        </div>
      )}
      {out}
    </details>
  );
}

/** Diff lists a comparison field by field, with a choice on each hunk the owner must decide; without onChoose it only shows them. */
function Diff({ scope, cmp, choices, onChoose }: { scope: string; cmp: Comparison; choices: Choices; onChoose?: (id: string, c: string) => void }) {
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
          {onChoose && (
            <span className="row" role="radiogroup" aria-label="Keep for avatar">
              {(a.app ? ["library", "app"] : ["library"]).map((s) => (
                <label key={s} className="check">
                  <input type="radio" name={`${scope}/avatar`} checked={(choices.avatar ?? "library") === s} onChange={() => onChoose("avatar", s)} />{" "}
                  {s === "library" ? "Library" : "App"}
                </label>
              ))}
            </span>
          )}
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
  const p = useParams();
  // Stepping through the queue reuses this route, so each item starts with fresh choices.
  return <ReviewItem key={`${p.instance}/${p.kind}/${p.remoteId}`} />;
}

function ReviewItem() {
  const params = useParams();
  const instance = Number(params.instance);
  const kind: ItemKind = params.kind === "lorebook-items" ? "lorebook-items" : "items";
  const remoteId = params.remoteId ?? "";
  const [search, setSearch] = useSearchParams();
  const jobId = Number(search.get("job")) || 0;
  const twoWay = search.get("base") === "none";
  const queue = search.has("queue");
  const filter = changesFilter(search);
  const navigate = useNavigate();
  const location = useLocation();
  const qc = useQueryClient();
  const toast = useToast();

  const job = useQuery({
    enabled: !!jobId,
    queryKey: ["job", jobId],
    queryFn: async () => must(await api.GET("/jobs/{id}", { params: { path: { id: jobId } } })),
  });
  const changes = useQuery({
    enabled: queue,
    queryKey: ["sync", "changes", filter.instance_id, filter.state],
    queryFn: async () => must(await api.GET("/sync/changes", { params: { query: filter } })),
  });
  const reviews = queueReviews(changes.data?.rows ?? []);
  const at = reviews.findIndex((c) => c.instance_id === instance && c.remote_id === remoteId);
  const next = reviews[at + 1];
  const onward = () => navigate(next ? changePath(next, filter) : changesPath(filter));
  const snap = snapshotOf(job.data);
  const item = snap && rows(snap).find((r) => r.kind === kind && r.item.remote_id === remoteId)?.item;
  const suggested = item?.suggested;
  const candidates = item?.candidates ?? [];
  const card =
    Number(search.get("card")) ||
    (suggested?.action === "bind" ? suggested.card_id : (candidates.find((c) => !c.bound_to) ?? candidates[0])?.card_id) ||
    0;

  const [action, setAction] = useState<Option["action"]>();
  // Only the snapshot's options are offered: an error item can only be skipped, and a bound one shouldn't default to a duplicate import.
  // A match picked by hand adds Bind, unless the item errored.
  const actions = (["bind", "import", "skip"] as const).filter(
    (a) =>
      !item ||
      (item.options ?? []).some((o) => o.action === a) ||
      (a === "bind" && card > 0 && item.verdict !== "error"),
  );
  const act =
    action ?? suggested?.action ?? (!item || item.verdict === "bound" ? "bind" : (actions[0] ?? "skip"));
  const [choices, setChoices] = useState<Choices>({});
  const [afterStale, setAfterStale] = useState(false);
  const nested = kind === "items" ? (item?.lorebooks ?? []) : [];
  const [nestedPicks, setNestedPicks] = useState<Record<string, string>>({});
  const [nestedChoices, setNestedChoices] = useState<Record<string, Choices>>({});
  const nestedPick = (lb: Item) => {
    const key = nestedPicks[lb.remote_id] ?? (lb.suggested && optionKey(lb.suggested));
    return (lb.options ?? []).find((o) => optionKey(o) === key);
  };

  const cmp = useQuery({
    enabled: card > 0,
    queryKey: ["compare", instance, kind, remoteId, card, twoWay],
    gcTime: 0,
    // A refetch would swap the comparison under the owner's choices.
    refetchOnWindowFocus: false,
    queryFn: () => compareItem(instance, kind, remoteId, card, twoWay),
  });
  const nestedCmps = useQueries({
    queries: nested.map((lb) => {
      const o = nestedPick(lb);
      return {
        enabled: o?.action === "bind",
        queryKey: ["compare", instance, "lorebook-items", lb.remote_id, o?.card_id ?? 0],
        gcTime: 0,
        refetchOnWindowFocus: false,
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
    if (item?.verdict !== "error") setAction("bind");
    setChoices({});
  };

  const resolve = useMutation({
    mutationFn: async () => {
      // The binding the owner saw: the snapshot's, since a comparison reopened after an import already names its new
      // binding. A 409's reloaded comparison, or a drift review's, is what they saw instead; it omits binding_id when unbound.
      const bindingId = item && !(afterStale && cmp.data) ? (item.binding_id ?? 0) : (cmp.data?.binding_id ?? 0);
      const body = resolution(remoteId, cmp.data?.app_hash ?? item?.app_hash ?? "", act, cmp.data, choices, bindingId);
      const books = nested.flatMap((lb, i) => {
        const o = nestedPick(lb);
        return o
          ? [resolution(lb.remote_id, lb.app_hash, o.action, nestedCmps[i]?.data, nestedChoices[lb.remote_id], lb.binding_id ?? 0)]
          : [];
      });
      if (books.length && act !== "skip") body.lorebooks = books;
      return resolveItem(instance, kind, remoteId, body);
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["search"] });
      toast({ text: `Resolved ${item?.name || remoteId}. Push to send the library's side to the app.` });
      if (queue) {
        void qc.invalidateQueries({ queryKey: ["sync", "changes"] });
        onward();
      } else if (jobId) navigate(`/sync/reconciles/${jobId}`);
      // Opened directly, there's no history entry to go back to.
      else if (location.key === "default") navigate("/sync");
      else navigate(-1);
    },
    onError: (e) => {
      if (e instanceof ApiError && e.status === 409) {
        // Hunk IDs may differ in the fresh comparison, so the old choices can't carry over.
        setChoices({});
        setNestedChoices({});
        setAfterStale(true);
        void qc.invalidateQueries({ queryKey: ["compare", instance] });
      }
      // Nested world book imports carry the snapshot's hash, which only a new sync refreshes.
      const more = e instanceof ApiError && e.status === 409 ? " If this keeps happening, run Sync again." : "";
      toast({ text: e.message + more, error: true });
    },
  });

  const ready =
    act === "skip" ||
    ((act !== "bind" || !!cmp.data) &&
      (!!item || !!cmp.data) &&
      nested.every((lb, i) => nestedPick(lb)?.action !== "bind" || !!nestedCmps[i]?.data));
  // A drift review has no snapshot item, so it takes the library card's name.
  const name = item?.name || text(cmp.data?.fields?.find((f) => f.path === "name")?.hunks?.map((h) => text(h.library)).join("")) || remoteId;

  if (jobId && job.isPending) return <p className="page">Loading…</p>;
  if (job.isError) return <p className="error page">{job.error.message}</p>;
  return (
    <div className="review">
      <p>{queue ? <Link to={changesPath(filter)}>← Changes</Link> : jobId ? <Link to={`/sync/reconciles/${jobId}`}>← Sync #{jobId}</Link> : <Link to="/sync">← Sync</Link>}</p>
      <section aria-label={name}>
        <div className="row">
          <h1>{name}</h1>
          {item && <span className="muted">{item.verdict}</span>}
          {at >= 0 && <span className="muted">{`${at + 1} of ${reviews.length}`}</span>}
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
          {queue && (
            <button className="button" disabled={!changes.data || resolve.isPending} onClick={onward}>
              Later
            </button>
          )}
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
                  #{c.card_id} {c.name}
                  {c.bound_to ? " (already bound to another app item)" : ""}
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
          act !== "skip" && (
            <Diff
              scope={`${kind === "items" ? "card" : "book"}:${remoteId}`}
              cmp={cmp.data}
              choices={choices}
              onChoose={act === "bind" ? (id, c) => setChoices({ ...choices, [id]: c }) : undefined}
            />
          )
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
                  {!o && <option value="">Server's suggestion</option>}
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
