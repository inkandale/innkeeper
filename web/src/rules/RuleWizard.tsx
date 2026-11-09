import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { Link, useNavigate, useParams, useSearchParams } from "react-router";
import { toast } from "sonner";
import { api, must, mustOk } from "../api/client";
import { collectionsQuery, librariesQuery, savedSearchesQuery } from "../api/queries";
import { useConfirm } from "../shell/ConfirmDialog";
import { useLibrary } from "../shell/library";
import { toastError } from "../shell/Toasts";
import { MatchStep } from "./MatchStep";
import { RulePreview } from "./RulePreview";
import { actionLabel, specOf, type Spec } from "./Rules";
import "./Rules.css";

type Step = "match" | "do" | "where" | "review";
const stepLabels: Record<Step, string> = { match: "Match", do: "Do", where: "Where", review: "Review" };

/** RuleWizard adds a rule step by step, on the saved search ?search= names if any, or edits rule :id from its review. */
export function RuleWizard() {
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
  return <Wizard key={id ?? "new"} id={id} initial={initial} library={library} />;
}

function Wizard({ id, initial, library }: { id?: number; initial: Spec; library: number }) {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const [ask, dialog] = useConfirm();
  const [spec, setSpec] = useState(initial);
  const [step, setStep] = useState<Step>(id === undefined ? "match" : "review");
  const [previewing, setPreviewing] = useState(false);
  const searches = useQuery(savedSearchesQuery(library));
  const libraries = useQuery(librariesQuery);
  const collectLibrary = spec.libraries?.[0] ?? library;
  const collections = useQuery({ ...collectionsQuery(collectLibrary), enabled: spec.action === "collect" });
  const set = (change: Partial<Spec>) => setSpec({ ...spec, ...change });
  // A rule's action and collection can't change.
  const fixed = id !== undefined;
  // A collect rule covers its collection's library alone.
  const steps: Step[] = spec.action === "collect" ? ["match", "do", "review"] : ["match", "do", "where", "review"];
  const searchName = searches.data?.find((s) => s.id === spec.saved_search_id)?.name;
  const noLibrary = !spec.all_libraries && !spec.libraries?.length;
  const done: Record<Step, boolean> = {
    match: spec.saved_search_id > 0,
    do: spec.action === "archive" || !!spec.collection_id,
    where: !noLibrary,
    review: true,
  };
  const reachable = (s: Step) => steps.slice(0, steps.indexOf(s)).every((before) => done[before]);
  const go = (to: Step) => {
    // The rule takes its search's name until the owner names it.
    if (to === "review" && !spec.name && searchName) set({ name: searchName });
    setStep(to);
  };
  const next = () => go(steps[steps.indexOf(step) + 1]!);
  const leave = () => {
    void qc.invalidateQueries({ queryKey: ["rules"] });
    void qc.invalidateQueries({ queryKey: ["saved-searches"] });
    void navigate("/rules");
  };
  const save = useMutation({
    mutationFn: async ({ s, on }: { s: Spec; on: boolean }) => {
      if (id !== undefined) return must(await api.PUT("/rules/{id}", { params: { path: { id } }, body: s }));
      // A new rule starts off, so turning it on is a second call.
      const added = must(await api.POST("/rules", { body: s }));
      if (!on) return added;
      try {
        return must(await api.PUT("/rules/{id}", { params: { path: { id: added.id } }, body: { ...specOf(added), enabled: true } }));
      } catch (e) {
        // The rule exists now, so a retry here would add it twice.
        toastError(`Added ${added.name}, but couldn't turn it on: ${(e as Error).message}`);
        leave();
        return undefined;
      }
    },
    onSuccess: (r) => {
      if (!r) return;
      toast.success(id !== undefined ? `Saved ${r.name}` : r.enabled ? `Added ${r.name} and turned it on` : `Added ${r.name}; it's off until you turn it on`);
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
    if (fixed && spec.enabled) setPreviewing(true);
    else save.mutate({ s: spec, on: false });
  };
  const toggleLibrary = (lib: number, on: boolean) =>
    set({ libraries: on ? [...(spec.libraries ?? []), lib] : (spec.libraries ?? []).filter((l) => l !== lib) });
  const libraryName = (lib: number) => libraries.data?.find((l) => l.id === lib)?.name ?? `Library ${lib}`;
  const where = spec.all_libraries ? "All libraries" : (spec.libraries ?? []).map(libraryName).join(", ");
  const title = id === undefined ? "New rule" : initial.name;
  const nextButton = (
    <button className="button primary" disabled={!done[step]} onClick={next}>
      Next
    </button>
  );
  return (
    <section className="page">
      <p className="muted">
        <Link to="/rules">Rules</Link> / {title}
      </p>
      <h1>{title}</h1>
      <nav className="steps" aria-label="Steps">
        {steps.map((s, i) => (
          <button
            key={s}
            className={s === step ? "button primary" : "button"}
            aria-current={s === step ? "step" : undefined}
            disabled={!reachable(s)}
            onClick={() => go(s)}
          >
            {i + 1}. {stepLabels[s]}
          </button>
        ))}
      </nav>
      {step === "match" && (
        <MatchStep
          library={library}
          picked={spec.saved_search_id || undefined}
          onPick={(s) => set({ saved_search_id: s.id })}
          onSaved={(s) => {
            setSpec({ ...spec, saved_search_id: s.id, name: spec.name || s.name });
            setStep("do");
          }}
          onNext={next}
        />
      )}
      {step === "do" && (
        <div className="rule-form">
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
          {spec.action === "collect" && (
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
                <select className="input" value={spec.collection_id ?? ""} disabled={fixed} onChange={(e) => set({ collection_id: Number(e.target.value) || undefined })}>
                  <option value="">Pick one</option>
                  {(collections.data ?? []).map((c) => (
                    <option key={c.id} value={c.id}>
                      {c.name}
                    </option>
                  ))}
                </select>
              </label>
              {collections.error && <p className="error">{collections.error.message}</p>}
            </>
          )}
          {nextButton}
        </div>
      )}
      {step === "where" && (
        <div className="rule-form">
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
          {nextButton}
        </div>
      )}
      {step === "review" && (
        <form className="rule-form" onSubmit={submit}>
          <label>
            Name <input className="input" value={spec.name} required onChange={(e) => set({ name: e.target.value })} />
          </label>
          <dl className="rule-summary">
            <dt>Matches</dt>
            <dd>
              <button type="button" className="link-button" onClick={() => go("match")}>
                {searchName ?? `Saved search ${spec.saved_search_id}`}
              </button>
            </dd>
            <dt>Does</dt>
            <dd>
              <button type="button" className="link-button" onClick={() => go("do")}>
                {actionLabel(spec)}
              </button>
            </dd>
            <dt>In</dt>
            <dd>
              {spec.action === "collect" ? (
                where
              ) : (
                <button type="button" className="link-button" onClick={() => go("where")}>
                  {where}
                </button>
              )}
            </dd>
          </dl>
          {fixed && (
            <label className="check">
              <input type="checkbox" checked={spec.enabled} onChange={(e) => set({ enabled: e.target.checked })} /> On
            </label>
          )}
          {save.error && <p className="error">{save.error.message}</p>}
          <div className="row">
            <button className="button primary" disabled={save.isPending || !spec.name.trim()}>
              Save
            </button>
            {!fixed && (
              <button type="button" className="button" disabled={save.isPending || !spec.name.trim()} onClick={() => setPreviewing(true)}>
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
      )}
      {previewing && (
        <RulePreview
          id={id}
          spec={{ ...spec, enabled: true }}
          confirmLabel={fixed ? "Save rule" : "Turn on"}
          onConfirm={() => save.mutate({ s: spec, on: !fixed })}
          onClose={() => setPreviewing(false)}
        />
      )}
      {dialog}
    </section>
  );
}
