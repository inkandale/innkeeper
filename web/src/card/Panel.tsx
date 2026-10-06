import { useMutation, useQueries, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Link } from "react-router";
import { api, must, mustOk, thumb, upload } from "../api/client";
import { badgeClass } from "../cards/Tile";
import { useToast } from "../shell/Toasts";
import type { Entity } from "./Card";
import { Crop } from "./CropDialog";
import { text, type Data } from "./draft";
import { Collections, Sources, Tags } from "./Organize";

export type PanelProps = {
  card: Entity;
  data: Data;
  edit: (key: string, value: unknown) => void;
  dirty: boolean;
  saving: boolean;
  onSave: () => void;
  onAvatar: (sha: string) => void;
  avatarBusy: boolean;
};

/** Panel is the card page's left side. */
export function Panel({ card, data, edit, dirty, saving, onSave, onAvatar, avatarBusy }: PanelProps) {
  const avatar = card.revision.avatar;
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
        <a className="button" href={`/api/cards/${card.id}/export?format=png`} download>
          Export PNG
        </a>
        <Archive card={card} />
      </div>
      <Push id={card.id} dirty={dirty} />
      <Tags id={card.id} tags={card.tags ?? []} />
      <Collections id={card.id} member={card.collections ?? []} />
      <Sources id={card.id} />
      <Sync id={card.id} />
      <Lorebooks ids={card.lorebooks ?? []} />
      <Revisions id={card.id} />
    </aside>
  );
}

function Archive({ card }: { card: Entity }) {
  const qc = useQueryClient();
  const toast = useToast();
  const archived = !!card.archived_at;
  const set = useMutation({
    mutationFn: async () => mustOk(await api.PUT("/cards/{id}/archived", { params: { path: { id: card.id } }, body: { archived: !archived } })),
    onSuccess: () => void qc.invalidateQueries({ queryKey: ["card", card.id] }),
    onError: (e) => toast({ text: e.message, error: true }),
  });
  return (
    <button className={archived ? "button" : "button danger"} disabled={set.isPending} onClick={() => set.mutate()}>
      {archived ? "Unarchive" : "Archive"}
    </button>
  );
}

// Push sends the saved head, so it waits for unsaved edits to be saved.
function Push({ id, dirty }: { id: number; dirty: boolean }) {
  const toast = useToast();
  const [instance, setInstance] = useState<number>();
  const instances = useQuery({ queryKey: ["instances"], queryFn: async () => must(await api.GET("/instances")) });
  const plugins = useQuery({ queryKey: ["plugins"], queryFn: async () => must(await api.GET("/plugins")) });
  const pushers = new Set((plugins.data ?? []).filter((p) => p.capabilities?.includes("push")).map((p) => p.name));
  const targets = (instances.data ?? []).filter((i) => pushers.has(i.plugin));
  const instanceId = instance ?? targets[0]?.id ?? 0;
  const push = useMutation({
    mutationFn: async () => must(await api.POST("/cards/{id}/push", { params: { path: { id } }, body: { instance_id: instanceId } })),
    onSuccess: (j) => toast({ text: `Job ${j.id} started`, link: { to: `/settings/jobs/${j.id}`, label: "View job" } }),
    onError: (e) => toast({ text: e.message, error: true }),
  });
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
      <button className="button" disabled={dirty || push.isPending} title={dirty ? "Save first" : undefined} onClick={() => push.mutate()}>
        Push
      </button>
    </div>
  );
}

function Sync({ id }: { id: number }) {
  const bindings = useQuery({
    queryKey: ["card", id, "bindings"],
    queryFn: async () => must(await api.GET("/cards/{id}/bindings", { params: { path: { id } } })),
  });
  const instances = useQuery({ queryKey: ["instances"], queryFn: async () => must(await api.GET("/instances")) });
  const name = (n: number) => instances.data?.find((i) => i.id === n)?.name ?? `#${n}`;
  if (!bindings.data?.length) return null;
  return (
    <section>
      <h2>Sync</h2>
      <ul className="plain">
        {bindings.data.map((b) => (
          <li key={b.id}>
            <span className={"badge " + (badgeClass[b.status] ?? "unknown")} aria-hidden="true" /> {name(b.instance_id)}: {b.status.replaceAll("_", " ")}
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

function Revisions({ id }: { id: number }) {
  const revs = useQuery({
    queryKey: ["card", id, "revisions"],
    queryFn: async () => must(await api.GET("/cards/{id}/revisions", { params: { path: { id } } })),
  });
  return (
    <section>
      <h2>History</h2>
      <ul className="plain">
        {(revs.data ?? []).slice(0, 5).map((r) => (
          <li key={r.id}>
            <Link to={`/cards/${id}/history?rev=${r.id}`}>{r.message || `Revision ${r.id}`}</Link>{" "}
            <span className="muted">{new Date(r.created_at).toLocaleDateString()}</span>
          </li>
        ))}
      </ul>
      <Link to={`/cards/${id}/history`}>All history</Link>
    </section>
  );
}

function Image({ sha, busy, onAvatar }: { sha?: string; busy: boolean; onAvatar: (sha: string) => void }) {
  const toast = useToast();
  const [cropping, setCropping] = useState(false);
  const up = useMutation({
    mutationFn: upload,
    onSuccess: (s) => {
      setCropping(false);
      onAvatar(s);
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
            if (f) up.mutate(f);
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
          onDone={(png) => up.mutate(png)}
          onError={(text) => toast({ text, error: true })}
          onCancel={() => setCropping(false)}
        />
      )}
    </div>
  );
}
