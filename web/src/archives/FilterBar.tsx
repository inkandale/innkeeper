import type { CatalogFilter, FilterValues } from "./archive";

/** FilterBar shows an input for each filter a catalog declares. An emptied filter is dropped, since the catalog refuses empty values. */
export function FilterBar({ filters, value, onChange }: { filters: CatalogFilter[]; value: FilterValues; onChange: (v: FilterValues) => void }) {
  const set = (name: string, v: unknown) => {
    const next = { ...value };
    if (v === undefined) delete next[name];
    else next[name] = v;
    onChange(next);
  };
  if (!filters.length) return null;
  return (
    <div className="filter-bar" role="group" aria-label="Filters">
      {filters.map((f) => (
        <FilterInput key={f.name} filter={f} value={value[f.name]} set={(v) => set(f.name, v)} />
      ))}
    </div>
  );
}

type InputProps = { filter: CatalogFilter; value: unknown; set: (v: unknown) => void };

function FilterInput({ filter, value, set }: InputProps) {
  switch (filter.type) {
    case "enum":
      return (
        <select className="input" aria-label={filter.label} value={typeof value === "string" ? value : ""} onChange={(e) => set(e.target.value || undefined)}>
          <option value="">{filter.label}: any</option>
          {(filter.options ?? []).map((o) => (
            <option key={o.value} value={o.value}>
              {o.label}
            </option>
          ))}
        </select>
      );
    case "text":
      return <TextFilter filter={filter} value={value} set={set} />;
    case "date_range":
      return <RangeFilter filter={filter} value={value} set={set} ends={["from", "to"]} type="date" />;
    case "number_range":
      return <RangeFilter filter={filter} value={value} set={set} ends={["min", "max"]} type="number" />;
    default:
      return null;
  }
}

// A text filter applies on Enter or leaving the box, not on every keystroke, so typing doesn't search per letter.
function TextFilter({ filter, value, set }: InputProps) {
  const current = typeof value === "string" ? value : "";
  const apply = (s: string) => s.trim() !== current && set(s.trim() || undefined);
  return (
    <input
      key={current}
      className="input"
      aria-label={filter.label}
      placeholder={filter.label}
      defaultValue={current}
      onBlur={(e) => apply(e.target.value)}
      onKeyDown={(e) => e.key === "Enter" && apply(e.currentTarget.value)}
    />
  );
}

function RangeFilter({ filter, value, set, ends, type }: InputProps & { ends: [string, string]; type: "date" | "number" }) {
  const range = (value ?? {}) as Record<string, unknown>;
  const setEnd = (end: string, raw: string) => {
    const next = { ...range };
    if (raw === "") delete next[end];
    else next[end] = type === "number" ? Number(raw) : raw;
    set(Object.keys(next).length ? next : undefined);
  };
  return (
    <span className="range">
      {ends.map((end) => (
        <input
          key={end}
          className="input"
          type={type}
          aria-label={`${filter.label} ${end}`}
          placeholder={`${filter.label} ${end}`}
          value={range[end] === undefined ? "" : String(range[end])}
          onChange={(e) => setEnd(end, e.target.value)}
        />
      ))}
    </span>
  );
}
