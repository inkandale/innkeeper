import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { Link, useNavigate, useParams, useSearchParams } from "react-router";
import { toast } from "sonner";
import { api, must, mustOk } from "../api/client";
import { collectionsQuery, librariesQuery, savedSearchesQuery } from "../api/queries";
import { useConfirm } from "../shell/ConfirmDialog";
import { useLibrary } from "../shell/library";
import { RulePreview } from "./RulePreview";
import "./Rules.css";
import { specOf, type Spec } from "./Rules";

/** RuleEditor adds a rule on the saved search ?search= names, or edits rule :id. */
export function RuleEditor() {
  const raw = useParams().id;
  const id = raw === undefined ? undefined : Number(raw);
  const [params] = useSearchParams();
  const library = useLibrary();
  const rule = useQuery({
    queryKey: ["rules", "one", id],
    queryFn: async () => must(await api.GET("/rules/{id}", { params: { path: { id: id! } } })),
    enabled: id !== undefined,
  });
  if (rule.error) return <p className="error">{rule.error.message}</p>;
  if (id !== undefined && !rule.data) return null;
  const initial: Spec = rule.data
    ? specOf(rule.data)
    : { name: "", saved_search_id: Number(params.get("search")) || 0, action: "archive", all_libraries: true, enabled: false };
  return <RuleForm key={id ?? "new"} id={id} initial={initial} library={library} />;
}

function RuleForm({ id, initial, library }: { id?: number; initial: Spec; library: number }) {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const [ask, dialog] = useConfirm();
  const [spec, setSpec] = useState(initial);
  const [previewing, setPreviewing] = useState(false);
  const searches = useQuery(savedSearchesQuery(library));
  const libraries = useQuery(librariesQuery);
  const collectLibrary = spec.libraries?.[0] ?? library;
  const collections = useQuery({ ...collectionsQuery(collectLibrary), enabled: spec.action === "collect" });
  const set = (change: Partial<Spec>) => setSpec({ ...spec, ...change });
  const leave = () => {
    void qc.invalidateQueries({ queryKey: ["rules"] });
    void qc.invalidateQueries({ queryKey: ["saved-searches"] });
    void navigate("/rules");
  };
  const save = useMutation({
    mutationFn: async (s: Spec) =>
      id === undefined
        ? must(await api.POST("/rules", { body: s }))
        : must(await api.PUT("/rules/{id}", { params: { path: { id } }, body: s })),
    onSuccess: (r) => {
      toast.success(id === undefined ? `Added ${r.name}; it's off until you turn it on` : `Saved ${r.name}`);
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
    if (spec.enabled) setPreviewing(true);
    else save.mutate(spec);
  };
  // A rule's action and collection can't change.
  const fixed = id !== undefined;
  const noLibrary = !spec.all_libraries && !spec.libraries?.length;
  const title = id === undefined ? "New rule" : initial.name;
  const toggleLibrary = (lib: number, on: boolean) =>
    set({ libraries: on ? [...(spec.libraries ?? []), lib] : (spec.libraries ?? []).filter((l) => l !== lib) });
  return (
    <section className="page">
      <p className="muted">
        <Link to="/rules">Rules</Link> / {title}
      </p>
      <h1>{title}</h1>
      <form className="rule-form" onSubmit={submit}>
        <label>
          Name <input className="input" value={spec.name} required onChange={(e) => set({ name: e.target.value })} />
        </label>
        <label>
          Saved search{" "}
          <select className="input" value={spec.saved_search_id || ""} required onChange={(e) => set({ saved_search_id: Number(e.target.value) })}>
            <option value="">Pick one</option>
            {(searches.data ?? []).map((s) => (
              <option key={s.id} value={s.id}>
                {s.name}
              </option>
            ))}
          </select>
        </label>
        {searches.data?.length === 0 && (
          <p className="muted">
            No saved searches yet. Filter the cards on <Link to="/cards">Cards</Link>, then choose Save search.
          </p>
        )}
        <label>
          Action{" "}
          <select
            className="input"
            value={spec.action}
            disabled={fixed}
            onChange={(e) => {
              const action = e.target.value as Spec["action"];
              const scope = action === "collect" ? { all_libraries: false, libraries: [library] } : { all_libraries: true, libraries: undefined };
              setSpec({ ...spec, action, collection_id: undefined, ...scope });
            }}
          >
            <option value="archive">Archive</option>
            <option value="collect">Add to a collection</option>
          </select>
        </label>
        {spec.action === "collect" ? (
          <>
            <label>
              Library{" "}
              <select className="input" value={collectLibrary} disabled={fixed} onChange={(e) => set({ libraries: [Number(e.target.value)], collection_id: undefined })}>
                {(libraries.data ?? []).map((l) => (
                  <option key={l.id} value={l.id}>
                    {l.name}
                  </option>
                ))}
              </select>
            </label>
            <label>
              Collection{" "}
              <select className="input" value={spec.collection_id ?? ""} required disabled={fixed} onChange={(e) => set({ collection_id: Number(e.target.value) })}>
                <option value="">Pick one</option>
                {(collections.data ?? []).map((c) => (
                  <option key={c.id} value={c.id}>
                    {c.name}
                  </option>
                ))}
              </select>
            </label>
          </>
        ) : (
          <fieldset>
            <legend>Libraries</legend>
            <label className="check">
              <input type="checkbox" checked={spec.all_libraries} onChange={(e) => set({ all_libraries: e.target.checked })} /> All libraries, new ones included
            </label>
            {!spec.all_libraries &&
              (libraries.data ?? []).map((l) => (
                <label key={l.id} className="check">
                  <input type="checkbox" checked={spec.libraries?.includes(l.id) ?? false} onChange={(e) => toggleLibrary(l.id, e.target.checked)} /> {l.name}
                </label>
              ))}
            {noLibrary && <p className="muted">Pick at least one library.</p>}
          </fieldset>
        )}
        {fixed && (
          <label className="check">
            <input type="checkbox" checked={spec.enabled} onChange={(e) => set({ enabled: e.target.checked })} /> On
          </label>
        )}
        {save.error && <p className="error">{save.error.message}</p>}
        <div className="row">
          <button className="button primary" disabled={save.isPending || noLibrary}>
            {id === undefined ? "Add rule" : "Save"}
          </button>
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
      {previewing && <RulePreview id={id} spec={spec} confirmLabel="Save rule" onConfirm={() => save.mutate(spec)} onClose={() => setPreviewing(false)} />}
      {dialog}
    </section>
  );
}
