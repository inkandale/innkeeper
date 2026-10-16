import { useMutation, useQueries, useQuery, useQueryClient, type UseQueryResult } from "@tanstack/react-query";
import { Fragment, useState } from "react";
import { Link, useLocation, useNavigate, useParams, useSearchParams } from "react-router";
import { api, ApiError, must, route, type Kind } from "../api/client";
import { CardPicker } from "../cards/CardPicker";
import { useFollowers } from "../api/queries";
import { text } from "../card/draft";
import { useJob } from "../settings/jobUtil";
import { useToast } from "../shell/Toasts";
import { Diff, type Choices } from "./Diff";
import {
  changePath,
  changesFilter,
  changesPath,
  compareItem,
  optionKey,
  optionLabel,
  queueReviews,
  resolution,
  resolveItem,
  rows,
  siteOf,
  snapshotOf,
  type Comparison,
  type Item,
  type Option,
} from "./sync";
import "./Sync.css";

/** Review is the diff editor: one app item against a library card, decided hunk by hunk, with the card's world books nested. */
export function Review() {
  const p = useParams();
  const [search] = useSearchParams();
  // Stepping through the queue reuses this route, so each item starts with fresh choices. Without a snapshot the item
  // is its card, since several cards may follow one source.
  const card = search.has("job") ? "" : `/${search.get("card")}`;
  return <ReviewItem key={`${p.instance}/${p.kind}/${p.remoteId}${card}`} />;
}

function ReviewItem() {
  const params = useParams();
  const instance = Number(params.instance);
  // lorebook-items is the segment links had before they named the kind.
  const kind: Kind = params.kind === "lorebook" || params.kind === "lorebook-items" ? "lorebook" : "card";
  const remoteId = params.remoteId ?? "";
  const [search, setSearch] = useSearchParams();
  const jobId = Number(search.get("job")) || 0;
  const twoWay = search.get("base") === "none";
  const queue = search.has("queue");
  const filter = changesFilter(search);
  const follows = useFollowers();
  const followed = !!follows?.has(instance);
  const navigate = useNavigate();
  const location = useLocation();
  const qc = useQueryClient();
  const toast = useToast();

  const job = useJob(jobId, { enabled: !!jobId, follow: false });
  const changes = useQuery({
    enabled: queue,
    queryKey: ["sync", "changes", filter.instance_id, filter.state],
    queryFn: async () => must(await api.GET("/sync/changes", { params: { query: filter } })),
  });
  const reviews = queueReviews(changes.data?.rows ?? []);
  // Several cards may follow one source, so a followed source's place in the queue is its card's or lorebook's.
  const entity = Number(search.get("card"));
  const at = reviews.findIndex(
    (c) => c.instance_id === instance && c.remote_id === remoteId && (!followed || (c.lorebook_id ?? c.card_id) === entity),
  );
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
  const [asVariant, setAsVariant] = useState(false);
  // Only the snapshot's options are offered: an error item can only be skipped, and a bound one shouldn't default to a duplicate import.
  // A match picked by hand adds Bind, unless the item errored.
  // A followed source can only merge into its card: importing or skipping it would mean nothing.
  const actions: Option["action"][] = followed
    ? ["bind"]
    : (["bind", "import", "skip"] as const).filter(
        (a) =>
          !item ||
          (item.options ?? []).some((o) => o.action === a) ||
          (a === "bind" && card > 0 && item.verdict !== "error"),
      );
  const act = followed
    ? "bind"
    : (action ?? suggested?.action ?? (!item || item.verdict === "bound" ? "bind" : (actions[0] ?? "skip")));
  const [choices, setChoices] = useState<Choices>({});
  const [afterStale, setAfterStale] = useState(false);
  const nested = kind === "card" ? (item?.lorebooks ?? []) : [];
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
        queryKey: ["compare", instance, "lorebook", lb.remote_id, o?.card_id ?? 0],
        gcTime: 0,
        refetchOnWindowFocus: false,
        queryFn: () => compareItem(instance, "lorebook", lb.remote_id, o?.card_id ?? 0),
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
    setAsVariant(false);
    setChoices({});
  };

  const resolve = useMutation({
    mutationFn: async () => {
      // The binding the owner saw: the snapshot's, since a comparison reopened after an import already names its new
      // binding. A 409's reloaded comparison, or a drift review's, is what they saw instead; it omits binding_id when unbound.
      const bindingId = item && !(afterStale && cmp.data) ? (item.binding_id ?? 0) : (cmp.data?.binding_id ?? 0);
      const body = resolution(remoteId, cmp.data?.app_hash ?? item?.app_hash ?? "", act, cmp.data, choices, bindingId);
      if (act === "import" && asVariant) body.variant_of = card;
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
      toast({ text: followed ? `Resolved ${name}.` : `Resolved ${item?.name || remoteId}. Push to send the library's side to the app.` });
      if (queue) {
        void qc.invalidateQueries({ queryKey: ["sync", "changes"] });
        onward();
      } else if (jobId) navigate(`/sync/reconciles/${jobId}`);
      // Opened directly, there's no history entry to go back to.
      else if (location.key === "default") navigate("/sync");
      else navigate(-1);
    },
    meta: { toast: false },
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

  // Until the instances load, a followed source would show as an app item, with Skip and the wrong queue position.
  if (!follows || (jobId && job.isPending)) return <p className="page">Loading…</p>;
  if (job.isError) return <p className="error page">{job.error.message}</p>;
  return (
    <div className="review">
      <p>{queue ? <Link to={changesPath(filter)}>← Changes</Link> : jobId ? <Link to={`/sync/reconciles/${jobId}`}>← Sync #{jobId}</Link> : <Link to="/sync">← Sync</Link>}</p>
      <section aria-label={name}>
        <div className="row">
          <h1>{name}</h1>
          {item && <span className="muted">{item.verdict}</span>}
          {at >= 0 && <span className="muted">{`${at + 1} of ${reviews.length}`}</span>}
          {!followed && (
            <span className="row" role="radiogroup" aria-label="Action">
              {actions.map((a) => (
                <Fragment key={a}>
                  <label className="check">
                    <input
                      type="radio"
                      name="action"
                      checked={act === a && !(a === "import" && asVariant)}
                      onChange={() => {
                        setAction(a);
                        setAsVariant(false);
                      }}
                    />{" "}
                    {a === "bind" ? "Bind" : a === "import" ? "Import as new" : "Skip"}
                  </label>
                  {a === "import" && card > 0 && (
                    <label className="check">
                      <input
                        type="radio"
                        name="action"
                        checked={act === "import" && asVariant}
                        onChange={() => {
                          setAction("import");
                          setAsVariant(true);
                        }}
                      />{" "}
                      Import as variant of this
                    </label>
                  )}
                </Fragment>
              ))}
            </span>
          )}
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
        <p className="muted">
          {followed
            ? "Resolving saves the merge as a new revision. The site isn't changed."
            : "Resolving changes the library and the binding, not the app. Push afterwards."}
        </p>
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
          <CardPicker kind={kind} label="Other match" onPick={pickCard} />
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
              scope={`${kind === "card" ? "card" : "book"}:${remoteId}`}
              cmp={cmp.data}
              sides={followed ? { library: "Library", app: siteOf(remoteId) } : undefined}
              choices={choices}
              onChoose={act === "bind" ? (id, c) => setChoices({ ...choices, [id]: c }) : undefined}
            />
          )
        )}
      </section>
      {act !== "skip" &&
        nested.map((lb, i) => {
          const own = nestedChoices[lb.remote_id] ?? {};
          return (
            <NestedLorebook
              key={lb.remote_id}
              lb={lb}
              pick={nestedPick(lb)}
              cmp={nestedCmps[i]}
              choices={own}
              onPick={(key) => {
                setNestedPicks({ ...nestedPicks, [lb.remote_id]: key });
                setNestedChoices({ ...nestedChoices, [lb.remote_id]: {} });
              }}
              onChoose={(id, c) => setNestedChoices({ ...nestedChoices, [lb.remote_id]: { ...own, [id]: c } })}
            />
          );
        })}
    </div>
  );
}

