import { useState, type ReactNode } from "react";

/** Schema is the part of JSON Schema the form renders. */
export type Schema = {
  type?: string;
  title?: string;
  description?: string;
  enum?: unknown[];
  properties?: Record<string, Schema>;
  required?: string[];
};

type Props = {
  schema: Schema;
  /** label is the property name, used when the schema has no title. */
  label: string;
  value: unknown;
  onChange: (v: unknown) => void;
  required?: boolean;
};

const fits: Record<string, (v: unknown) => boolean> = {
  string: (v) => typeof v === "string",
  number: (v) => typeof v === "number",
  integer: (v) => Number.isInteger(v),
  boolean: (v) => typeof v === "boolean",
  object: (v) => typeof v === "object" && v !== null && !Array.isArray(v),
};

/**
 * SchemaForm renders inputs for a JSON Schema: string, number, integer,
 * boolean, string enum, and objects with properties. Anything else is raw JSON.
 */
export function SchemaForm({ schema, label, value, onChange, required }: Props) {
  const title = schema.title ?? label;
  const t = schema.type ?? "";
  // A stored value of another type is shown as JSON rather than lost.
  const fit = fits[t];
  const known = fit !== undefined && (value === undefined || fit(value));

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
            onChange={(v) => {
              const { [k]: _, ...rest } = obj;
              onChange(v === undefined ? rest : { ...rest, [k]: v });
            }}
          />
        ))}
      </fieldset>
    );
  }

  let control: ReactNode;
  if (known && t === "string" && schema.enum?.every((e) => typeof e === "string")) {
    control = (
      <select className="input" required={required} value={(value as string) ?? ""} onChange={(e) => onChange(e.target.value || undefined)}>
        <option value="">—</option>
        {(schema.enum as string[]).map((e) => (
          <option key={e}>{e}</option>
        ))}
      </select>
    );
  } else if (known && t === "string") {
    control = (
      <input className="input" required={required} value={(value as string) ?? ""} onChange={(e) => onChange(e.target.value || undefined)} />
    );
  } else if (known && (t === "number" || t === "integer")) {
    control = (
      <input
        className="input"
        type="number"
        step={t === "integer" ? 1 : "any"}
        required={required}
        value={(value as number) ?? ""}
        onChange={(e) => onChange(e.target.value === "" ? undefined : Number(e.target.value))}
      />
    );
  } else if (known && t === "boolean") {
    control = <input type="checkbox" checked={value === true} onChange={(e) => onChange(e.target.checked)} />;
  } else {
    control = <RawJson value={value} onChange={onChange} required={required} />;
  }
  return (
    <div className="form-field">
      <label className={t === "boolean" && known ? "check" : undefined}>
        <span>{title}</span>
        {control}
      </label>
      {schema.description && <small className="muted">{schema.description}</small>}
    </div>
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
