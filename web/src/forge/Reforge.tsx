import { useState } from "react";
import { fieldLabel } from "../card/Fields";
import { useModal } from "../shell/useModal";
import { instructionAction, type Action } from "./actions";
import { Hammer } from "./Hammer";
import { ActionOptions, asks, imagesOf, ModelPicker, noOptions, type Options } from "./Inputs";
import { blocking, emptyInstruction, MentionBox, useMentions, type Instruction } from "./MentionBox";
import { contextOf } from "./mentions";
import type { Forge } from "./useForge";

/** Target is the action Run runs, and the field for a field action. */
export type Target = { action: Action; field?: string };

/**
 * Reforge is the panel for changing the whole card: the instruction box, which runs the card's revise action, and the
 * other whole-card actions. target, when set, is another action to run instead, such as a field action with inputs.
 */
export function Reforge(props: {
  forge: Forge;
  name: string;
  target?: Target;
  setTarget: (t?: Target) => void;
  instruction: Instruction;
  setInstruction: (i: Instruction) => void;
  avatar?: Blob | string;
  onClose: () => void;
}) {
  const { forge, target, instruction } = props;
  const modal = useModal(props.onClose);
  const revise = instructionAction(forge.mine);
  const current: Target | undefined = target ?? (revise && { action: revise });
  const others = forge.mine.filter((a) => a.applies === "card" && !a.fields?.length && a !== revise);
  const mentions = useMentions(instruction, forge.instance?.id);
  const [options, setOptions] = useState<Options>(noOptions);
  // The panel stays open, with its instruction, until the run has started.
  const [starting, setStarting] = useState(false);
  const choose = (t?: Target) => {
    props.setTarget(t);
    setOptions(noOptions);
  };
  const takesText = !!current?.action.instruction;
  const ready = !!current && !starting && (!takesText || (instruction.text.trim() !== "" && blocking(mentions).length === 0));
  const run = async (t: Target, o: Options) => {
    setStarting(true);
    const started = await forge.start({
      action: t.action,
      field: t.field,
      instruction: t.action.instruction ? instruction.text : undefined,
      inputs: o.inputs,
      context: t.action.instruction ? contextOf(mentions) : undefined,
      images: imagesOf(t.action, o, props.avatar),
    });
    setStarting(false);
    if (!started) return;
    if (t.action.instruction) props.setInstruction(emptyInstruction);
    props.onClose();
  };
  return (
    <dialog className="forge-panel" aria-label={`Reforge ${props.name}`} {...modal}>
      <div className="forge-panel-head">
        <Hammer />
        <strong>Reforge {props.name || "this card"}</strong>
        {target && (
          <span className="forge-target muted">
            Runs <strong>{target.action.label}</strong>
            {target.field && ` on ${fieldLabel(target.field)}`}
            <button className="forge-x" aria-label="Back to the instruction" onClick={() => choose(undefined)}>
              ×
            </button>
          </span>
        )}
      </div>
      {takesText && (
        <MentionBox
          label="Instruction"
          placeholder="Reforge this card… e.g. make the family robots · @cards/Billy is the brother"
          value={instruction}
          onChange={props.setInstruction}
          mentions={mentions}
          instance={forge.instance?.id}
          autoFocus
          onEnter={() => current && ready && void run(current, options)}
        />
      )}
      {current && <ActionOptions action={current.action} value={options} onChange={setOptions} hasAvatar={!!props.avatar} />}
      {others.length > 0 && (
        <>
          <div className="forge-group">Or change the whole card</div>
          <ul className="plain forge-presets">
            {others.map((a) => (
              <li key={a.id}>
                <button className={target?.action.id === a.id ? "on" : undefined} disabled={starting} onClick={() => (asks(a) || a.images ? choose({ action: a }) : void run({ action: a }, noOptions))}>
                  <strong>{a.label}</strong>
                  <span className="muted">{a.description}</span>
                </button>
              </li>
            ))}
          </ul>
        </>
      )}
      <div className="forge-panel-foot">
        <ModelPicker forge={forge} />
        <span className="muted">{takesText ? "@ adds cards or lorebooks · Shift+Enter for a new line · Esc to close" : "Esc to close"}</span>
        <button className="button primary" disabled={!ready} onClick={() => current && void run(current, options)}>
          <Hammer /> Run
        </button>
      </div>
    </dialog>
  );
}
