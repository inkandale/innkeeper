import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { useNavigate } from "react-router";
import { api, must, upload } from "../api/client";
import { useLibrary } from "../shell/library";
import { Crop } from "./CropDialog";
import { changed, type Data } from "./draft";
import { Identity, Name } from "./Panel";
import { useForgeView } from "../forge/Forge";
import { useForge } from "../forge/useForge";
import { cardSubject } from "./subject";
import { Fields } from "./Fields";
import { PageTabs } from "./PageTabs";
import "./Card.css";
import { toastError } from "../shell/Toasts";

const draftKey = "innkeeper.newCard";

// Storage can throw or be empty in private windows; the page then forgets the draft on reload.
function readDraft(): Data {
  try {
    const v: unknown = JSON.parse(localStorage.getItem(draftKey) ?? "{}");
    return v && typeof v === "object" && !Array.isArray(v) ? (v as Data) : {};
  } catch {
    return {};
  }
}

function writeDraft(d: Data | null) {
  try {
    if (d) localStorage.setItem(draftKey, JSON.stringify(d));
    else localStorage.removeItem(draftKey);
  } catch {
    // The draft still lives in this tab.
  }
}

/** NewCard is the card page for a card not yet saved. Its data is kept in localStorage until the first Save creates the card. */
export function NewCard() {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const library = useLibrary();
  const [data, setData] = useState(readDraft);
  // The avatar stays in memory: a cropped PNG can outgrow localStorage.
  const [avatar, setAvatar] = useState<Blob | null>(null);
  useEffect(() => writeDraft(data), [data]);
  const edit = (key: string, value: unknown) => setData((d) => ({ ...d, [key]: value }));
  const dirty = changed({ baseId: 0, base: {}, data });
  const forge = useForge({ subject: cardSubject, data, library });
  const forgeBusy = forge.runs.some((r) => r.live) || forge.waiting.length > 0 || !!forge.pending;
  const [tab, setTab] = useState<string>();
  const view = useForgeView({ forge, isNew: true, data, edit, avatar: avatar ?? undefined, setTab });
  const save = useMutation({
    mutationFn: async () => {
      const sha = avatar ? await upload(avatar) : undefined;
      return must(await api.POST("/cards", { body: { data, avatar: sha, source: "ui", library } }));
    },
    onSuccess: (card) => {
      writeDraft(null);
      qc.setQueryData(["card", card.id], card);
      void qc.invalidateQueries({ queryKey: ["search"] });
      // Replace, so Back doesn't land on an empty new card.
      void navigate(`/cards/${card.id}`, { replace: true });
    },
  });
  return (
    <div className="card-page">
      {/* Read-only while saving: Save sends the data as it was, then clears the draft. */}
      <fieldset className="contents" disabled={save.isPending}>
        <aside className="card-aside">
          <Avatar image={avatar} onChange={setAvatar} />
          <Name data={data} edit={edit} lock={forge.lock} />
          <Identity data={data} edit={edit} lock={forge.lock} />
          <div className="row">
            <button className="button primary" disabled={(!dirty.size && !avatar) || forgeBusy} onClick={() => save.mutate()}>
              Save
            </button>
          </div>
          {forgeBusy && <p className="muted">Save waits for the Forge run to finish and be reviewed: the run belongs to this page, not the saved card.</p>}
        </aside>
        <div className="card-main">
          <PageTabs
            label="New card"
            selected={tab ?? ""}
            onSelect={setTab}
            tabs={[
              ...(view.start ? [view.start] : []),
              { id: "fields", label: "Fields", panel: <Fields data={data} dirty={dirty} edit={edit} lock={view.lock} extra={view.extra} /> },
              ...view.tabs,
            ]}
            actions={view.actions}
            notices={view.notices}
          />
          {view.overlays}
        </div>
      </fieldset>
    </div>
  );
}

/** Avatar picks the new card's image and crops it, holding it until Save uploads it. */
function Avatar({ image, onChange }: { image: Blob | null; onChange: (b: Blob) => void }) {
  const [cropping, setCropping] = useState(false);
  const [src, setSrc] = useState<string>();
  useEffect(() => {
    if (!image) return;
    const url = URL.createObjectURL(image);
    setSrc(url);
    return () => URL.revokeObjectURL(url);
  }, [image]);
  return (
    <>
      {src ? <img className="portrait" src={src} alt="" /> : <div className="portrait" />}
      <div className="row">
        <label className="button file-button">
          Choose image
          <input
            type="file"
            accept="image/png,image/jpeg,image/webp"
            className="visually-hidden"
            onChange={(e) => {
              const f = e.target.files?.[0];
              e.target.value = "";
              if (f) onChange(f);
            }}
          />
        </label>
        {src && (
          <button className="button" onClick={() => setCropping(true)}>
            Crop
          </button>
        )}
      </div>
      {cropping && src && (
        <Crop
          src={src}
          busy={false}
          onDone={(png) => {
            onChange(png);
            setCropping(false);
          }}
          onError={toastError}
          onCancel={() => setCropping(false)}
        />
      )}
    </>
  );
}
