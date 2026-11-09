import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState, type FormEvent } from "react";
import { Link, useNavigate, useParams, useSearchParams } from "react-router";
import { toast } from "sonner";
import { api, must, mustOk } from "../api/client";
import { collectionsQuery, enrichmentKeysQuery, librariesQuery, savedSearchesQuery, searchQuery } from "../api/queries";
import type { components } from "../api/schema";
import { text } from "../card/draft";
import { sidebarKeys } from "../cards/filters";
import { useConfirm } from "../shell/ConfirmDialog";
import { count } from "../shell/count";
import { useLibrary } from "../shell/library";
import { toastError } from "../shell/Toasts";
import { complete, conditionsOf, fieldsFor, fromFilter, newCondition, newGroup, opLabel, toFilter, type Condition, type Field, type Group, type Item } from "./conditions";
import { RulePreview } from "./RulePreview";
import { specOf, type Spec } from "./Rules";
import "./Rules.css";

type SavedSearch = components["schemas"]["SavedSearchItem"];

/** RuleBuilder adds a rule, on the saved search ?search= names if any, or edits rule :id. */
export function RuleBuilder() {
  const raw = useParams().id;
  const id = raw === undefined ? undefined : Number(raw);
  const [params] = useSearchParams();
  const library = useLibrary();
  const rule = useQuery({
    queryKey: ["rules", "one", id],
    queryFn: async () => must(await api.GET("/rules/{id}", { params: { path: { id: id! } } })),
    enabled: id !== undefined,
  });
  const searches = useQuery(savedSearchesQuery(library));
  const keys = useQuery(enrichmentKeysQuery);
  const failed = rule.error ?? searches.error ?? keys.error;
  if (failed) return <p className="error">{failed.message}</p>;
  if ((id !== undefined && !rule.data) || !searches.data || !keys.data) return null;
  const fields = fieldsFor(sidebarKeys(keys.data));
  const searchId = rule.data?.saved_search_id ?? (Number(params.get("search")) || undefined);
  const search = searches.data.find((s) => s.id === searchId);
  const initial: Spec = rule.data
    ? specOf(rule.data)
    : { name: "", saved_search_id: search?.id ?? 0, action: "archive", all_libraries: true, enabled: false };
  return <Builder key={id ?? `new-${searchId ?? ""}`} id={id} initial={initial} search={search} fields={fields} library={library} />;
}

type Props = { id?: number; initial: Spec; search?: SavedSearch; fields: Field[]; library: number };

