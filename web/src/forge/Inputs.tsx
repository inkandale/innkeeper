import { useEffect, useState } from "react";
import { SchemaForm, type Schema } from "../settings/SchemaForm";
import type { Action } from "./actions";
import type { Forge, RunRequest } from "./useForge";

/** Options are a run's inputs and images, as the owner sets them before Run. */
export type Options = { inputs?: unknown; reference?: File; sendAvatar: boolean };

export const noOptions: Options = { sendAvatar: false };

const hasProperties = (s: unknown) => !!s && typeof s === "object" && Object.keys((s as Schema).properties ?? {}).length > 0;

/** asks reports whether an action takes anything before it runs: an instruction or inputs. */
export const asks = (a: Action) => !!a.instruction || hasProperties(a.inputs);

/** imagesOf is what a run sends as images: only for an action that takes them. avatar is the current avatar. */
export function imagesOf(a: Action, o: Options, avatar: Blob | string | undefined): RunRequest["images"] {
  if (!a.images) return undefined;
  return { reference: o.reference, avatar: o.sendAvatar ? avatar : undefined };
}

/** ActionOptions renders an action's inputs, and for an action that takes images, a reference image and the avatar. */
export function ActionOptions({ action, value, onChange, hasAvatar }: { action: Action; value: Options; onChange: (o: Options) => void; hasAvatar: boolean }) {
  return (
    <>
      {hasProperties(action.inputs) && (
        <SchemaForm schema={action.inputs as Schema} label="" value={value.inputs} onChange={(inputs) => onChange({ ...value, inputs })} />
      )}
      {action.images && (
        <div className="forge-images">
          <ImageDrop file={value.reference} onPick={(reference) => onChange({ ...value, reference })} />
          {hasAvatar && (
            <label className="check">
              <input type="checkbox" checked={value.sendAvatar} onChange={(e) => onChange({ ...value, sendAvatar: e.target.checked })} /> Send the avatar too
            </label>
          )}
        </div>
      )}
    </>
  );
}

/** ImageDrop picks a reference image for the model, by drop or by file picker. */
function ImageDrop({ file, onPick }: { file?: File; onPick: (f: File) => void }) {
  const [over, setOver] = useState(false);
  const [src, setSrc] = useState<string>();
  useEffect(() => {
    if (!file) return;
    const url = URL.createObjectURL(file);
    setSrc(url);
    return () => URL.revokeObjectURL(url);
  }, [file]);
  const take = (f?: File) => f?.type.startsWith("image/") && onPick(f);
  return (
    <label
      className={over ? "forge-drop over" : "forge-drop"}
      onDragOver={(e) => {
        e.preventDefault();
        setOver(true);
      }}
      onDragLeave={() => setOver(false)}
      onDrop={(e) => {
        e.preventDefault();
        setOver(false);
        take(e.dataTransfer.files[0]);
      }}
    >
      {file && src ? <img src={src} alt="Reference" /> : <span className="muted">Reference image: drop one here, or click to pick</span>}
      <input
        type="file"
        accept="image/png,image/jpeg,image/webp"
        aria-label="Reference image"
        className="visually-hidden"
        onChange={(e) => take(e.target.files?.[0])}
      />
    </label>
  );
}

/** ModelPicker chooses the model setup: the action instance runs go to. */
export function ModelPicker({ forge }: { forge: Forge }) {
  if (forge.instances.length < 2) return <span className="muted">{forge.instance?.name}</span>;
  return (
    <select className="input forge-instance" aria-label="Model setup" value={forge.instance?.id} onChange={(e) => forge.setInstance(Number(e.target.value))}>
      {forge.instances.map((i) => (
        <option key={i.id} value={i.id}>
          {i.name}
        </option>
      ))}
    </select>
  );
}
