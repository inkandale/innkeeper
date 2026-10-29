import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { api, must, mustOk } from "../api/client";
import type { components } from "../api/schema";
import { useConfirm } from "../shell/ConfirmDialog";
import { SchemaForm, type Schema } from "./SchemaForm";
import { toast } from "sonner";

export type RecordType = components["schemas"]["RecordTypeOut"];

type Props = { instance: number; type: string; recordType: RecordType };

/** Records lists one record type a plugin instance keeps, with an editor, delete, and import. */
export function Records({ instance, type, recordType }: Props) {
  const [ask, dialog] = useConfirm();
  const qc = useQueryClient();
  const path = { id: instance, type };
  const listKey = ["records", instance, type];
  // undefined: no editor; "": a new record; otherwise the ID being edited.
  const [editing, setEditing] = useState<string>();
  const list = useQuery({
    queryKey: listKey,
    queryFn: async () => must(await api.GET("/instances/{id}/records/{type}", { params: { path } })),
  });
  const imported = (got: { name: string }[] | null) => {
    qc.invalidateQueries({ queryKey: listKey });
    toast.success(`Imported ${got?.map((r) => r.name).join(", ") || "nothing"}.`);
  };
  const remove = useMutation({
    mutationFn: async (record: string) =>
      mustOk(await api.DELETE("/instances/{id}/records/{type}/{record}", { params: { path: { ...path, record } } })),
    onSuccess: () => qc.invalidateQueries({ queryKey: listKey }),
  });
  const importFile = useMutation({
    mutationFn: async (file: File) =>
      must(
        await api.POST("/instances/{id}/records/{type}/import", {
          params: { path, query: { name: file.name } },
          // The schema types a binary body as string; send the bytes as they are.
          body: file as unknown as string,
          bodySerializer: (b) => b,
          headers: { "Content-Type": "application/octet-stream" },
        }),
      ),
    onSuccess: imported,
  });
  const importURL = useMutation({
    mutationFn: async (url: string) => must(await api.POST("/instances/{id}/records/{type}/import-url", { params: { path }, body: { url } })),
    onSuccess: imported,
  });
  const imports = recordType.import ?? [];

  return (
    <section className="records">
      <header>
        <h3>{recordType.label}</h3>
        <button type="button" className="button" onClick={() => setEditing("")}>
          New
        </button>
      </header>
      {editing !== undefined && <RecordEditor key={editing} {...{ instance, type, recordType }} id={editing} onDone={() => setEditing(undefined)} />}
      {list.error && <p className="error">{list.error.message}</p>}
      {list.data?.length === 0 && <p className="muted">None yet.</p>}
      {!!list.data?.length && (
        <ul>
          {list.data.map((r) => (
            <li key={r.id}>
              <span>{r.name}</span>
              {r.description && <small className="muted">{r.description}</small>}
              <button type="button" className="button" aria-label={`Edit ${r.name}`} onClick={() => setEditing(r.id)}>
                Edit
              </button>
              <button
                type="button"
                className="button"
                aria-label={`Delete ${r.name}`}
                onClick={() =>
                  ask({
                    title: `Delete ${r.name}?`,
                    confirmLabel: "Delete",
                    danger: true,
                    onConfirm: () => remove.mutate(r.id),
                  })
                }
              >
                Delete
              </button>
            </li>
          ))}
        </ul>
      )}
      {imports.length > 0 && (
        <div className="actions">
          {imports.includes("file") && (
            <label className="button">
              Import a file
              <input
                type="file"
                hidden
                disabled={importFile.isPending}
                onChange={(e) => {
                  const f = e.target.files?.[0];
                  e.target.value = "";
                  if (f) importFile.mutate(f);
                }}
              />
            </label>
          )}
          {imports.includes("url") && (
            <form
              className="actions"
              onSubmit={(e) => {
                e.preventDefault();
                const box = e.currentTarget.elements.namedItem("url") as HTMLInputElement;
                importURL.mutate(box.value, { onSuccess: () => (box.value = "") });
              }}
            >
              <input className="input" name="url" type="url" required placeholder="https://…" aria-label="Import from a URL" />
              <button className="button" disabled={importURL.isPending}>
                Import
              </button>
            </form>
          )}
        </div>
      )}
      {dialog}
    </section>
  );
}

/** RecordEditor loads the record id, or starts an empty one when id is "". */
function RecordEditor({ instance, type, recordType, id, onDone }: Props & { id: string; onDone: () => void }) {
  const rec = useQuery({
    queryKey: ["record", instance, type, id],
    enabled: id !== "",
    // Always the plugin's current copy, not one cached before an edit.
    gcTime: 0,
    queryFn: async () => must(await api.GET("/instances/{id}/records/{type}/{record}", { params: { path: { id: instance, type, record: id } } })),
  });
  if (rec.error) return <p className="error">{rec.error.message}</p>;
  if (id !== "" && !rec.data) return null;
  return <RecordForm {...{ instance, type, recordType, id, onDone }} initial={rec.data?.record ?? {}} />;
}

function RecordForm({ instance, type, recordType, id, onDone, initial }: Props & { id: string; onDone: () => void; initial: unknown }) {
  const qc = useQueryClient();
  const [value, setValue] = useState(initial);
  const save = useMutation({
    mutationFn: async () => {
      const body = { record: (value ?? {}) as Record<string, unknown> };
      const path = { id: instance, type };
      if (id === "") return must(await api.POST("/instances/{id}/records/{type}", { params: { path }, body }));
      return must(await api.PUT("/instances/{id}/records/{type}/{record}", { params: { path: { ...path, record: id } }, body }));
    },
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["records", instance, type] });
      onDone();
    },
    meta: { toast: false },
  });
  return (
    <form
      className="record-form"
      onSubmit={(e) => {
        e.preventDefault();
        save.mutate();
      }}
    >
      <SchemaForm schema={recordType.schema as Schema} label="" value={value} onChange={setValue} />
      {save.error && <p className="error">{save.error.message}</p>}
      <div className="actions">
        <button className="button primary" disabled={save.isPending}>
          Save
        </button>
        <button type="button" className="button" onClick={onDone}>
          Cancel
        </button>
      </div>
    </form>
  );
}
