import type { Target } from "./PushPreview";
import "./Sync.css";

/** TargetPicker is a checkbox per app that takes pushes. */
export function TargetPicker({ targets, picked, onChange }: { targets: Target[]; picked: number[]; onChange: (ids: number[]) => void }) {
  return (
    <fieldset className="targets" aria-label="Targets">
      {targets.map((t) => (
        <label key={t.id}>
          <input
            type="checkbox"
            checked={picked.includes(t.id)}
            onChange={(e) => onChange(e.target.checked ? [...picked, t.id] : picked.filter((id) => id !== t.id))}
          />{" "}
          {t.label}
        </label>
      ))}
    </fieldset>
  );
}