/** NestedLorebook is one of the card's world books: what to do with it, and its diff when it binds. */
function NestedLorebook(props: {
  lb: Item;
  pick?: Option;
  cmp?: UseQueryResult<Comparison>;
  choices: Choices;
  onPick: (key: string) => void;
  onChoose: (id: string, c: string) => void;
}) {
  const { lb, pick, cmp, choices, onPick, onChoose } = props;
  return (
    <section className="nested-item" aria-label={`World book ${lb.name || lb.remote_id}`}>
      <h2>
        {lb.name || lb.remote_id} <span className="muted">{lb.verdict}</span>
      </h2>
      {lb.error && <p className="error">{lb.error}</p>}
      {!!lb.options?.length && (
        <select className="input" aria-label={`Action for ${lb.name || lb.remote_id}`} value={pick ? optionKey(pick) : ""} onChange={(e) => onPick(e.target.value)}>
          {!pick && <option value="">Server's suggestion</option>}
          {lb.options.map((opt) => (
            <option key={optionKey(opt)} value={optionKey(opt)}>
              {optionLabel(opt, lb.candidates)}
            </option>
          ))}
        </select>
      )}
      {pick?.action === "bind" &&
        (cmp?.data ? (
          <Diff scope={`book:${lb.remote_id}`} cmp={cmp.data} choices={choices} onChoose={onChoose} />
        ) : cmp?.isError ? (
          <p className="error">{cmp.error.message}</p>
        ) : (
          <p>Comparing…</p>
        ))}
    </section>
  );
}
