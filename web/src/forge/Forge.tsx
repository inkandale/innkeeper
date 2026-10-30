import { useEffect, useRef, useState, type KeyboardEvent as ReactKeyboardEvent, type ReactNode } from "react";
import type { Data } from "../card/draft";
import { ConfirmDialog } from "../shell/ConfirmDialog";
import { changedKeys, instructionAction, newAction, type Action, type Report as ReportT, type Severity } from "./actions";
import { Hammer } from "./Hammer";
import { ActionOptions, asks, imagesOf, ModelPicker, noOptions, type Options } from "./Inputs";
import { blocking, emptyInstruction, MentionBox, useMentions, type Instruction } from "./MentionBox";
import { contextOf } from "./mentions";
import { Reforge, type Target } from "./Reforge";
import { Report, reportAnchor } from "./Report";
import { Review } from "./Review";
import { RunHeader, WaitingHeader } from "./RunHeader";
import type { Forge as ForgeState } from "./useForge";
import "./Forge.css";

type Tab = "start" | "fields" | "review" | "report";

const severities: Severity[] = ["high", "medium", "low"];

/** Lock returns the label of the run that locks a data key, if one does. */
export type Lock = (key: string) => string | undefined;

/**
 * Forge is a card or lorebook page's main column: the Fields tab, and with an action plugin, Review, Report, the
 * Reforge panel, and the runs. fields renders the Fields tab; with an action plugin it gets the locks and extra, a
 * text field's header additions. A new card has a Start tab instead of Report. avatar is the current avatar: a blob
 * SHA, or a new card's file.
 */
