import { useMutation, useQueries, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Link } from "react-router";
import { api, exportFormats, must, mustOk, route, thumb, upload, type Kind } from "../api/client";
import { bindingsQuery, instancesQuery, librariesQuery, pluginsQuery, useInstancesWith } from "../api/queries";
import { badgeClass } from "../cards/Tile";
import { useToast } from "../shell/Toasts";
import type { Entity } from "./Card";
import { Crop } from "./CropDialog";
import { Enrichments } from "./Enrichments";
import { OtherLibraries } from "./OtherLibraries";
import { strings, text, type Data } from "./draft";
import { Logo, apps } from "../sync/Logo";
import { PushPreview } from "../sync/PushPreview";
import { TargetPicker } from "../sync/TargetPicker";
import { reviewPath, stateLabel } from "../sync/sync";
import { ChipInput } from "./ChipInput";
import { Collections, Sources } from "./Organize";
import { useBindingEdits } from "./Upstream";
import { VariantOf, Variants } from "./Variants";

type PanelProps = {
  card: Entity;
  data: Data;
  edit: (key: string, value: unknown) => void;
  dirty: boolean;
  saving: boolean;
  onSave: () => void;
  /** onAvatar saves sha as the image, with message as the revision's. */
  onAvatar: (sha: string, message: string) => void;
  avatarBusy: boolean;
  lock?: (key: string) => string | undefined;
};

/** Panel is the card page's left side. */
export function Panel({ card, data, edit, dirty, saving, onSave, onAvatar, avatarBusy, lock }: PanelProps) {
  const avatar = card.revision.avatar;
  const instances = useQuery(instancesQuery);
  const plugins = useQuery(pluginsQuery);
  return (
    <aside className="card-aside">
      {avatar ? <img className="portrait" src={thumb(avatar, 640)} alt="" /> : <div className="portrait" />}
      <Image sha={avatar} busy={avatarBusy} onAvatar={onAvatar} />
      <Identity data={data} edit={edit} lock={lock} />
      <VariantOf kind="card" entity={card} />
      <LibraryName id={card.library_id} />
      <div className="row">
        <button className="button primary" disabled={!dirty || saving} onClick={onSave}>
          Save
        </button>
        {exportFormats(plugins.data, instances.data).map((f) => (
          <a key={f} className="button" href={`/api/cards/${card.id}/export?format=${encodeURIComponent(f)}`} download>
            Export {f.toUpperCase()}
          </a>
        ))}
        <Archive kind="card" entity={card} />
      </div>
      <Push kind="card" id={card.id} dirty={dirty} />
      <Collections id={card.id} library={card.library_id} member={card.collections ?? []} />
      <Sources kind="card" id={card.id} />
      <OtherLibraries kind="card" id={card.id} />
      <Sync kind="card" id={card.id} />
      <Lorebooks ids={card.lorebooks ?? []} />
      <Enrichments id={card.id} />
      <Variants kind="card" entity={card} />
      <Revisions kind="card" id={card.id} />
    </aside>
  );
}

/** LibraryName names the library a card or lorebook is in. */
export function LibraryName({ id }: { id: number }) {
  const library = useQuery(librariesQuery).data?.find((l) => l.id === id);
  return library ? <p className="muted">Library: {library.name}</p> : null;
}

/** Identity edits the card's name, nickname, creator, version, and tags. */
export function Identity({ data, edit, lock }: { data: Data; edit: (key: string, value: unknown) => void; lock?: (key: string) => string | undefined }) {
  const locked = (key: string) => !!lock?.(key);
  const field = (key: string, label: string, className = "input") => (
    <input
      className={className}
      aria-label={label}
      placeholder={label}
      value={text(data[key])}
      disabled={locked(key)}
      onChange={(e) => edit(key, e.target.value)}
    />
  );
  return (
    <>
      {field("name", "Name", "input name")}
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
  const archived = !!entity.archived_at;
  const [unpublish, setUnpublish] = useState(false);
  const bindings = useQuery(bindingsQuery(kind, entity.id));
  const instances = useQuery(instancesQuery);
  const linked = (bindings.data ?? [])
    .filter((b) => b.push)
    .map((b) => instances.data?.find((i) => i.id === b.instance_id)?.label ?? `#${b.instance_id}`);
  const set = useMutation({
    mutationFn: async () =>
      mustOk(
        await api.PUT(route(kind, "/cards/{id}/archived"), {
          params: { path: { id: entity.id } },
          body: { archived: !archived, unpublish: !archived && unpublish },
        }),
      ),
    onSuccess: () => void qc.invalidateQueries({ queryKey: [kind, entity.id] }),
  });
  return (
    <>
      <button className={archived ? "button" : "button danger"} disabled={set.isPending} onClick={() => set.mutate()}>
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
    </>
  );
}

// Push sends the saved head, so it waits for unsaved edits to be saved.
export function Push({ kind, id, dirty }: { kind: Kind; id: number; dirty: boolean }) {
  const [picked, setPicked] = useState<number[]>();
  const [previewing, setPreviewing] = useState(false);
  const targets = useInstancesWith("push");
  if (!targets.length) return null;
  const chosen = targets.filter((t) => (picked ?? [targets[0]!.id]).includes(t.id));
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
    <section>
      <h2>Sync</h2>
      <ul className="plain">
        {pushed.map((b) => (
          <li key={b.id}>
            <span aria-hidden="true">
              <Logo app={logos.get(b.instance_id)} label={name(b.instance_id)} />
            </span>{" "}
            <span className={"badge " + (badgeClass[b.status] ?? "unknown")} aria-hidden="true" /> {name(b.instance_id)}: {stateLabel(b.status)}{" "}
            <label className="autosync">
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
