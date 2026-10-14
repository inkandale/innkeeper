import { useState } from "react";

/** ChipInput edits a list of strings as chips. Enter, comma, or leaving the input adds; × removes; Backspace on an empty input removes the last. */
export function ChipInput({ label, values, onChange }: { label: string; values: string[]; onChange: (v: string[]) => void }) {
  const [typed, setTyped] = useState("");
  const add = () => {
    const v = typed.trim();
    if (v && !values.some((x) => x.toLowerCase() === v.toLowerCase())) onChange([...values, v]);
    setTyped("");
  };
  return (
    <div className="chip-input" role="group" aria-label={label}>
      {values.map((v, i) => (
        <span key={i} className="tag">
          {v}
          <button className="chip-x" aria-label={`Remove ${v} from ${label}`} onClick={() => onChange(values.filter((_, j) => j !== i))}>
            ×
          </button>
        </span>
      ))}
      <input
        className="input"
        aria-label={`Add to ${label}`}
        placeholder={label}
        value={typed}
        onChange={(e) => setTyped(e.target.value)}
        onBlur={add}
        onKeyDown={(e) => {
          if (e.key === "Enter" || e.key === ",") {
            e.preventDefault();
            add();
          } else if (e.key === "Backspace" && !typed && values.length) {
            onChange(values.slice(0, -1));
          }
        }}
      />
    </div>
  );
}