function Builder({ id, initial, search, fields, library }: Props) {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const [ask, dialog] = useConfirm();
  const [spec, setSpec] = useState(initial);
  const decoded = search ? fromFilter(search.filter, fields) : newGroup();
  // A search the page can't show stays as it is; the rest of the rule still edits.
  const readOnly = search !== undefined && decoded === undefined;
  const [root, setRoot] = useState<Group>(() => decoded ?? newGroup());
  const [previewing, setPreviewing] = useState<"save" | "turn-on">();
  const libraries = useQuery(librariesQuery);
  const ruleLibrary = spec.libraries?.[0] ?? library;
  const collections = useQuery({ ...collectionsQuery(ruleLibrary), enabled: spec.action === "collect" });
  const byId = new Map(fields.map((f) => [f.id, f]));
  const filter = readOnly ? search!.filter : toFilter(root, fields);
  // Both sides go through toFilter, so key order and a bare condition's rewrite as and can't count as a change.
  const changed = !readOnly && JSON.stringify(filter) !== JSON.stringify(decoded && toFilter(decoded, fields));
  const fixed = id !== undefined;
  const set = (change: Partial<Spec>) => setSpec({ ...spec, ...change });
  const others = (search?.rules ?? []).filter((r) => r.id !== id);
  const libraryName = (lib: number) => libraries.data?.find((l) => l.id === lib)?.name ?? `Library ${lib}`;

  // Counting waits for typing to pause, so it searches and re-announces once, not per key.
  const [counted, setCounted] = useState(filter);
  const filterKey = JSON.stringify(filter);
  useEffect(() => {
    const t = setTimeout(() => setCounted(filter), 300);
    return () => clearTimeout(t);
  }, [filterKey]);
  const matches = useQuery({
    ...searchQuery("card", { library, filter: counted, fields: ["name"], page: { size: 5, number: 1 } }),
    enabled: counted !== undefined,
    placeholderData: keepPreviousData,
  });
  const valueRefs = fields.filter((f) => f.input === "value").map((f) => f.id.slice("value:".length));
  const facets = useQuery(searchQuery("card", { library, facets: ["tags", "sources", ...valueRefs.map((r) => `enrichment:${r}`)], facets_only: true }));
  const tags = facets.data?.facets?.tags ?? [];
  const sources = facets.data?.facets?.sources ?? [];

  const conditions = conditionsOf(root);
  const incomplete = !readOnly && (conditions.length === 0 || conditions.some((c) => !complete(c, byId.get(c.field))));
  const noLibrary = !spec.all_libraries && !spec.libraries?.length;
  const noCollection = spec.action === "collect" && !spec.collection_id;
  const blocked = !spec.name.trim() || incomplete || noLibrary || noCollection;

  const leave = () => {
    void qc.invalidateQueries({ queryKey: ["rules"] });
    void qc.invalidateQueries({ queryKey: ["saved-searches"] });
    void navigate("/rules");
  };
  const save = useMutation({
    mutationFn: async (turnOn: boolean) => {
      let searchId = search?.id;
      let created: number | undefined;
      if (searchId === undefined) {
        const s = must(await api.POST("/saved-searches", { body: { name: spec.name.trim(), filter: filter!, sort: "" } }));
        searchId = created = s.id;
      }
      const body: Spec = { ...spec, name: spec.name.trim(), saved_search_id: searchId };
      let saved;
      try {
        saved = id !== undefined ? must(await api.PUT("/rules/{id}", { params: { path: { id } }, body })) : must(await api.POST("/rules", { body }));
      } catch (e) {
        // The search was only for this rule.
        if (created !== undefined)
          try {
            await api.DELETE("/saved-searches/{id}", { params: { path: { id: created } } });
          } catch {
            // The rule's own error is the one the owner can act on.
          }
        throw e;
      }
      // A shared search changes only once its rule saved, as the change rechecks every rule on it.
      if (created === undefined && changed) {
        try {
          must(await api.PATCH("/saved-searches/{id}", { params: { path: { id: searchId } }, body: { filter: filter! } }));
        } catch (e) {
          // The rule is saved now, so a retry would add it twice.
          toastError(`Saved ${saved.name}, but couldn't change its conditions: ${(e as Error).message}`);
          leave();
          return undefined;
        }
      }
      if (!turnOn) return saved;
      try {
        return must(await api.PUT("/rules/{id}", { params: { path: { id: saved.id } }, body: { ...specOf(saved), enabled: true } }));
      } catch (e) {
        // The rule exists now, so a retry would add it twice.
        toastError(`Added ${saved.name}, but couldn't turn it on: ${(e as Error).message}`);
        leave();
        return undefined;
      }
    },
    onSuccess: (r) => {
      if (!r) return;
      toast.success(fixed ? `Saved ${r.name}` : r.enabled ? `Added ${r.name} and turned it on` : `Added ${r.name}; it's off until you turn it on`);
      leave();
    },
    meta: { toast: false },
  });
  const remove = useMutation({
    mutationFn: async () => mustOk(await api.DELETE("/rules/{id}", { params: { path: { id: id! } } })),
    onSuccess: () => {
      toast.success(`Deleted ${initial.name}`);
      leave();
    },
  });
  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (fixed && spec.enabled) setPreviewing("save");
    else save.mutate(false);
  };

  // The select's own choice, as one ticked library under "some" must not turn into that library's option.
  const [scope, setScopeChoice] = useState(initial.all_libraries ? "all" : initial.libraries?.length === 1 ? String(initial.libraries[0]) : "some");
  const setScope = (v: string) => {
    setScopeChoice(v);
    if (v === "all") set({ all_libraries: true, libraries: undefined });
    else if (v === "some") set({ all_libraries: false, libraries: spec.libraries ?? [] });
    else set({ all_libraries: false, libraries: [Number(v)], collection_id: spec.action === "collect" ? undefined : spec.collection_id });
  };
  const title = fixed ? initial.name : "New rule";

  return (
    <section className="page">
      <p className="muted">
        <Link to="/rules">Rules</Link> / {title}
      </p>
      <h1>{title}</h1>
      <form className="rule-builder" onSubmit={submit}>
        <label>
          Rule name <input className="input" value={spec.name} onChange={(e) => set({ name: e.target.value })} />
        </label>

        <p className="rule-sentence">
          When a card in{" "}
          <select className="input" aria-label="Libraries" value={scope} disabled={fixed && spec.action === "collect"} onChange={(e) => setScope(e.target.value)}>
            {spec.action === "archive" && <option value="all">all libraries</option>}
            {(libraries.data ?? []).map((l) => (
              <option key={l.id} value={l.id}>
                {l.name}
              </option>
            ))}
            {spec.action === "archive" && <option value="some">some libraries…</option>}
          </select>{" "}
          matches:
        </p>
        {scope === "some" && (
          <fieldset className="rule-libraries">
            <legend>Libraries</legend>
            {(libraries.data ?? []).map((l) => (
              <label key={l.id} className="check">
                <input
                  type="checkbox"
                  checked={spec.libraries?.includes(l.id) ?? false}
                  onChange={(e) => set({ libraries: e.target.checked ? [...(spec.libraries ?? []), l.id] : (spec.libraries ?? []).filter((x) => x !== l.id) })}
                />{" "}
                {l.name}
              </label>
            ))}
            {noLibrary && <p className="muted">Pick at least one library.</p>}
          </fieldset>
        )}

        {readOnly ? (
          <p>
            Matches saved search <strong>{search!.name}</strong>, which has conditions this page can't show.{" "}
            <Link to={`/cards?saved=${search!.id}`}>Edit on Cards</Link>
          </p>
        ) : (
          <GroupView
            group={root}
            path=""
            level={0}
            onChange={setRoot}
            ctx={{ fields, byId, sources, values: (fieldId) => facets.data?.facets?.enrichments?.[fieldId.slice("value:".length)] ?? [] }}
          />
        )}
        <datalist id="rule-tags">
          {tags.slice(0, 200).map((t) => (
            <option key={t.name} value={t.name}>{`${t.name} (${t.cards})`}</option>
          ))}
        </datalist>
        {others.length > 0 && !readOnly && (
          <p className="muted">Changing these conditions also changes {others.map((r) => r.name).join(", ")}, which use the same saved search.</p>
        )}

        <p className="rule-sentence">
          then{" "}
          <select
            className="input"
            aria-label="Action"
            value={spec.action}
            disabled={fixed}
            onChange={(e) => {
              const action = e.target.value as Spec["action"];
              setSpec({ ...spec, action, collection_id: undefined, ...(action === "collect" ? { all_libraries: false, libraries: [ruleLibrary] } : { all_libraries: true, libraries: undefined }) });
              setScopeChoice(action === "collect" ? String(ruleLibrary) : "all");
            }}
          >
            <option value="archive">archive it</option>
            <option value="collect">add it to</option>
          </select>
          {spec.action === "collect" && (
            <>
              {" "}
              <select className="input" aria-label="Collection" value={spec.collection_id ?? ""} disabled={fixed} onChange={(e) => set({ collection_id: Number(e.target.value) || undefined })}>
                <option value="">a collection…</option>
                {(collections.data ?? []).map((c) => (
                  <option key={c.id} value={c.id}>
                    {c.name}
                  </option>
                ))}
              </select>
            </>
          )}
          .
        </p>
        {noCollection && <p className="muted">Pick a collection.</p>}
        {collections.error && <p className="error">{collections.error.message}</p>}

        <p role="status" className="muted">
          {filter === undefined
            ? "Add a condition to see which cards match."
            : matches.error
              ? matches.error.message
              : matches.data
                ? `${count(matches.data.total, "card")} match in ${libraryName(library)}${matches.data.hits?.length ? ": " + matches.data.hits.map((h) => text(h.fields.name) || `#${h.id}`).join(", ") : ""}${matches.data.total > (matches.data.hits?.length ?? 0) ? ", …" : ""}`
                : "Counting cards…"}
        </p>

        {fixed && (
          <label className="check">
            <input type="checkbox" checked={spec.enabled} onChange={(e) => set({ enabled: e.target.checked })} /> On
          </label>
        )}
        {save.error && <p className="error">{save.error.message}</p>}
        <div className="row">
          <Link className="button" to="/rules">
            Cancel
          </Link>
          <button className="button primary" disabled={blocked || save.isPending}>
            Save
          </button>
          {!fixed && (
            <button type="button" className="button" disabled={blocked || save.isPending} onClick={() => setPreviewing("turn-on")}>
              Save and turn on
            </button>
          )}
          {fixed && (
            <button
              type="button"
              className="button danger"
              onClick={() =>
                ask({
                  title: `Delete ${initial.name}?`,
                  body: <p>Its archived cards come back, and the cards it collected leave the collection.</p>,
                  confirmLabel: "Delete rule",
                  danger: true,
                  onConfirm: () => remove.mutate(),
                })
              }
            >
              Delete
            </button>
          )}
        </div>
      </form>
      {previewing && (
        <RulePreview
          id={id}
          spec={{ ...spec, name: spec.name.trim(), saved_search_id: search?.id ?? 0, enabled: true }}
          filter={filter}
          confirmLabel={previewing === "turn-on" ? "Turn on" : "Save rule"}
          onConfirm={() => save.mutate(previewing === "turn-on")}
          onClose={() => setPreviewing(undefined)}
        />
      )}
      {dialog}
    </section>
  );
}

