import { useMutation, useQueries, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Link } from "react-router";
import { api, canWrite, must, mustOk, route, thumb, upload, type Kind } from "../api/client";
import { badgeClass } from "../cards/Tile";
import { useToast } from "../shell/Toasts";
import type { Entity } from "./Card";
import { Crop } from "./CropDialog";
import { text, type Data } from "./draft";
import { PushPreview } from "../sync/PushPreview";
import { reviewPath } from "../sync/sync";
import { Collections, Sources, Tags } from "./Organize";

export type PanelProps = {
  card: Entity;
  data: Data;
  edit: (key: string, value: unknown) => void;
  dirty: boolean;
  saving: boolean;
  onSave: () => void;
  /** onAvatar saves sha as the image, with message as the revision's. */
  onAvatar: (sha: string, message: string) => void;
  avatarBusy: boolean;
};

/** Panel is the card page's left side. */
export function Panel({ card, data, edit, dirty, saving, onSave, onAvatar, avatarBusy }: PanelProps) {
  const avatar = card.revision.avatar;
  const instances = useQuery({ queryKey: ["instances"], queryFn: async () => must(await api.GET("/instances")) });
  const plugins = useQuery({ queryKey: ["plugins"], queryFn: async () => must(await api.GET("/plugins")) });
  return (
    <aside className="card-aside">
      {avatar ? <img className="portrait" src={thumb(avatar, 640)} alt="" /> : <div className="portrait" />}
      <Image sha={avatar} busy={avatarBusy} onAvatar={onAvatar} />
      <input className="input name" aria-label="Name" value={text(data.name)} onChange={(e) => edit("name", e.target.value)} />
      <input className="input" aria-label="Creator" placeholder="Creator" value={text(data.creator)} onChange={(e) => edit("creator", e.target.value)} />
      <div className="row">
        <button className="button primary" disabled={!dirty || saving} onClick={onSave}>
          Save
        </button>
        <a className="button" href={`/api/cards/${card.id}/export?format=json`} download>
          Export JSON
        </a>
        {canWrite("png", plugins.data, instances.data) && (
          <a className="button" href={`/api/cards/${card.id}/export?format=png`} download>
            Export PNG
          </a>
        )}
        <Archive kind="card" entity={card} />
      </div>
      <Push kind="card" id={card.id} dirty={dirty} />
      <Tags id={card.id} tags={card.tags ?? []} />
      <Collections id={card.id} member={card.collections ?? []} />
      <Sources kind="card" id={card.id} />
      <Sync kind="card" id={card.id} />
      <Lorebooks ids={card.lorebooks ?? []} />
      <Revisions kind="card" id={card.id} />
    </aside>
  );
}

export function Archive({ kind, entity }: { kind: Kind; entity: Entity }) {
  const qc = useQueryClient();
  const toast = useToast();
  const archived = !!entity.archived_at;
  const set = useMutation({
    mutationFn: async () =>
      mustOk(await api.PUT(route(kind, "/cards/{id}/archived"), { params: { path: { id: entity.id } }, body: { archived: !archived } })),
    onSuccess: () => void qc.invalidateQueries({ queryKey: [kind, entity.id] }),
    onError: (e) => toast({ text: e.message, error: true }),
  });
  return (
    <button className={archived ? "button" : "button danger"} disabled={set.isPending} onClick={() => set.mutate()}>
      {archived ? "Unarchive" : "Archive"}
    </button>
  );
}

// Push sends the saved head, so it waits for unsaved edits to be saved.
export function Push({ kind, id, dirty }: { kind: Kind; id: number; dirty: boolean }) {
  const [instance, setInstance] = useState<number>();
  const [previewing, setPreviewing] = useState(false);
  const instances = useQuery({ queryKey: ["instances"], queryFn: async () => must(await api.GET("/instances")) });
  const plugins = useQuery({ queryKey: ["plugins"], queryFn: async () => must(await api.GET("/plugins")) });
  const pushers = new Set((plugins.data ?? []).filter((p) => p.capabilities?.includes("push")).map((p) => p.name));
  const targets = (instances.data ?? []).filter((i) => pushers.has(i.plugin));
  const instanceId = instance ?? targets[0]?.id ?? 0;
  if (!targets.length) return null;
  return (
    <div className="row">
      <select className="input" aria-label="Target" value={instanceId} onChange={(e) => setInstance(Number(e.target.value))}>
        {targets.map((i) => (
          <option key={i.id} value={i.id}>
            {i.name}
          </option>
        ))}
      </select>
      <button className="button" disabled={dirty} title={dirty ? "Save first" : undefined} onClick={() => setPreviewing(true)}>
        Push
      </button>
      {previewing && (
        <PushPreview
          kind={kind}
          id={id}
          instance={instanceId}
          name={targets.find((t) => t.id === instanceId)?.name ?? ""}
          onClose={() => setPreviewing(false)}
        />
      )}
    </div>
  );
}

