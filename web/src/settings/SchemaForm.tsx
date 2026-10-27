import { createContext, useContext, useEffect, useId, useRef, useState, type ReactNode } from "react";

/** Schema is the part of JSON Schema the form renders. */
export type Schema = {
  type?: string;
  title?: string;
  description?: string;
  enum?: unknown[];
  default?: unknown;
  /** Any media type marks a string as long text, edited in a multi-line box. */
  contentMediaType?: string;
  /** "instance" with x-offers makes an integer a picker of instances offering that service. */
  format?: string;
  "x-offers"?: string;
  /** The plugin lists this string's choices with settings.options. */
  "x-options"?: boolean;
  items?: Schema;
  properties?: Record<string, Schema>;
  required?: string[];
};

/** FormHelpers is what fields need from the form around them. */
export type FormHelpers = {
  /** Defaults by JSON pointer: the schema's and the plugin's. */
  defaults: Record<string, unknown>;
  /** Pointers whose override replaced a default that has since changed. */
  updated: Set<string>;
  loadOptions?: (pointer: string) => Promise<{ value: string; label?: string }[]>;
  instances?: (service: string) => { id: number; label: string }[];
};

export const FormContext = createContext<FormHelpers>({ defaults: {}, updated: new Set() });

type Props = {
  schema: Schema;
  /** label is the property name, used when the schema has no title. */
  label: string;
  value: unknown;
  onChange: (v: unknown) => void;
  required?: boolean;
  /** path is the field's JSON pointer, "" for the root. */
  path?: string;
};

const fits: Record<string, (v: unknown) => boolean> = {
  string: (v) => typeof v === "string",
  number: (v) => typeof v === "number",
  integer: (v) => Number.isInteger(v),
  boolean: (v) => typeof v === "boolean",
  object: (v) => typeof v === "object" && v !== null && !Array.isArray(v),
  array: Array.isArray,
};

/** pointerKey escapes a property name for a JSON pointer. */
export const pointerKey = (k: string) => k.replaceAll("~", "~0").replaceAll("/", "~1");
/** unpointerKey undoes pointerKey. */
export const unpointerKey = (k: string) => k.replaceAll("~1", "/").replaceAll("~0", "~");

/** same compares JSON values, ignoring the order of a string array (a set of choices); config values have no cycles. */
const same = (a: unknown, b: unknown) => JSON.stringify(unordered(a)) === JSON.stringify(unordered(b));
const unordered = (v: unknown) => (Array.isArray(v) && v.every((e) => typeof e === "string") ? [...v].sort() : v);

/**
 * SchemaForm renders inputs for a JSON Schema: string (long text with contentMediaType), number, integer,
 * boolean, string enum, an array of string enum (checkboxes), an instance picker, and objects with properties.
 * Anything else is raw JSON. A field with a default shows it until overridden, and offers a reset once it is.
 */