type Ctx = {
  fields: Field[];
  byId: Map<string, Field>;
  sources: { kind: string; cards: number }[];
  values: (fieldId: string) => { value: string; cards: number }[];
};

/** GroupView is one group's items, joined by its connector, with buttons to add to it. path names it in labels, "" for the page's own. */
function GroupView({ group, path, level, onChange, ctx }: { group: Group; path: string; level: number; onChange: (g: Group) => void; ctx: Ctx }) {
  const at = (i: number) => (path ? `${path}.${i + 1}` : `${i + 1}`);
  const where = path ? ` to group ${path}` : "";
  const set = (i: number, item: Item) => onChange({ ...group, items: group.items.map((x, j) => (j === i ? item : x)) });
  const remove = (i: number) => onChange({ ...group, items: group.items.filter((_, j) => j !== i) });
  return (
    <ol className={level ? "rule-group nested" : "rule-group"}>
      {group.items.map((item, i) => (
        <li key={item.key} className="rule-item">
          <span className="rule-connector">
            {i === 0 ? (
              <span>Where</span>
            ) : i === 1 ? (
              <select
                className="input"
                aria-label={path ? `Connector in group ${path}` : "Connector"}
                value={group.op}
                onChange={(e) => onChange({ ...group, op: e.target.value as Group["op"] })}
              >
                <option value="and">and</option>
                <option value="or">or</option>
              </select>
            ) : (
              <span>{group.op}</span>
            )}
          </span>
          <button type="button" className="button ghost rule-not" aria-pressed={item.not} aria-label={`Not ${at(i)}`} onClick={() => set(i, { ...item, not: !item.not })}>
            Not
          </button>
          {item.kind === "group" ? (
            <GroupView group={item} path={at(i)} level={level + 1} onChange={(g) => set(i, g)} ctx={ctx} />
          ) : (
            <ConditionView n={at(i)} row={item} onChange={(c) => set(i, c)} ctx={ctx} />
          )}
          <button type="button" className="button ghost" aria-label={`Remove ${item.kind} ${at(i)}`} onClick={() => remove(i)}>
            ✕
          </button>
        </li>
      ))}
      <li className="rule-add">
        <button type="button" className="button" aria-label={`Add condition${where}`} onClick={() => onChange({ ...group, items: [...group.items, newCondition()] })}>
          + Add condition
        </button>
        {/* Two levels of groups cover real rules and stay readable. */}
        {level < 2 && (
          <button
            type="button"
            className="button"
            aria-label={`Add group${where}`}
            onClick={() => onChange({ ...group, items: [...group.items, { ...newGroup(), items: [newCondition()] }] })}
          >
            + Add group
          </button>
        )}
      </li>
    </ol>
  );
}

