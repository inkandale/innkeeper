import { useMutation, useQueries, useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { api, exportFormats, must } from "../api/client";
import { collectionsQuery, enrichmentKeysQuery, instancesQuery, librariesQuery, pluginsQuery, useInstancesWith } from "../api/queries";
import type { components } from "../api/schema";
import { useToast } from "../shell/Toasts";
import { MoveLibrary } from "./MoveLibrary";
import { SetParent, type Picked } from "./SetParent";
import { jobToast } from "../settings/jobUtil";
import { pushSummary } from "../sync/sync";
import { TargetPicker } from "../sync/TargetPicker";

type Search = components["schemas"]["Query"];
type Bulk = components["schemas"]["BulkInBody"];
type Job = components["schemas"]["JobOut"];
type Panel = "tag" | "collection" | "push" | "archive" | "export" | "evaluate" | "delete" | "parent" | "move" | null;

const labels = {
  tag: "Tag",
  collection: "Collection",
  push: "Push",
  archive: "Archive",
  export: "Export",
  evaluate: "Score",
  delete: "Delete…",
  parent: "Set parent…",
  move: "Move to library…",
};

/** SelectionBar replaces the toolbar while cards are picked, and starts bulk jobs on them. */
export function SelectionBar(props: {
  all: boolean;
  /** total is left out when the list groups variants, since it then counts families. */
  total?: number;
  query: Search;
  library: number;
  ids: number[];
  /** shown is the picked entries on this page, by ID. */
  shown: Map<number, Picked>;
  /** onAll is left out when the search can't be selected whole. */
  onAll?: () => void;
  /** onPage adds the rest of this page; left out once the whole page is picked. */
  onPage?: () => void;
  onClear: () => void;
  onStarted?: (job: number) => void;
  kind?: "card" | "lorebook";
}) {
  const { all, total, query, library, ids, shown, onAll, onPage, onClear, onStarted, kind = "card" } = props;
  const plural = (n?: number) => (n === undefined ? `all matching ${kind}s` : `${n} ${kind}${n === 1 ? "" : "s"}`);
  const enrichers = useInstancesWith("enrich");
  const keys = useQuery({ ...enrichmentKeysQuery, enabled: kind === "card" });
  // Lorebooks have no tags, collections, bulk export, or enrichments.
  const base =
    kind === "lorebook" ? (["push", "archive"] as const)
    : enrichers.length ? (["tag", "collection", "push", "archive", "export", "evaluate", "delete"] as const)
    : (["tag", "collection", "push", "archive", "export", "delete"] as const);
  const libraries = useQuery(librariesQuery);
  // Set parent and Move send the picked IDs, which "Select all matching" doesn't have. Move needs somewhere to go.
  const canMove = (libraries.data?.length ?? 0) > 1;
  const actions = all ? base : canMove ? ([...base, "parent", "move"] as const) : ([...base, "parent"] as const);
  // Card calls leave kind out, as they did before it existed.
  const kindField = kind === "lorebook" ? { kind } : {};
  const [panel, setPanel] = useState<Panel>(null);
  const [tags, setTags] = useState("");
  const [collection, setCollection] = useState<number>();
  const [picked, setPicked] = useState<number[]>();
  const [format, setFormat] = useState("json");
  const [enricher, setEnricher] = useState<number>();
  const [rescore, setRescore] = useState(false);
  const [confirmBig, setConfirmBig] = useState(false);
  const [unpublish, setUnpublish] = useState(false);
  // The count typed to confirm a delete; the server refuses one that no longer matches.
  const [typed, setTyped] = useState("");
  const toast = useToast();
  const count = ids.length;
  const n = all ? total : count;

  const collections = useQuery(collectionsQuery(library));
  const instances = useQuery(instancesQuery);
  const plugins = useQuery(pluginsQuery);
  const targets = useInstancesWith("push");

  const start = useMutation({
    mutationFn: (call: () => Promise<Job>) => call(),
    onSuccess: (j) => {
      toast(jobToast(j));
      onStarted?.(j.id);
      onClear();
    },
  });
  const bulk = (body: Omit<Bulk, "query">) =>
    start.mutate(async () => must(await api.POST("/bulk", { body: { query, ...kindField, ...body } })));
  const collectionId = collection ?? collections.data?.[0]?.id ?? 0;
  const enricherId = enricher ?? keys.data?.scorers?.[0]?.instance ?? enrichers[0]?.id ?? 0;
  const enricherLabel = enrichers.find((i) => i.id === enricherId)?.label ?? "the plugin";
  const enricherPlugin = instances.data?.find((i) => i.id === enricherId)?.plugin;
  const maxCards = plugins.data?.find((p) => p.name === enricherPlugin)?.enrich_max_cards;
  // Past the plugin's limit the API wants confirm, since each card may cost a model call.
  const big = maxCards !== undefined && n !== undefined && n > maxCards;
  const evaluate = () =>
    start.mutate(async () => must(await api.POST("/enrichments/runs", { body: { instance_id: enricherId, query, rescore, ...(big && { confirm: true }) } })));
  const chosen = targets.filter((t) => (picked ?? [targets[0]?.id]).includes(t.id));
  // What the push does to each copy. Its confirm makes the push leave alone any copy that changes after this.
  const previews = useQueries({
    queries: chosen.map((t) => ({
      enabled: panel === "push",
      queryKey: ["push-preview", kind, t.id, query],
      queryFn: async () => must(await api.POST("/pushes/preview", { body: { instance_id: t.id, query, ...kindField } })),
    })),
  });
  // One push job per target. A target that fails stays picked, so pushing again retries only it.
  const push = useMutation({
    mutationFn: async () => {
      const failed: string[] = [];
      for (const [i, t] of chosen.entries()) {
        const res = await api.POST("/pushes", { body: { instance_id: t.id, query, ...kindField, confirm: previews[i]!.data!.confirm } });
        // A 409 means the copies changed since the preview; show the new one.
        if (res.response.status === 409) void previews[i]!.refetch();
        try {
          const j = must(res);
          toast(jobToast(j));
          onStarted?.(j.id);
          setPicked((ids) => (ids ?? chosen.map((c) => c.id)).filter((id) => id !== t.id));
        } catch (e) {
          failed.push(`${t.label}: ${(e as Error).message}`);
        }
      }
      if (failed.length) throw new Error(failed.join("; "));
      onClear();
    },
  });
  const busy = start.isPending || push.isPending;
  const tagList = () => tags.split(",").map((t) => t.trim()).filter(Boolean);

  const toggle = (p: Panel) => setPanel(panel === p ? null : p);
  return (
    <div className="toolbar selection-bar">
      <strong>{all ? (total === undefined ? `All matching ${kind}s` : `All ${total} matching ${kind}${total === 1 ? "" : "s"}`) : `${count} selected`}</strong>
      {!all && onPage && (
        <button className="button" onClick={onPage}>
          Select page
        </button>
      )}
      {!all && onAll && (total === undefined || count < total) && (
        <button className="button" onClick={onAll}>
          Select all {total === undefined ? "" : `${total} `}matching
        </button>
      )}
      <button className="button" onClick={onClear}>
        Clear
      </button>
      <span className="actions">
        {actions.map((p) => (
          <button key={p} className={panel === p ? "button primary" : "button"} aria-expanded={panel === p} onClick={() => toggle(p)}>
            {labels[p]}
          </button>
        ))}
      </span>
      {panel === "parent" && (
        <SetParent kind={kind} picked={ids.map((id) => shown.get(id) ?? { id, name: `#${id}` })} onClose={() => setPanel(null)} onDone={onClear} />
      )}
      {panel === "move" && <MoveLibrary kind={kind} ids={ids} library={library} onClose={() => setPanel(null)} onDone={onClear} />}
      {panel && panel !== "parent" && panel !== "move" && (
        <div className="bulk-panel">
          {panel === "tag" && (
            <>
              <input className="input" aria-label="Tags" placeholder="Tags, comma separated" value={tags} onChange={(e) => setTags(e.target.value)} />
              <button className="button primary" disabled={busy || !tagList().length} onClick={() => bulk({ action: "tag", tags: tagList() })}>
                Add tags
              </button>
              <button className="button" disabled={busy || !tagList().length} onClick={() => bulk({ action: "untag", tags: tagList() })}>
                Remove tags
              </button>
            </>
          )}
          {panel === "collection" && (
            <>
              <select className="input" aria-label="Collection" value={collectionId} onChange={(e) => setCollection(Number(e.target.value))}>
                {(collections.data ?? []).map((c) => (
                  <option key={c.id} value={c.id}>
                    {c.name}
                  </option>
                ))}
              </select>
              <button className="button primary" disabled={busy || !collectionId} onClick={() => bulk({ action: "collect", collection: collectionId })}>
                Add to collection
              </button>
              <button className="button" disabled={busy || !collectionId} onClick={() => bulk({ action: "uncollect", collection: collectionId })}>
                Remove from collection
              </button>
            </>
          )}
          {panel === "push" &&
            (!instances.data || !plugins.data ? (
              <span>Loading…</span>
            ) : targets.length ? (
              <>
                <TargetPicker targets={targets} picked={chosen.map((t) => t.id)} onChange={setPicked} />
                <button className="button primary" disabled={busy || !chosen.length || previews.some((p) => !p.data)} onClick={() => push.mutate()}>
                  Push {plural(n)}
                </button>
                {chosen.map((t, i) => {
                  const p = previews[i]!;
                  return (
                    <span key={t.id} className="muted">
                      {chosen.length > 1 && `${t.label}: `}
                      {p.data ? pushSummary(p.data) : p.error?.message}
                    </span>
                  );
                })}
              </>
            ) : (
              <span>No app can take pushes. Set one up in Settings.</span>
            ))}
          {panel === "archive" && (
            <>
              {targets.length > 0 && (
                <label className="check">
                  <span>Also delete from linked apps, chats included</span>
                  <input type="checkbox" checked={unpublish} onChange={(e) => setUnpublish(e.target.checked)} />
                </label>
              )}
              <button className="button danger" disabled={busy} onClick={() => bulk({ action: "archive", unpublish })}>
                Archive {plural(n)}
              </button>
            </>
          )}
          {panel === "evaluate" && (
            <>
              {/* With one enricher there's nothing to ask. */}
              {enrichers.length > 1 && (
                <select className="input" aria-label="Score with" value={enricherId} onChange={(e) => setEnricher(Number(e.target.value))}>
                  {enrichers.map((i) => (
                    <option key={i.id} value={i.id}>
                      {i.label}
                    </option>
                  ))}
                </select>
              )}
              <label className="check">
                <span>Re-score cards already scored</span>
                <input type="checkbox" checked={rescore} onChange={(e) => setRescore(e.target.checked)} />
              </label>
              {big && (
                <label className="check">
                  <span>
                    Send all {n.toLocaleString()} cards to {enricherLabel}, over its usual limit of {maxCards.toLocaleString()}
                  </span>
                  <input type="checkbox" checked={confirmBig} onChange={(e) => setConfirmBig(e.target.checked)} />
                </label>
              )}
              <button className="button primary" disabled={busy || !enricherId || (big && !confirmBig)} onClick={evaluate}>
                Score {plural(n)}
              </button>
              {start.error && <span className="error">{start.error.message}</span>}
            </>
          )}
          {panel === "delete" && (
            <>
              <span>
                Deletes {plural(n)} and their history for good; this can't be undone. Cards pushed to an app are kept.
              </span>
              <input
                className="input"
                inputMode="numeric"
                aria-label="Number of cards, to confirm"
                placeholder={n === undefined ? "Number of cards" : `Type ${n}`}
                value={typed}
                onChange={(e) => setTyped(e.target.value.trim())}
              />
              <button
                className="button danger"
                disabled={busy || !typed || (n !== undefined && typed !== String(n))}
                onClick={() => start.mutate(async () => must(await api.POST("/cards/purge", { body: { query, confirm: typed } })))}
              >
                Delete {plural(n)} permanently
              </button>
              {start.error && <span className="error">{start.error.message}</span>}
            </>
          )}
          {panel === "export" && (
            <>
              <select className="input" aria-label="Format" value={format} onChange={(e) => setFormat(e.target.value)}>
                {exportFormats(plugins.data, instances.data).map((f) => (
                  <option key={f} value={f}>
                    {f.toUpperCase()}
                  </option>
                ))}
              </select>
              <button className="button primary" disabled={busy} onClick={() => bulk({ action: "export", format })}>
                Export {plural(n)}
              </button>
            </>
          )}
        </div>
      )}
    </div>
  );
}
