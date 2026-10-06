import { useMutation, useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { api, must } from "../api/client";
import type { components } from "../api/schema";
import { useToast } from "../shell/Toasts";

type Search = components["schemas"]["Query"];
type Bulk = components["schemas"]["BulkInBody"];
type Job = components["schemas"]["JobOut"];
type Panel = "tag" | "collection" | "push" | "archive" | "export" | null;

const labels = { tag: "Tag", collection: "Collection", push: "Push", archive: "Archive", export: "Export" };

/** SelectionBar replaces the toolbar while cards are picked, and starts bulk jobs on them. */
export function SelectionBar(props: {
  count: number;
  all: boolean;
  total: number;
  query: Search;
  onAll: () => void;
  onClear: () => void;
  kind?: "card" | "lorebook";
}) {
  const { count, all, total, query, onAll, onClear, kind = "card" } = props;
  const noun = kind === "card" ? "card" : "lorebook";
  const plural = (n: number) => `${n} ${noun}${n === 1 ? "" : "s"}`;
  // Lorebooks have no tags, collections, or bulk export.
  const actions = kind === "card" ? (["tag", "collection", "push", "archive", "export"] as const) : (["push", "archive"] as const);
  // Card calls leave kind out, as they did before it existed.
  const kindField = kind === "lorebook" ? { kind } : {};
  const [panel, setPanel] = useState<Panel>(null);
  const [tags, setTags] = useState("");
  const [collection, setCollection] = useState<number>();
  const [instance, setInstance] = useState<number>();
  const [format, setFormat] = useState("json");
  const toast = useToast();
  const n = all ? total : count;

  const collections = useQuery({ queryKey: ["collections"], queryFn: async () => must(await api.GET("/collections")) });
  const instances = useQuery({ queryKey: ["instances"], queryFn: async () => must(await api.GET("/instances")) });
  const plugins = useQuery({ queryKey: ["plugins"], queryFn: async () => must(await api.GET("/plugins")) });
  const pushers = new Set((plugins.data ?? []).filter((p) => p.capabilities?.includes("push")).map((p) => p.name));
  const targets = (instances.data ?? []).filter((i) => pushers.has(i.plugin));

  const start = useMutation({
    mutationFn: (call: () => Promise<Job>) => call(),
    onSuccess: (j) => {
      toast({ text: `Job ${j.id} started`, link: { to: `/settings/jobs/${j.id}`, label: "View job" } });
      onClear();
    },
    onError: (e) => toast({ text: e.message, error: true }),
  });
  const busy = start.isPending;
  const bulk = (body: Omit<Bulk, "query">) =>
    start.mutate(async () => must(await api.POST("/bulk", { body: { query, ...kindField, ...body } })));
  const collectionId = collection ?? collections.data?.[0]?.id ?? 0;
  const instanceId = instance ?? targets[0]?.id ?? 0;
  const tagList = () => tags.split(",").map((t) => t.trim()).filter(Boolean);

  const toggle = (p: Panel) => setPanel(panel === p ? null : p);
  return (
    <div className="toolbar selection-bar">
      <strong>{all ? `All ${total} matching ${noun}${total === 1 ? "" : "s"}` : `${count} selected`}</strong>
      {!all && count < total && (
        <button className="button" onClick={onAll}>
          Select all {total} matching
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
      {panel && (
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
                <select className="input" aria-label="Target" value={instanceId} onChange={(e) => setInstance(Number(e.target.value))}>
                  {targets.map((i) => (
                    <option key={i.id} value={i.id}>
                      {i.name}
                    </option>
                  ))}
                </select>
                <button
                  className="button primary"
                  disabled={busy}
                  onClick={() =>
                    start.mutate(async () =>
                      must(await api.POST("/pushes", { body: { instance_id: instanceId, query, ...kindField } })),
                    )
                  }
                >
                  Push {plural(n)}
                </button>
              </>
            ) : (
              <span>No target can take pushes. Add one in Settings.</span>
            ))}
          {panel === "archive" && (
            <button className="button danger" disabled={busy} onClick={() => bulk({ action: "archive" })}>
              Archive {plural(n)}
            </button>
          )}
          {panel === "export" && (
            <>
              <select className="input" aria-label="Format" value={format} onChange={(e) => setFormat(e.target.value)}>
                <option value="json">JSON</option>
                <option value="png">PNG</option>
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