/** ConditionView is one condition's field, operator, and value controls. */
function ConditionView({ n, row, onChange, ctx }: { n: string; row: Condition; onChange: (row: Condition) => void; ctx: Ctx }) {
  const field = ctx.byId.get(row.field);
  return (
    <>
      <select
        className="input"
        aria-label={`Field ${n}`}
        value={row.field}
        onChange={(e) => {
          const next = ctx.byId.get(e.target.value)!;
          onChange({ kind: row.kind, key: row.key, not: row.not, field: next.id, op: next.ops[0]!, value: "" });
        }}
      >
        {ctx.fields.map((f) => (
          <option key={f.id} value={f.id}>
            {f.label}
          </option>
        ))}
      </select>
      {field && field.input !== "none" && field.ops.length === 1 && <span>{opLabel(field.ops[0]!, field)}</span>}
      {field && field.ops.length > 1 && (
        <select className="input" aria-label={`Operator ${n}`} value={row.op} onChange={(e) => onChange({ ...row, op: e.target.value as Condition["op"] })}>
          {field.ops.map((op) => (
            <option key={op} value={op}>
              {opLabel(op, field)}
            </option>
          ))}
        </select>
      )}
      {field && row.op !== "missing" && <ValueInput n={n} field={field} row={row} sources={ctx.sources} values={ctx.values(row.field)} onChange={onChange} />}
    </>
  );
}

