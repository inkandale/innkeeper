import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Link } from "react-router";
import { api, must, mustOk, route, thumb, upload, type Kind } from "../api/client";
import { bindingsQuery, instancesQuery, pluginsQuery, useInstancesWith } from "../api/queries";
import { badgeClass } from "../cards/Tile";
import { count } from "../shell/count";
import { useConfirm } from "../shell/ConfirmDialog";
import type { Entity } from "./Card";
import { CardLorebooks } from "./CardLorebooks";
import { Crop } from "./CropDialog";
import { OtherLibraries } from "./OtherLibraries";
import { nsfw, strings, text, type Data } from "./draft";
import { Logo, apps } from "../sync/Logo";
import { PushPreview } from "../sync/PushPreview";
import { TargetPicker } from "../sync/TargetPicker";
import { reviewPath, stateLabel } from "../sync/sync";
import { ChipInput } from "./ChipInput";
import { Group } from "./Group";
import { Collections, Sources } from "./Organize";
import { useBindingEdits } from "./Upstream";
import { Variants } from "./Variants";
import { toastError } from "../shell/Toasts";
import { toast } from "sonner";

type PanelProps = {
  card: Entity;
  data: Data;
  edit: (key: string, value: unknown) => void;
  /** onAvatar saves sha as the image, with message as the revision's. */
  onAvatar: (sha: string, message: string) => void;
  avatarBusy: boolean;
  lock?: (key: string) => string | undefined;
};

/** Panel is the card page's left side. */
export function Panel({ card, data, edit, onAvatar, avatarBusy, lock }: PanelProps) {
  const avatar = card.revision.avatar;
  return (
    <aside className="card-aside">
      {avatar ? <img className={nsfw(data.tags) ? "portrait nsfw" : "portrait"} src={thumb(avatar, 640)} alt="" /> : <div className="portrait" />}
      <Image sha={avatar} busy={avatarBusy} onAvatar={onAvatar} />
      <Identity data={data} edit={edit} lock={lock} />
      <Group title="Links">
        <Sources kind="card" id={card.id} />
        <Variants kind="card" entity={card} />
        <OtherLibraries kind="card" id={card.id} />
        <CardLorebooks card={card} />
        <Revisions kind="card" id={card.id} />
      </Group>
      <Sync kind="card" id={card.id} />
      <Collections id={card.id} library={card.library_id} member={card.collections ?? []} />
    </aside>
  );
}

type IdentityProps = { data: Data; edit: (key: string, value: unknown) => void; lock?: (key: string) => string | undefined };

/** Name edits a card's or lorebook's name. */
export function Name({ data, edit, lock }: IdentityProps) {
  return (
    <input
      className="input name"
      aria-label="Name"
      placeholder="Name"
      value={text(data.name)}
      disabled={!!lock?.("name")}
      onChange={(e) => edit("name", e.target.value)}
    />
  );
}

/** Identity edits the card's nickname, creator, version, and tags. */
export function Identity({ data, edit, lock }: IdentityProps) {
  const locked = (key: string) => !!lock?.(key);
  const field = (key: string, label: string) => (
    <label className="aside-field">
      <span>{label}</span>
      <input className="input" value={text(data[key])} disabled={locked(key)} onChange={(e) => edit(key, e.target.value)} />
    </label>
  );
  return (
    <>
      {field("nickname", "Nickname")}
      {field("creator", "Creator")}
      {field("character_version", "Version")}
      <fieldset className="contents" disabled={locked("tags")}>
        <ChipInput label="Tags" values={strings(data.tags)} onChange={(v) => edit("tags", v)} />
      </fieldset>
    </>
  );
}

export function Archive({ kind, entity }: { kind: Kind; entity: Entity }) {
  const qc = useQueryClient();
  const [ask, dialog] = useConfirm();
  const archived = !!entity.archived_at;
  const name = text((entity.revision.data as Data).name);
  const [unpublish, setUnpublish] = useState(false);
  const bindings = useQuery(bindingsQuery(kind, entity.id));
  const instances = useQuery(instancesQuery);
  const linked = (bindings.data ?? [])
    .filter((b) => b.push)
    .map((b) => instances.data?.find((i) => i.id === b.instance_id)?.label ?? `#${b.instance_id}`);
  const set = useMutation({
    meta: { toast: false },
    mutationFn: async (v: { archived: boolean; unpublish: boolean }) =>
      mustOk(await api.PUT(route(kind, "/cards/{id}/archived"), { params: { path: { id: entity.id } }, body: v })),
    onSuccess: (_, v) => {
      void qc.invalidateQueries({ queryKey: [kind, entity.id] });
      // Unarchiving brings back the card, but not the apps' deleted copies.
      if (v.archived && !v.unpublish)
        toast.success(`Archived ${name}`, { action: { label: "Undo", onClick: () => set.mutate({ archived: false, unpublish: false }) } });
    },
  });
  const archive = () => {
    if (!unpublish) return set.mutate({ archived: true, unpublish: false });
    ask({
      title: `Archive ${name} and delete it from ${linked.join(", ")}?`,
      body: (
        <>
          <p>This deletes, for good:</p>
          <ul>
            {linked.map((l) => (
              <li key={l}>The copy in {l}, chats included</li>
            ))}
          </ul>
          <p className="muted">You can unarchive it later, but the apps' copies don't come back.</p>
        </>
      ),
      confirmLabel: "Archive and delete",
      danger: true,
      onConfirm: () => set.mutate({ archived: true, unpublish: true }),
    });
  };
  return (
    <>
      <button
        className={archived ? "button" : "button danger"}
        disabled={set.isPending}
        onClick={() => (archived ? set.mutate({ archived: false, unpublish: false }) : archive())}
      >
        {archived ? "Unarchive" : "Archive"}
      </button>
      {entity.archived_reason && (
        <span className="muted">
          Hidden {entity.archived_reason === "duplicate" ? "as a duplicate" : "for a low score"}. If you unarchive it, the next
          hide run may hide it again.
        </span>
      )}
      {!archived && linked.length > 0 && (
        <label className="check">
          <span>Also delete from {linked.join(", ")}, chats included</span>
          <input type="checkbox" checked={unpublish} onChange={(e) => setUnpublish(e.target.checked)} />
        </label>
      )}
      {set.error && <span className="error">{set.error.message}</span>}
      {dialog}
    </>
  );
}

