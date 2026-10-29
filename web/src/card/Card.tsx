import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useFollowLibrary } from "../shell/library";
import { useParams } from "react-router";
import { api, ApiError, exportFormats, must } from "../api/client";
import { instancesQuery, pluginsQuery } from "../api/queries";
import type { components } from "../api/schema";
import { Stale } from "./Conflict";
import { EditorBar } from "./EditorBar";
import { Panel } from "./Panel";
import { UpdateBanner } from "./Upstream";
import { useEditor } from "./useEditor";
import { Forge } from "../forge/Forge";
import { useForge } from "../forge/useForge";
import { cardSubject } from "./subject";
import { Similar } from "./Discover";
import { Fields } from "./Fields";
import "./Card.css";
import { toastError } from "../shell/Toasts";

export type Entity = components["schemas"]["EntityOut"];

/** Card is the card page. It's keyed by ID so another card starts with no draft. */
export function Card() {
  const { id } = useParams();
  return <CardEditor key={id} id={Number(id)} />;
}

function CardEditor({ id }: { id: number }) {
  const qc = useQueryClient();
  const ed = useEditor("card", id);
  const forge = useForge({ subject: cardSubject, id, data: ed.data, library: ed.entity.data?.library_id ?? 1 });
  useFollowLibrary(ed.entity.data?.library_id);
  const instances = useQuery(instancesQuery);
  const plugins = useQuery(pluginsQuery);

  // An image change saves at once, on the head. A draft started from that
  // head moves onto the new revision, since its data didn't change.
  const avatar = useMutation({
    mutationFn: async ({ sha, message, base }: { sha: string; message: string; base: Entity["revision"] }) =>
      must(
        await api.PUT("/cards/{id}", {
          params: { path: { id } },
          body: { base_revision_id: base.id, data: base.data, avatar: sha, message, source: "ui" },
        }),
      ),
    onSuccess: (e, { base }) => {
      qc.setQueryData(["card", id], e);
      void qc.invalidateQueries({ queryKey: ["card", id, "revisions"] });
      ed.setDraft((d) => (d && d.baseId === base.id ? { ...d, baseId: e.revision.id } : d));
    },
    meta: { toast: false },
    onError: (e) => {
      if (e instanceof ApiError && e.status === 409) {
        // The head moved on, so the button's base is stale; refetch it.
        void qc.invalidateQueries({ queryKey: ["card", id] });
        toastError("This card changed elsewhere. Try again.");
      } else toastError(e.message);
    },
  });

  if (ed.entity.isPending) return null;
  if (ed.entity.isError) return <p className="error page">{ed.entity.error.message}</p>;
  const head = ed.entity.data;

  return (
    <div className="card-page">
      <Panel
        card={head}
        data={ed.data}
        edit={ed.edit}
        onAvatar={(sha, message) => avatar.mutate({ sha, message, base: head.revision })}
        avatarBusy={avatar.isPending || ed.save.isPending}
        lock={forge.lock}
      />
      <div className="card-main">
        <EditorBar
          kind="card"
          entity={head}
          ed={ed}
          lock={forge.lock}
          busy={avatar.isPending}
          menu={exportFormats(plugins.data, instances.data).map((f) => (
            <a key={f} href={`/api/cards/${id}/export?format=${encodeURIComponent(f)}`} download>
              Export {f.toUpperCase()}
            </a>
          ))}
        />
        <UpdateBanner kind="card" id={id} />
        <Stale kind="card" ed={ed} />
        <Forge
          forge={forge}
          data={ed.data}
          edit={ed.edit}
          save={ed.saveWith}
          fields={(lock, extra) => <Fields data={ed.data} dirty={ed.dirty} edit={ed.edit} lock={lock} extra={extra} />}
          avatar={head.revision.avatar}
        />
        <Similar id={head.id} library={head.library_id} />
      </div>
    </div>
  );
}
