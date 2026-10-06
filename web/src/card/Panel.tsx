import { Link } from "react-router";
import { thumb } from "../api/client";
import type { Entity } from "./Card";
import { text, type Data } from "./draft";

export type PanelProps = {
  card: Entity;
  data: Data;
  edit: (key: string, value: unknown) => void;
  dirty: boolean;
  saving: boolean;
  onSave: () => void;
};

/** Panel is the card page's left side: portrait, name, and actions. */
export function Panel({ card, data, edit, dirty, saving, onSave }: PanelProps) {
  const avatar = card.revision.avatar;
  return (
    <aside className="card-aside">
      {avatar ? <img className="portrait" src={thumb(avatar, 640)} alt="" /> : <div className="portrait" />}
      <input className="input name" aria-label="Name" value={text(data.name)} onChange={(e) => edit("name", e.target.value)} />
      <input className="input" aria-label="Creator" placeholder="Creator" value={text(data.creator)} onChange={(e) => edit("creator", e.target.value)} />
      <div className="row">
        <button className="button primary" disabled={!dirty || saving} onClick={onSave}>
          Save
        </button>
      </div>
      <Link to={`/cards/${card.id}/history`}>All history</Link>
    </aside>
  );
}
