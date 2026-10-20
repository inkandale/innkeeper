import { useQuery } from "@tanstack/react-query";
import { useFollowLibrary } from "../shell/library";
import { Link, useParams } from "react-router";
import { api, must } from "../api/client";
import { Stale } from "../card/Conflict";
import { text, tokens } from "../card/draft";
import { Section } from "../card/Fields";
import { MacroTextarea } from "../card/MacroTextarea";
import { OtherLibraries } from "../card/OtherLibraries";
import { Sources } from "../card/Organize";
import { EditorBar } from "../card/EditorBar";
import { LibraryName, Revisions, Sync } from "../card/Panel";
import { VariantOf, Variants } from "../card/Variants";
import { UpdateBanner } from "../card/Upstream";
import { useEditor } from "../card/useEditor";
import { Forge } from "../forge/Forge";
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
        <Forge
          forge={forge}
          data={ed.data}
          edit={ed.edit}
          fields={(lock, extra) => (
            <>
              <Section
                label="Description"
                dirty={ed.dirty.has("description")}
                count={tokens(text(ed.data.description))}
                lock={lock?.("description")}
                extra={extra?.("description")}
              >
                <MacroTextarea label="Description" value={text(ed.data.description)} onChange={(v) => ed.edit("description", v)} />
              </Section>
              <Entries data={ed.data} base={ed.draft?.base ?? ed.headData} edit={ed.edit} lock={lock?.("entries")} />
            </>
          )}
        />
      </div>
    </div>
  );
}

function LinkedCards({ ids, library }: { ids: number[]; library: number }) {
  const body = { library, filter: { ids }, fields: ["name"], page: { size: 500 } };
  const cards = useQuery({
    queryKey: ["search", "card", body],
    enabled: ids.length > 0,
    queryFn: async () => must(await api.POST("/cards/search", { body })),
  });
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
