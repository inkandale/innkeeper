import { useQuery } from "@tanstack/react-query";
import { collectionsQuery, enrichmentKeysQuery, instancesQuery, namesQuery, pluginsQuery } from "../api/queries";
import { text } from "../card/draft";
import { Icon } from "../shell/Icon";
import { apps } from "../sync/Logo";
import { stateLabel } from "../sync/sync";
import { enrichmentValue, parseScore, toggleTerm, valueLabel, type CardQuery, type Term } from "./query";
import type { Names } from "./Tile";

function scoreLabel(value: string): string {
  const s = parseScore(value)!;
  if (s.none) return `${s.key} not evaluated`;
  return `${s.key} ${s.gte ?? "…"}–${s.lte ?? "…"}`;
}

function termLabel(t: Term, names: Names): string {
  const pair = t.kind === "value" ? enrichmentValue(t.value) : undefined;
  if (pair) return `${t.not ? "not " : ""}${pair.key}: ${valueLabel(pair.value)}`;
  const value =
    t.kind === "score" ? scoreLabel(t.value)
    : t.kind === "collection" ? (names.collections.get(Number(t.value)) ?? "#" + t.value)
    : t.kind === "lorebook" ? (names.lorebooks.get(Number(t.value)) || "#" + t.value)
    : t.kind === "target" ? (names.instances.get(Number(t.value)) ?? "#" + t.value)
    : t.kind === "state" ? stateLabel(t.value)
    : t.value;
  return `${t.not ? "not " : ""}${t.kind}: ${value}`;
}

/** useFilterInputs loads what the filter sidebar and its chips need for q: the enrichment keys, the names terms show, and the browsable keys a scored instance offers. */
export function useFilterInputs(q: CardQuery, keysEnabled = true) {
  const keys = useQuery({ ...enrichmentKeysQuery, enabled: keysEnabled });
  const instances = useQuery(instancesQuery);
  const plugins = useQuery(pluginsQuery);
  const collections = useQuery(collectionsQuery(q.library));
  const lorebookIds = q.terms.filter((t) => t.kind === "lorebook").map((t) => Number(t.value));
  const lorebooks = useQuery(namesQuery("lorebook", q.library, lorebookIds));
  const enrichers = keys.data?.instances ?? [];
  const names: Names = {
    instances: new Map((instances.data ?? []).map((i) => [i.id, i.label])),
    collections: new Map((collections.data ?? []).map((c) => [c.id, c.name])),
    lorebooks: new Map((lorebooks.data?.hits ?? []).map((h) => [h.id, text(h.fields.name)])),
    logos: apps(instances.data, plugins.data),
  };
  const browse = (keys.data?.browse ?? []).filter((b) => b.instances?.some((id) => enrichers.some((e) => e.id === id && e.scored)));
  return { keys, instances, plugins, enrichers, names, browse };
}

/** TermChips shows q's terms as chips that remove themselves. */
export function TermChips({ q, names, onChange }: { q: CardQuery; names: Names; onChange: (q: CardQuery) => void }) {
  return q.terms.map((t) => (
    <button
      key={`${t.not ? "-" : ""}${t.kind}:${t.value}`}
      className={t.not ? "chip excluded" : "chip"}
      aria-label={`Remove ${termLabel(t, names)}`}
      onClick={() => onChange(toggleTerm(q, t))}
    >
      {termLabel(t, names)}
      <Icon name="close" />
    </button>
  ));
}