export function Forge(props: {
  forge: ForgeState;
  isNew?: boolean;
  data: Data;
  edit: (key: string, value: unknown) => void;
  /** save saves the draft with a reviewed proposal's changes; without it, Review only applies them to the draft. */
  save?: (changes: Data) => void;
  fields: (lock?: Lock, extra?: (key: string) => ReactNode) => ReactNode;
  avatar?: Blob | string;
}) {
  const { forge, data, isNew } = props;
  const { kind, label } = forge.subject;
  const [tab, setTab] = useState<Tab>();
  const [panel, setPanel] = useState(false);
  const [target, setTarget] = useState<Target>();
  const [instruction, setInstruction] = useState<Instruction>(emptyInstruction);
  // A plugin whose actions are all for other kinds leaves this page the plain editor.
  const on = forge.actions.some((a) => a.applies === forge.subject.kind || (isNew && a.applies === "new"));
  const name = typeof data.name === "string" ? data.name : "";
  const verb = isNew ? "Forge" : "Reforge";

  const open = (t?: Target) => {
    if (isNew && !t) return setTab("start");
    setTarget(t);
    setPanel(true);
  };
  useEffect(() => {
    if (!on) return;
    const key = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        if (isNew) setTab("start");
        else {
          setTarget(undefined);
          setPanel(true);
        }
      }
    };
    window.addEventListener("keydown", key);
    return () => window.removeEventListener("keydown", key);
  }, [on, isNew]);

  // A proposal opens Review when it arrives; a report from a run started here opens Report.
  const pendingId = forge.pending?.id;
  useEffect(() => {
    if (pendingId !== undefined) setTab("review");
  }, [pendingId]);
  const reported = useRef(new Set<number>());
  useEffect(() => {
    for (const r of forge.runs)
      if (forge.started.includes(r.id) && r.result?.report && !r.result.proposal && !reported.current.has(r.id)) {
        reported.current.add(r.id);
        setTab("report");
      }
  }, [forge.runs, forge.started]);

  const toReport = (key: string) => {
    setTab("report");
    const id = forge.report && reportAnchor(forge.report, key);
    requestAnimationFrame(() => id && document.getElementById(id)?.scrollIntoView({ behavior: "smooth" }));
  };
  const pickField = (a: Action, key: string) => (asks(a) || a.images ? open({ action: a, field: key }) : forge.start({ action: a, field: key }));
  const extra = (key: string) => (
    <>
      {forge.report && <Badges report={forge.report} field={key} label={label} onOpen={() => toReport(key)} />}
      <FieldMenu
        field={key}
        label={label}
        // A new card has no Report tab to show a report-only action's result.
        actions={forge.mine.filter((a) => a.applies === kind && a.fields?.includes(key) && !(isNew && a.report_only))}
        disabled={!!forge.lock(key)}
        onPick={(a) => pickField(a, key)}
      />
    </>
  );

  const pending = forge.pending;
  const proposed = pending?.result?.proposal && forge.subject.review(pending.start, data, pending.result.proposal);
  const reviewCount = proposed ? proposed.rows.length : 0;
  const tabs: [Tab, string][] = isNew
    ? [["start", "Start"], ["fields", "Fields"], ["review", `Review${pending ? ` (${reviewCount})` : ""}`]]
    : [["fields", "Fields"], ["review", `Review${pending ? ` (${reviewCount})` : ""}`], ["report", "Report"]];
  const revise = instructionAction(forge.mine, kind);
  const shown: Tab = on ? (tab ?? (isNew ? "start" : "fields")) : "fields";
  const tabId = (t: Tab) => `forge-tab-${t}`;
  const usable = tabs.filter(([t]) => t !== "review" || pending).map(([t]) => t);
  // Arrow keys move between the usable tabs, selecting as they go.
  const arrow = (key: string) => {
    const step = key === "ArrowRight" ? 1 : key === "ArrowLeft" ? -1 : 0;
    if (!step) return;
    const next = usable[(usable.indexOf(shown) + step + usable.length) % usable.length]!;
    setTab(next);
    document.getElementById(tabId(next))?.focus();
  };

  return (
    <div className="forge">
      {on && (
        <div className="forge-tabs">
          <div role="tablist" aria-label="Forge" onKeyDown={(e) => arrow(e.key)}>
            {tabs.map(([t, label]) => (
              <button
                key={t}
                id={tabId(t)}
                role="tab"
                aria-selected={shown === t}
                aria-controls="forge-tabpanel"
                tabIndex={shown === t ? 0 : -1}
                className={shown === t ? "on" : undefined}
                disabled={!usable.includes(t)}
                onClick={() => setTab(t)}
              >
                {label}
              </button>
            ))}
          </div>
          <button className="button primary forge-open" onClick={() => open()} title={`${verb} (Ctrl+K)`}>
            <Hammer /> {verb} <kbd>⌘K</kbd>
          </button>
        </div>
      )}
      {forge.runs.map((r) => (
        <RunHeader key={r.id} run={r} label={label} onStop={() => forge.stop(r.id)} onDismiss={() => forge.settle(r.id)} />
      ))}
      {forge.waiting.map((w) => (
        <WaitingHeader key={w.key} w={w} label={label} onCancel={() => forge.unwait(w.key)} />
      ))}

      <div id="forge-tabpanel" role={on ? "tabpanel" : undefined} aria-labelledby={on ? tabId(shown) : undefined}>
      {shown === "start" && <Start forge={forge} avatar={props.avatar} />}
      {shown === "fields" && (on ? props.fields(forge.lock, extra) : props.fields())}
      {shown === "review" && pending && proposed && (
        <Review
          key={pending.id}
          title={pending.field ? `${pending.label} on ${label(pending.field)}` : pending.label}
          proposed={proposed}
          warnings={pending.result?.warnings}
          instruction={pending.instruction}
          stale={changedKeys(pending.start, data).length ? kind : undefined}
          saves={!!props.save}
          preview={(changes) => forge.subject.preview({ ...data, ...changes })}
          onApply={(changes) => {
            if (props.save) props.save(changes);
            else for (const [k, v] of Object.entries(changes)) props.edit(k, v);
            forge.settlePending();
            setTab("fields");
          }}
          onDiscard={() => {
            forge.settlePending();
            setTab("fields");
          }}
        />
      )}
      {shown === "report" && (
        <>
          {forge.report && (
            <Report key={forge.reportId} report={forge.report} subject={forge.subject} changed={changedKeys(forge.reportStart ?? data, data)} onRefine={revise && ((text) => forge.start({ action: revise, instruction: text }))} />
          )}
          <div className="forge-empty">
            {!forge.report && <p>No report yet.</p>}
            {forge.mine
              .filter((a) => a.applies === kind && a.report_only)
              .map((a) => (
                <button key={a.id} className={forge.report ? "button" : "button primary"} onClick={() => (asks(a) ? open({ action: a }) : forge.start({ action: a }))}>
                  <Hammer /> {forge.report ? `${a.label} again` : a.label}
                </button>
              ))}
          </div>
        </>
      )}
      </div>

      {forge.repeat && (
        <ConfirmDialog
          title="A proposal from this run is waiting"
          body={<p>Review already has a proposal from the same {kind} and instruction.</p>}
          confirmLabel="Run again"
          cancelLabel="Open Review"
          onConfirm={forge.runAgain}
          onCancel={() => setTab("review")}
          onClose={forge.unrepeat}
        />
      )}
      {panel && (
        <Reforge
          forge={forge}
          name={name}
          verb={verb}
          target={target}
          setTarget={setTarget}
          instruction={instruction}
          setInstruction={setInstruction}
          avatar={props.avatar}
          onClose={() => setPanel(false)}
        />
      )}
    </div>
  );
}

