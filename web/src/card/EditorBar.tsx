import { useQuery } from "@tanstack/react-query";
import { useState, type ReactNode } from "react";
import type { Kind } from "../api/client";
import { librariesQuery } from "../api/queries";
import { MoveLibrary } from "../cards/MoveLibrary";
import { Menu } from "../shell/Menu";
import { useSaveShortcut } from "../shell/useSaveShortcut";
import type { Entity } from "./Card";
import { Archive, Name, Push } from "./Panel";
import type { Editor } from "./useEditor";

type EditorBarProps = {
  kind: Kind;
  entity: Entity;
  ed: Editor;
  lock: (key: string) => string | undefined;
  /** busy holds Save while another write to the entity, such as an image change, is in flight. */
  busy?: boolean;
  /** menu is the overflow items listed above Archive. */
  menu?: ReactNode;
};

/** EditorBar is the card and lorebook editors' header: the name, unsaved count, Push, Save, and an overflow menu. */
export function EditorBar({ kind, entity, ed, lock, busy = false, menu }: EditorBarProps) {
  const [moving, setMoving] = useState(false);
  const libraries = useQuery(librariesQuery);
  const unsaved = ed.dirty.size;
  const canSave = unsaved > 0 && !ed.save.isPending && !ed.stale && !busy;
  const save = () => ed.draft && ed.save.mutate(ed.draft);
  useSaveShortcut(canSave, save);
  return (
    <div className="editor-bar">
      <Name data={ed.data} edit={ed.edit} lock={lock} />
      {unsaved > 0 && (
        <span className="editor-unsaved">
          {unsaved} unsaved {unsaved === 1 ? "field" : "fields"}
        </span>
      )}
      <Push kind={kind} id={entity.id} dirty={unsaved > 0} />
      <button className="button primary" disabled={!canSave} onClick={save} aria-keyshortcuts="Meta+S Control+S">
        Save <kbd aria-hidden="true">⌘S</kbd>
      </button>
      <Menu label="More actions">
        {menu}
        {/* The move refuses an archived card or lorebook, and needs another library to go to. */}
        {!entity.archived_at && (libraries.data?.length ?? 0) > 1 && <button onClick={() => setMoving(true)}>Move to library…</button>}
        {/* Archive's unpublish checkbox and error need the menu to stay open on a click. */}
        <div className="menu-archive" onClick={(e) => e.stopPropagation()}>
          <Archive kind={kind} entity={entity} />
        </div>
      </Menu>
      {moving && (
        <MoveLibrary kind={kind} ids={[entity.id]} library={entity.library_id} onClose={() => setMoving(false)} onDone={() => setMoving(false)} />
      )}
    </div>
  );
}