type ValueProps = {
  n: string;
  field: Field;
  row: Condition;
  sources: { kind: string; cards: number }[];
  values: { value: string; cards: number }[];
  onChange: (row: Condition) => void;
};

/** ValueInput is the control a field's value takes: text, a tag with suggestions, a choice, or numbers. */
function ValueInput({ n, field, row, sources, values, onChange }: ValueProps) {
  const label = `Value ${n}`;
  switch (field.input) {
    case "none":
      return null;
    case "text":
      return <input className="input" aria-label={label} value={row.value} onChange={(e) => onChange({ ...row, value: e.target.value })} />;
    case "tag":
      return <input className="input" aria-label={label} list="rule-tags" value={row.value} onChange={(e) => onChange({ ...row, value: e.target.value })} />;
    case "source":
    case "value": {
      const options = field.input === "source" ? sources.map((s) => ({ value: s.kind, cards: s.cards })) : values;
      return (
        <select className="input" aria-label={label} value={row.value} onChange={(e) => onChange({ ...row, value: e.target.value })}>
          <option value="">Pick one</option>
          {/* A value no card has now still shows, so an older rule reads true. */}
          {row.value && !options.some((o) => o.value === row.value) && <option value={row.value}>{row.value}</option>}
          {options.map((o) => (
            <option key={o.value} value={o.value}>{`${o.value} (${o.cards})`}</option>
          ))}
        </select>
      );
    }
    case "number":
      return (
        <>
          <input className="input rule-number" type="number" aria-label={label} value={row.value} onChange={(e) => onChange({ ...row, value: e.target.value })} />
          {row.op === "between" && (
            <>
              {" and "}
              <input className="input rule-number" type="number" aria-label={`Second value ${n}`} value={row.value2 ?? ""} onChange={(e) => onChange({ ...row, value2: e.target.value })} />
            </>
          )}
        </>
      );
  }
}
