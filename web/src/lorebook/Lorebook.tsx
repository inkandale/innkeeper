import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { useFollowLibrary } from "../shell/library";
import { Link, useParams } from "react-router";
import { namesQuery } from "../api/queries";
import { Stale } from "../card/Conflict";
import { text, tokens } from "../card/draft";
import { Section } from "../card/Fields";
import { MacroTextarea } from "../card/MacroTextarea";
import { OtherLibraries } from "../card/OtherLibraries";
import { PageTabs } from "../card/PageTabs";
import { Sources } from "../card/Organize";
import { EditorBar } from "../card/EditorBar";
import { Metadata } from "../card/Metadata";
import { LibraryName, Revisions, Sync } from "../card/Panel";
import { VariantOf, Variants } from "../card/Variants";
import { UpdateBanner } from "../card/Upstream";
import { useEditor } from "../card/useEditor";
import { useForgeView } from "../forge/Forge";
import { useForge } from "../forge/useForge";
import { lorebookSubject } from "./subject";
import { Entries } from "./EntryEditor";
import "../card/Card.css";
import "./Lorebook.css";

/** Lorebook is the lorebook page. It's keyed by ID so another lorebook starts with no draft. */
export function Lorebook() {
  const { id } = useParams();
  return <LorebookEditor key={id} id={Number(id)} />;
}

function LorebookEditor({ id }: { id: number }) {
  const ed = useEditor("lorebook", id);
  const forge = useForge({ subject: lorebookSubject, id, data: ed.data, library: ed.entity.data?.library_id ?? 1 });
  useFollowLibrary(ed.entity.data?.library_id);
  const [tab, setTab] = useState("");
  const view = useForgeView({ forge, data: ed.data, edit: ed.edit, save: ed.saveWith, setTab });
  if (ed.entity.isPending) return null;
  if (ed.entity.isError) return <p className="error page">{ed.entity.error.message}</p>;
  const head = ed.entity.data;
  return (
    <div className="card-page">
      <aside className="card-aside">
        <VariantOf kind="lorebook" entity={head} />
        <LibraryName id={head.library_id} />
        <Sync kind="lorebook" id={id} />
        <LinkedCards ids={head.cards ?? []} library={head.library_id} />
        <Sources kind="lorebook" id={id} />
        <OtherLibraries kind="lorebook" id={id} />
        <Variants kind="lorebook" entity={head} />
        <Revisions kind="lorebook" id={id} />
      </aside>
      <div className="card-main">
        <EditorBar kind="lorebook" entity={head} ed={ed} lock={forge.lock} />
        <UpdateBanner kind="lorebook" id={id} />
        <Stale kind="lorebook" ed={ed} />
        <PageTabs
          label="Lorebook"
          selected={tab}
          onSelect={setTab}
          tabs={[
            {
              id: "fields",
              label: "Fields",
              panel: (
                <>
                  <Section
                    label="Description"
                    dirty={ed.dirty.has("description")}
                    count={tokens(text(ed.data.description))}
                    lock={view.lock?.("description")}
                    extra={view.extra?.("description")}
                  >
                    <MacroTextarea label="Description" value={text(ed.data.description)} onChange={(v) => ed.edit("description", v)} />
                  </Section>
                  <Entries data={ed.data} base={ed.draft?.base ?? ed.headData} edit={ed.edit} lock={view.lock?.("entries")} />
                </>
              ),
            },
            ...view.tabs,
            { id: "metadata", label: "Metadata", panel: <Metadata kind="lorebook" entity={head} /> },
          ]}
          actions={view.actions}
          notices={view.notices}
        />
        {view.overlays}
      </div>
    </div>
  );
}

function LinkedCards({ ids, library }: { ids: number[]; library: number }) {
  const cards = useQuery(namesQuery("card", library, ids));
  if (!ids.length) return null;
  const names = new Map((cards.data?.hits ?? []).map((h) => [h.id, text(h.fields.name)]));
  return (
    <section>
      <h2>Cards</h2>
      <ul className="plain">
        {ids.map((id) => (
          <li key={id}>
            <Link to={`/cards/${id}`}>{names.get(id) || `#${id}`}</Link>
          </li>
        ))}
      </ul>
    </section>
  );
}