export function SchemaForm({ schema, label, value, onChange, required, path = "" }: Props) {
  const helpers = useContext(FormContext);
  // Above the object branch's early return, so every render calls the same hooks.
  const [options, setOptions] = useState<{ value: string; label?: string }[]>([]);
  const [optionsError, setOptionsError] = useState<string>();
  const [loadingOptions, setLoadingOptions] = useState(false);
  // Only the latest lookup's answer is shown.
  const lookup = useRef(0);
  const listId = useId();
  const title = schema.title ?? label;
  const t = schema.type ?? "";
  // A stored value of another type is shown as JSON rather than lost.
  const fit = fits[t];
  // Decided once, so a field doesn't swap kinds mid-typing, as 1.5 in an integer field would.
  const [fitsAtMount] = useState(() => value === undefined || fit?.(value) === true);
  const known = fit !== undefined && fitsAtMount;
  const candidates =
    known && t === "integer" && schema.format === "instance" && schema["x-offers"] && helpers.instances
      ? helpers.instances(schema["x-offers"])
      : undefined;
  const sole = required && value === undefined && candidates?.length === 1 ? candidates[0]!.id : undefined;
  // Keyed on sole alone: onChange is new each render, and the only choice is picked once.
  useEffect(() => {
    if (sole !== undefined) onChange(sole);
  }, [sole]);

  if (known && t === "object" && schema.properties) {
    const obj = (value ?? {}) as Record<string, unknown>;
    return (
      <fieldset className="schema-object">
        {title && <legend>{title}</legend>}
        {schema.description && <small className="muted">{schema.description}</small>}
        {Object.entries(schema.properties).map(([k, s]) => (
          <SchemaForm
            key={k}
            schema={s}
            label={k}
            value={obj[k]}
            required={schema.required?.includes(k)}
            path={`${path}/${pointerKey(k)}`}
            onChange={(v) => {
              const { [k]: _, ...rest } = obj;
              const next = v === undefined ? rest : { ...rest, [k]: v };
              // An emptied object is absent, so clearing its fields isn't a config change.
              onChange(Object.keys(next).length > 0 ? next : undefined);
            }}
          />
        ))}
      </fieldset>
    );
  }

  const def = helpers.defaults[path];
  const hasDefault = def !== undefined;
  const overridden = hasDefault && value !== undefined;
  const shown = value ?? def;
  // Typing the default back is no override.
  const set = (v: unknown) => onChange(hasDefault && v !== undefined && same(v, def) ? undefined : v);
  // With a default, emptying a field is an override; Reset is the way back to the default.
  const emptied = (empty: unknown) => (hasDefault ? empty : undefined);
  const loadOptions = schema["x-options"] && known && t === "string" ? helpers.loadOptions : undefined;

  let control: ReactNode;
  if (candidates) {
    control = (
      <>
        <select className="input" required={required} value={(shown as number) ?? ""} onChange={(e) => set(e.target.value ? Number(e.target.value) : undefined)}>
          <option value="">—</option>
          {candidates.map((i) => (
            <option key={i.id} value={i.id}>
              {i.label}
            </option>
          ))}
        </select>
        {candidates.length === 0 && (
          <small className="muted">
            Nothing set up offers this yet. <a href="/settings/plugins">Set one up in Settings › Plugins</a>, then come back.
          </small>
        )}
      </>
    );
  } else if (known && t === "array" && schema.items?.type === "string" && schema.items.enum?.every((e) => typeof e === "string")) {
    const choices = schema.items.enum as string[];
    const picked = (shown as string[] | undefined) ?? [];
    return (
      <fieldset className="form-field checks">
        <legend>{title}</legend>
        {choices.map((choice) => (
          <label key={choice} className="check">
            <input
              type="checkbox"
              checked={picked.includes(choice)}
              onChange={(e) => {
                const next = choices.filter((c) => (c === choice ? e.target.checked : picked.includes(c)));
                set(next.length > 0 ? next : emptied([]));
              }}
            />
            <span>{choice}</span>
          </label>
        ))}
        {overridden && <ResetButton title={title} onReset={() => onChange(undefined)} />}
        {overridden && helpers.updated.has(path) && <small className="muted">Default updated</small>}
        {schema.description && <small className="muted">{schema.description}</small>}
      </fieldset>
    );
  } else if (known && t === "string" && schema.enum?.every((e) => typeof e === "string")) {
    control = (
      <select className="input" required={required} value={(shown as string) ?? ""} onChange={(e) => set(e.target.value || undefined)}>
        <option value="">—</option>
        {(schema.enum as string[]).map((e) => (
          <option key={e}>{e}</option>
        ))}
      </select>
    );
  } else if (known && t === "string" && schema.contentMediaType) {
    control = <textarea className="input" rows={8} required={required} value={(shown as string) ?? ""} onChange={(e) => set(e.target.value || emptied(""))} />;
  } else if (known && t === "string") {
    control = (
      <input
        className="input"
        required={required}
        list={loadOptions ? listId : undefined}
        value={(shown as string) ?? ""}
        onChange={(e) => set(e.target.value || emptied(""))}
      />
    );
  } else if (known && (t === "number" || t === "integer")) {
    control = (
      <input
        className="input"
        type="number"
        step={t === "integer" ? 1 : "any"}
        required={required}
        value={(shown as number) ?? ""}
        onChange={(e) => set(e.target.value === "" ? undefined : Number(e.target.value))}
      />
    );
  } else if (known && t === "boolean") {
    control = <input type="checkbox" checked={shown === true} onChange={(e) => set(e.target.checked)} />;
  } else {
    control = <RawJson value={value} onChange={onChange} required={required} />;
  }
  const load = (fetchOptions: NonNullable<FormHelpers["loadOptions"]>) => {
    const id = ++lookup.current;
    setLoadingOptions(true);
    fetchOptions(path)
      .then(
        (o) => {
          if (id !== lookup.current) return;
          setOptions(o);
          setOptionsError(undefined);
        },
        (e: unknown) => {
          if (id !== lookup.current) return;
          setOptionsError(String((e as Error | undefined)?.message ?? e));
        },
      )
      .finally(() => id === lookup.current && setLoadingOptions(false));
  };
  return (
    <div className="form-field">
      <label className={t === "boolean" && known ? "check" : undefined}>
        <span>{title}</span>
        {control}
      </label>
      {loadOptions && (
        <>
          <datalist id={listId}>
            {options.map((o) => (
              <option key={o.value} value={o.value} label={o.label} />
            ))}
          </datalist>
          <button
            type="button"
            className="button"
            aria-label={`Load choices for ${title}`}
            disabled={loadingOptions}
            aria-busy={loadingOptions}
            onClick={() => load(loadOptions)}
          >
            Load choices
          </button>
          {optionsError && (
            <small className="error" role="alert">
              {optionsError}
            </small>
          )}
        </>
      )}
      {overridden && <ResetButton title={title} onReset={() => onChange(undefined)} />}
      {overridden && helpers.updated.has(path) && <small className="muted">Default updated</small>}
      {schema.description && <small className="muted">{schema.description}</small>}
    </div>
  );
}

/** ResetButton drops a field's override, so it shows its default again. */
function ResetButton({ title, onReset }: { title: string; onReset: () => void }) {
  return (
    <button type="button" className="button reset" aria-label={`Reset ${title}`} title="Back to the default" onClick={onReset}>
      Reset
    </button>
  );
}

/** RawJson edits any value as JSON. Text that doesn't parse marks the field invalid and isn't passed on. */
function RawJson({ value, onChange, required }: { value: unknown; onChange: (v: unknown) => void; required?: boolean }) {
  const [text, setText] = useState(value === undefined ? "" : JSON.stringify(value, null, 2));
  return (
    <textarea
      className="input"
      rows={3}
      required={required}
      value={text}
      onChange={(e) => {
        const box = e.target;
        setText(box.value);
        box.setCustomValidity("");
        if (box.value.trim() === "") return onChange(undefined);
        try {
          onChange(JSON.parse(box.value));
        } catch {
          box.setCustomValidity("Not valid JSON");
        }
      }}
    />
  );
}
