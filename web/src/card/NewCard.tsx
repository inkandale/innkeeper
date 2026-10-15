import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { useNavigate } from "react-router";
import { api, must, upload } from "../api/client";
import { useToast } from "../shell/Toasts";
import { Crop } from "./CropDialog";
import { changed, type Data } from "./draft";
import { Fields } from "./Fields";
import { Identity } from "./Panel";
import "./Card.css";

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
  const [data, setData] = useState(readDraft);
  // The avatar stays in memory: a cropped PNG can outgrow localStorage.
  const [avatar, setAvatar] = useState<Blob | null>(null);
  useEffect(() => writeDraft(data), [data]);
  const edit = (key: string, value: unknown) => setData((d) => ({ ...d, [key]: value }));
  const dirty = changed({ baseId: 0, base: {}, data });
  const save = useMutation({
    mutationFn: async () => {
      const sha = avatar ? await upload(avatar) : undefined;
      return must(await api.POST("/cards", { body: { data, avatar: sha, source: "ui" } }));
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
      <aside className="card-aside">
        <Avatar image={avatar} busy={save.isPending} onChange={setAvatar} />
        <Identity data={data} edit={edit} />
        <div className="row">
          <button className="button primary" disabled={(!dirty.size && !avatar) || save.isPending} onClick={() => save.mutate()}>
            Save
          </button>
        </div>
      </aside>
      <div className="card-main">
        <Fields data={data} dirty={dirty} edit={edit} />
      </div>
    </div>
  );
}

/** Avatar picks the new card's image and crops it, holding it until Save uploads it. */
function Avatar({ image, busy, onChange }: { image: Blob | null; busy: boolean; onChange: (b: Blob) => void }) {
  const toast = useToast();
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
            disabled={busy}
            onChange={(e) => {
              const f = e.target.files?.[0];
              e.target.value = "";
              if (f) onChange(f);
            }}
          />
        </label>
        {src && (
          <button className="button" disabled={busy} onClick={() => setCropping(true)}>
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
          onError={(text) => toast({ text, error: true })}
          onCancel={() => setCropping(false)}
        />
      )}
    </>
  );
}