// Push sends the saved head, so it waits for unsaved edits to be saved.
export function Push({ kind, id, dirty }: { kind: Kind; id: number; dirty: boolean }) {
  const [picked, setPicked] = useState<number[]>();
  const [previewing, setPreviewing] = useState(false);
  const targets = useInstancesWith("push");
  const bindings = useQuery(bindingsQuery(kind, id));
  if (!targets.length) return null;
  // Pushing to an app without a copy makes a new character there, so that's picked on purpose, not by default.
  const holding = (bindings.data ?? []).filter((b) => b.push).map((b) => b.instance_id);
  const chosen = targets.filter((t) => (picked ?? holding).includes(t.id));
  return (
    <div className="row">
      <TargetPicker targets={targets} picked={chosen.map((t) => t.id)} onChange={setPicked} />
      <button
        className="button"
        disabled={dirty || !chosen.length}
        title={dirty ? "Save first" : undefined}
        onClick={() => setPreviewing(true)}
      >
        Push
      </button>
      {previewing && <PushPreview kind={kind} id={id} targets={chosen} onClose={() => setPreviewing(false)} />}
    </div>
  );
}

export function Sync({ kind, id }: { kind: Kind; id: number }) {
  const bindings = useQuery(bindingsQuery(kind, id));
  const instances = useQuery(instancesQuery);
  const plugins = useQuery(pluginsQuery);
  const logos = apps(instances.data, plugins.data);
  const name = (n: number) => instances.data?.find((i) => i.id === n)?.label ?? `#${n}`;
  const { edit } = useBindingEdits(kind, id);
  // Followed site sources show under Sources.
  const pushed = (bindings.data ?? []).filter((b) => b.push);
  if (!pushed.length) return null;
  return (
    <Group title="Sync">
      <ul className="plain">
        {pushed.map((b) => (
          <li key={b.id}>
            <span aria-hidden="true">
              <Logo app={logos.get(b.instance_id)} label={name(b.instance_id)} />
            </span>{" "}
            <span className={"badge " + (badgeClass[b.status] ?? "unknown")} aria-hidden="true" /> {name(b.instance_id)}: {stateLabel(b.status)}{" "}
            <label className="autosync" title="Each sync pushes this card when the library's copy is newer and the app's copy hasn't changed.">
              <input
                type="checkbox"
                checked={b.autosync}
                aria-label={`Autosync to ${name(b.instance_id)}`}
                disabled={edit.isPending}
                onChange={(e) => edit.mutate({ binding: b.id, body: { autosync: e.target.checked } })}
              />{" "}
              autosync
            </label>
            {/* The queue lists drift only after a sync reads the app, so drift opens Review. */}
            {b.status === "ahead" || b.status === "missing" ? (
              <>
                {" "}
                <Link to={`/sync/changes?instance_id=${b.instance_id}`}>Changes</Link>
              </>
            ) : (
              (b.status === "remote_changed" || b.status === "diverged") && (
                <>
                  {" "}
                  <Link to={reviewPath(b.instance_id, kind, b.remote_id, { card: id })}>Review</Link>
                </>
              )
            )}
          </li>
        ))}
      </ul>
    </Group>
  );
}

/** Revisions links to the history page with the revision count. */
export function Revisions({ kind, id }: { kind: Kind; id: number }) {
  const revs = useQuery({
    queryKey: [kind, id, "revisions"],
    queryFn: async () => must(await api.GET(route(kind, "/cards/{id}/revisions"), { params: { path: { id } } })),
  });
  const n = revs.data?.length;
  return <Link to={`/${kind}s/${id}/history`}>History{n ? ` (${count(n, "revision")})` : ""}</Link>;
}

function Image({ sha, busy, onAvatar }: { sha?: string; busy: boolean; onAvatar: PanelProps["onAvatar"] }) {
  const [cropping, setCropping] = useState(false);
  const up = useMutation({
    mutationFn: ({ file }: { file: Blob; message: string }) => upload(file),
    onSuccess: (s, { message }) => {
      setCropping(false);
      onAvatar(s, message);
    },
  });
  const off = busy || up.isPending;
  return (
    <div className="row">
      <label className="button file-button">
        Replace image
        <input
          type="file"
          accept="image/png,image/jpeg,image/webp"
          className="visually-hidden"
          disabled={off}
          onChange={(e) => {
            const f = e.target.files?.[0];
            e.target.value = "";
            if (f) up.mutate({ file: f, message: "Replace image" });
          }}
        />
      </label>
      {sha && (
        <button className="button" disabled={off} onClick={() => setCropping(true)}>
          Crop
        </button>
      )}
      {cropping && sha && (
        <Crop
          src={`/api/blobs/${sha}`}
          busy={up.isPending}
          onDone={(png) => up.mutate({ file: png, message: "Crop image" })}
          onError={toastError}
          onCancel={() => setCropping(false)}
        />
      )}
    </div>
  );
}
