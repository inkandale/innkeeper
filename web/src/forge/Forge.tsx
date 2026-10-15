import { useEffect, useRef, useState, type KeyboardEvent as ReactKeyboardEvent } from "react";
import { fieldLabel, Fields } from "../card/Fields";
import type { Data } from "../card/draft";
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

/**
 * Forge is the card page's main column: the Fields tab, and with an action plugin, Review, Report, the Reforge panel,
 * and the runs. A new card, without cardId, has a Start tab instead of Report. avatar is the current avatar: a blob
 * SHA, or a new card's file.
 */
export function Forge(props: {
  forge: ForgeState;
  isNew?: boolean;
  data: Data;
  dirty: Set<string>;
  edit: (key: string, value: unknown) => void;
  avatar?: Blob | string;
}) {
  const { forge, data, isNew } = props;
  const [tab, setTab] = useState<Tab>();
  const [panel, setPanel] = useState(false);
  const [target, setTarget] = useState<Target>();
  const [instruction, setInstruction] = useState<Instruction>(emptyInstruction);
  const on = forge.actions.length > 0;
  const name = typeof data.name === "string" ? data.name : "";

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
      {forge.report && <Badges report={forge.report} field={key} onOpen={() => toReport(key)} />}
      <FieldMenu
        field={key}
        // A new card has no Report tab to show a report-only action's result.
        actions={forge.mine.filter((a) => a.fields?.includes(key) && !(isNew && a.report_only))}
        disabled={!!forge.lock(key)}
        onPick={(a) => pickField(a, key)}
      />
    </>
  );

  const pending = forge.pending;
  const reviewCount = pending ? changedKeys(pending.start, pending.result?.proposal ?? {}).length : 0;
  const tabs: [Tab, string][] = isNew
    ? [["start", "Start"], ["fields", "Fields"], ["review", `Review${pending ? ` (${reviewCount})` : ""}`]]
    : [["fields", "Fields"], ["review", `Review${pending ? ` (${reviewCount})` : ""}`], ["report", "Report"]];
  const revise = instructionAction(forge.mine);
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
          <button className="button primary forge-open" onClick={() => open()} title={isNew ? "Forge (Ctrl+K)" : "Reforge (Ctrl+K)"}>
            <Hammer /> {isNew ? "Forge" : "Reforge"} <kbd>⌘K</kbd>
          </button>
        </div>
      )}
      {forge.runs.map((r) => (
        <RunHeader key={r.id} run={r} onStop={() => forge.stop(r.id)} onDismiss={() => forge.settle(r.id)} />
      ))}
      {forge.waiting.map((w) => (
        <WaitingHeader key={w.key} w={w} onCancel={() => forge.unwait(w.key)} />
      ))}

      <div id="forge-tabpanel" role={on ? "tabpanel" : undefined} aria-labelledby={on ? tabId(shown) : undefined}>
      {shown === "start" && <Start forge={forge} avatar={props.avatar} />}
      {shown === "fields" && <Fields data={data} dirty={props.dirty} edit={props.edit} lock={on ? forge.lock : undefined} extra={on ? extra : undefined} />}
      {shown === "review" && pending?.result?.proposal && (
        <Review
          key={pending.id}
          title={pending.field ? `${pending.label} on ${fieldLabel(pending.field)}` : pending.label}
          start={pending.start}
          draft={data}
          proposal={pending.result.proposal}
          warnings={pending.result.warnings}
          onApply={(changes) => {
            for (const [k, v] of Object.entries(changes)) props.edit(k, v);
            forge.settle(pending.id);
            setTab("fields");
          }}
          onDiscard={() => {
            forge.settle(pending.id);
            setTab("fields");
          }}
        />
      )}
      {shown === "report" &&
        (forge.report ? (
          <Report key={forge.reportId} report={forge.report} onRefine={revise && ((text) => forge.start({ action: revise, instruction: text }))} />
        ) : (
          <div className="forge-empty">
            <p>No report yet.</p>
            {forge.mine
              .filter((a) => a.applies === "card" && a.report_only)
              .map((a) => (
                <button key={a.id} className="button primary" onClick={() => (asks(a) ? open({ action: a }) : forge.start({ action: a }))}>
                  <Hammer /> {a.label}
                </button>
              ))}
          </div>
        ))}
      </div>

      {panel && (
        <Reforge
          forge={forge}
          name={name}
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
function Badges({ report, field, onOpen }: { report: ReportT; field: string; onOpen: () => void }) {
  const mine = report.findings?.filter((f) => f.fields?.includes(field)) ?? [];
  return severities.map((s) => {
    const n = mine.filter((f) => f.severity === s).length;
    return (
      n > 0 && (
        <button key={s} className={`forge-badge ${s}`} aria-label={`${n} ${s} finding${n === 1 ? "" : "s"} on ${fieldLabel(field)}`} onClick={onOpen}>
          {n > 1 ? `${n} ${s}` : s}
        </button>
      )
    );
  });
}

/** FieldMenu is a field's hammer menu of field actions. */
function FieldMenu({ field, actions, disabled, onPick }: { field: string; actions: Action[]; disabled: boolean; onPick: (a: Action) => void }) {
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
        aria-label={`Forge actions for ${fieldLabel(field)}`}
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
        <span ref={menu} className="forge-pop" role="menu" aria-label={`Forge actions for ${fieldLabel(field)}`} onKeyDown={move}>
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
  const mentions = useMentions(concept, forge.instance?.id);
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