export function Sync({ kind, id }: { kind: Kind; id: number }) {
  const bindings = useQuery({
    queryKey: [kind, id, "bindings"],
    queryFn: async () => must(await api.GET(route(kind, "/cards/{id}/bindings"), { params: { path: { id } } })),
  });
  const instances = useQuery({ queryKey: ["instances"], queryFn: async () => must(await api.GET("/instances")) });
  const name = (n: number) => instances.data?.find((i) => i.id === n)?.name ?? `#${n}`;
  const qc = useQueryClient();
  const toast = useToast();
  const setAutosync = useMutation({
    mutationFn: async ({ id, on }: { id: number; on: boolean }) =>
      must(await api.PATCH("/bindings/{id}", { params: { path: { id } }, body: { autosync: on } })),
    onSuccess: () => qc.invalidateQueries({ queryKey: [kind, id, "bindings"] }),
    onError: (e) => toast({ text: e.message, error: true }),
  });
  if (!bindings.data?.length) return null;
  return (
    <section>
      <h2>Sync</h2>
      <ul className="plain">
        {bindings.data.map((b) => (
          <li key={b.id}>
            <span className={"badge " + (badgeClass[b.status] ?? "unknown")} aria-hidden="true" /> {name(b.instance_id)}: {b.status.replaceAll("_", " ")}{" "}
            <label className="autosync">
              <input
                type="checkbox"
                checked={b.autosync}
                aria-label={`Autosync to ${name(b.instance_id)}`}
                disabled={setAutosync.isPending}
                onChange={(e) => setAutosync.mutate({ id: b.id, on: e.target.checked })}
              />{" "}
              autosync
            </label>
            {/* The queue lists drift only after a sync reads the app, and cards only, so drift opens Review. */}
            {kind === "card" && (b.status === "ahead" || b.status === "missing") ? (
              <>
                {" "}
                <Link to={`/sync/changes?instance_id=${b.instance_id}`}>Changes</Link>
              </>
            ) : (
              (b.status === "remote_changed" || b.status === "diverged") && (
                <>
                  {" "}
                  <Link to={reviewPath(b.instance_id, kind === "card" ? "items" : "lorebook-items", b.remote_id, { card: id })}>Review</Link>
                </>
              )
            )}
          </li>
        ))}
      </ul>
    </section>
  );
}

function Lorebooks({ ids }: { ids: number[] }) {
  const books = useQueries({
    queries: ids.map((id) => ({
      queryKey: ["lorebook", id],
      queryFn: async () => must(await api.GET("/lorebooks/{id}", { params: { path: { id } } })),
    })),
  });
  if (!ids.length) return null;
  return (
    <section>
      <h2>Lorebooks</h2>
      <ul className="plain">
        {ids.map((id, i) => (
          <li key={id}>
            <Link to={`/lorebooks/${id}`}>{text((books[i]?.data?.revision.data as Data | undefined)?.name) || `#${id}`}</Link>
          </li>
        ))}
      </ul>
    </section>
  );
}

export function Revisions({ kind, id }: { kind: Kind; id: number }) {
  const revs = useQuery({
    queryKey: [kind, id, "revisions"],
    queryFn: async () => must(await api.GET(route(kind, "/cards/{id}/revisions"), { params: { path: { id } } })),
  });
  return (
    <section>
      <h2>History</h2>
      <ul className="plain">
        {(revs.data ?? []).slice(0, 5).map((r) => (
          <li key={r.id}>
            <Link to={`/${kind}s/${id}/history?rev=${r.id}`}>{r.message || `Revision ${r.id}`}</Link>{" "}
            <span className="muted">{new Date(r.created_at).toLocaleDateString()}</span>
          </li>
        ))}
      </ul>
      <Link to={`/${kind}s/${id}/history`}>All history</Link>
    </section>
  );
}

function Image({ sha, busy, onAvatar }: { sha?: string; busy: boolean; onAvatar: PanelProps["onAvatar"] }) {
  const toast = useToast();
  const [cropping, setCropping] = useState(false);
  const up = useMutation({
    mutationFn: ({ file }: { file: Blob; message: string }) => upload(file),
    onSuccess: (s, { message }) => {
      setCropping(false);
      onAvatar(s, message);
    },
    onError: (e) => toast({ text: e.message, error: true }),
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
          onError={(text) => toast({ text, error: true })}
          onCancel={() => setCropping(false)}
        />
      )}
    </div>
  );
}