/** Badges counts a field's findings per severity; each opens the Report at them. */
function Badges({ report, field, label, onOpen }: { report: ReportT; field: string; label: (key: string) => string; onOpen: () => void }) {
  const mine = report.findings?.filter((f) => f.fields?.includes(field)) ?? [];
  return severities.map((s) => {
    const n = mine.filter((f) => f.severity === s).length;
    return (
      n > 0 && (
        <button key={s} className={`forge-badge ${s}`} aria-label={`${n} ${s} finding${n === 1 ? "" : "s"} on ${label(field)}`} onClick={onOpen}>
          {n > 1 ? `${n} ${s}` : s}
        </button>
      )
    );
  });
}

/** FieldMenu is a field's hammer menu of field actions. */
function FieldMenu(props: { field: string; label: (key: string) => string; actions: Action[]; disabled: boolean; onPick: (a: Action) => void }) {
  const { field, actions, disabled, onPick } = props;
  const name = props.label(field);
  const [open, setOpen] = useState(false);
  const trigger = useRef<HTMLButtonElement>(null);
  const menu = useRef<HTMLSpanElement>(null);
  useEffect(() => {
    if (open) menu.current?.querySelector("button")?.focus();
  }, [open]);
  if (!actions.length) return null;
  const close = () => {
    setOpen(false);
    trigger.current?.focus();
  };
  const move = (e: ReactKeyboardEvent<HTMLSpanElement>) => {
    const items = [...e.currentTarget.querySelectorAll("button")];
    const at = items.indexOf(document.activeElement as HTMLButtonElement);
    const to = { ArrowDown: at + 1, ArrowUp: at - 1, Home: 0, End: items.length - 1 }[e.key];
    if (e.key === "Escape") close();
    else if (to !== undefined) items[(to + items.length) % items.length]?.focus();
    else return;
    e.preventDefault();
  };
  return (
    <span className="forge-menu" onBlur={(e) => !e.currentTarget.contains(e.relatedTarget) && setOpen(false)}>
      <button
        ref={trigger}
        className="button"
        aria-label={`Forge actions for ${name}`}
        aria-haspopup="menu"
        aria-expanded={open}
        disabled={disabled}
        onClick={(e) => {
          // The menu sits in the field's summary, which would fold the field.
          e.preventDefault();
          setOpen((o) => !o);
        }}
      >
        <Hammer />
      </button>
      {open && (
        <span ref={menu} className="forge-pop" role="menu" aria-label={`Forge actions for ${name}`} onKeyDown={move}>
          {actions.map((a) => (
            <button
              key={`${a.instance_id}/${a.id}`}
              role="menuitem"
              tabIndex={-1}
              title={a.description}
              onClick={(e) => {
                e.preventDefault();
                close();
                onPick(a);
              }}
            >
              {a.label}
            </button>
          ))}
        </span>
      )}
    </span>
  );
}

/** Start is a new card's one form: a concept, the new action's inputs and images, and Forge. */
function Start({ forge, avatar }: { forge: ForgeState; avatar?: Blob | string }) {
  const [concept, setConcept] = useState<Instruction>(emptyInstruction);
  const [options, setOptions] = useState<Options>(noOptions);
  const mentions = useMentions(concept, forge.instance?.id, forge.library);
  const action = newAction(forge.mine);
  if (!action) return <p className="muted">This model setup has no action for a new card. Pick another, or type the fields by hand.</p>;
  const ready = (!action.instruction || concept.text.trim() !== "") && blocking(mentions).length === 0;
  const run = () =>
    forge.start({
      action,
      instruction: action.instruction ? concept.text : undefined,
      inputs: options.inputs,
      context: contextOf(mentions),
      images: imagesOf(action, options, avatar),
    });
  return (
    <div className="forge-start">
      <p className="muted">Describe the card and Forge writes it, or skip this and type the fields by hand.</p>
      {action.instruction && (
        <MentionBox
          label="Concept"
          placeholder="Describe the character… @lorebooks/Northern Reach for the setting"
          value={concept}
          onChange={setConcept}
          mentions={mentions}
          instance={forge.instance?.id}
          library={forge.library}
          onEnter={() => ready && run()}
        />
      )}
      <ActionOptions action={action} value={options} onChange={setOptions} hasAvatar={!!avatar} />
      <div className="forge-footer">
        <ModelPicker forge={forge} />
        <button className="button primary" disabled={!ready} onClick={run}>
          <Hammer /> Forge
        </button>
      </div>
    </div>
  );
}
