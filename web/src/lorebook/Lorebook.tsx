import { useQuery } from "@tanstack/react-query";
import { Link, useParams } from "react-router";
import { api, must } from "../api/client";
import { Stale } from "../card/Conflict";
import { text, tokens } from "../card/draft";
import { Section } from "../card/Fields";
import { OtherLibraries } from "../card/OtherLibraries";
import { Sources } from "../card/Organize";
import { Archive, Push, Revisions, Sync } from "../card/Panel";
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
  const forge = useForge({ subject: lorebookSubject, id, data: ed.data });
  if (ed.entity.isPending) return null;
  if (ed.entity.isError) return <p className="error page">{ed.entity.error.message}</p>;
  const head = ed.entity.data;
  const dirty = ed.dirty.size > 0;
  return (
    <div className="card-page">
      <aside className="card-aside">
        <input
          className="input name"
          aria-label="Name"
          value={text(ed.data.name)}
          disabled={!!forge.lock("name")}
          onChange={(e) => ed.edit("name", e.target.value)}
        />
        <VariantOf kind="lorebook" entity={head} />
        <div className="row">
          <button className="button primary" disabled={!dirty || ed.save.isPending || ed.stale} onClick={() => ed.draft && ed.save.mutate(ed.draft)}>
            Save
          </button>
          <Archive kind="lorebook" entity={head} />
        </div>
        <Push kind="lorebook" id={id} dirty={dirty} />
        <Sync kind="lorebook" id={id} />
        <LinkedCards ids={head.cards ?? []} library={head.library_id} />
        <Sources kind="lorebook" id={id} />
        <OtherLibraries kind="lorebook" id={id} />
        <Variants kind="lorebook" entity={head} />
        <Revisions kind="lorebook" id={id} />
      </aside>
      <div className="card-main">
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
                <textarea
                  className="input grow"
                  aria-label="Description"
                  placeholder="Description"
                  value={text(ed.data.description)}
                  onChange={(e) => ed.edit("description", e.target.value)}
                />
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
