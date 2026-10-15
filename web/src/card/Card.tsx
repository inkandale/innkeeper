import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useParams } from "react-router";
import { api, ApiError, must } from "../api/client";
import type { components } from "../api/schema";
import { useToast } from "../shell/Toasts";
import { Stale } from "./Conflict";
import { Panel } from "./Panel";
import { UpdateBanner } from "./Upstream";
import { useEditor } from "./useEditor";
import { Forge } from "../forge/Forge";
import { useForge } from "../forge/useForge";
import "./Card.css";

export type Entity = components["schemas"]["EntityOut"];

/** Card is the card page. It's keyed by ID so another card starts with no draft. */
export function Card() {
  const { id } = useParams();
  return <CardEditor key={id} id={Number(id)} />;
}

function CardEditor({ id }: { id: number }) {
  const qc = useQueryClient();
  const toast = useToast();
  const ed = useEditor("card", id);
  const forge = useForge({ cardId: id, data: ed.data });

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
        toast({ text: "This card changed elsewhere. Try again.", error: true });
      } else toast({ text: e.message, error: true });
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
        dirty={ed.dirty.size > 0}
        saving={ed.save.isPending || ed.stale || avatar.isPending}
        onSave={() => ed.draft && ed.save.mutate(ed.draft)}
        onAvatar={(sha, message) => avatar.mutate({ sha, message, base: head.revision })}
        avatarBusy={avatar.isPending || ed.save.isPending}
        lock={forge.lock}
      />
      <div className="card-main">
        <UpdateBanner kind="card" id={id} />
        <Stale kind="card" ed={ed} />
        <Forge forge={forge} data={ed.data} dirty={ed.dirty} edit={ed.edit} avatar={head.revision.avatar} />
      </div>
    </div>
  );
}
