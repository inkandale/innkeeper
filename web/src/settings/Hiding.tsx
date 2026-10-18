import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { api, must } from "../api/client";
import { librarySettingsQuery } from "../api/queries";
import type { components } from "../api/schema";
import { JobLine } from "../cards/ImportForm";

type Patch = components["schemas"]["PatchLibrarySettingsInBody"];

const matchLabels: Record<string, string> = { off: "Off", exact: "Exact copies", near: "Near copies" };
const keepLabels: Record<string, string> = { best: "Best score", oldest: "Oldest", newest: "Newest" };

/** Hiding sets how a library's hide job archives low-scoring and duplicate cards, and runs it. */
export function Hiding({ library }: { library: number }) {
  const qc = useQueryClient();
  const settings = useQuery(librarySettingsQuery(library));
  const [job, setJob] = useState<number>();
  const save = useMutation({
    mutationFn: async (body: Patch) =>
      qc.setQueryData(
        librarySettingsQuery(library).queryKey,
        must(await api.PATCH("/libraries/{id}/settings", { params: { path: { id: library } }, body })),
      ),
  });
  const hide = useMutation({
    mutationFn: async () => must(await api.POST("/libraries/{id}/hide", { params: { path: { id: library } } })),
    onSuccess: (j) => setJob(j.id),
  });
  const s = settings.data;
  const near = s?.duplicates.value === "near";
  const deciders = useQuery({
    queryKey: ["instances", "offers", "ai.decide"],
    queryFn: async () => must(await api.GET("/instances", { params: { query: { offers: "ai.decide" } } })),
    enabled: near,
  });
  if (!s) return null;
  // A blank number follows the default.
  const number = (v: string) => (v.trim() === "" ? null : Number(v));
  const nothing = s.hide_below.value == null && s.duplicates.value === "off";
  return (
    <fieldset className="hiding" disabled={save.isPending}>
      <legend>Hiding</legend>
      <label>
        Hide below score{" "}
        <input
          className="input"
          type="number"
          key={String(s.hide_below.value)}
          defaultValue={s.hide_below.overridden ? (s.hide_below.value ?? "") : ""}
          placeholder={s.hide_below.default == null ? "Off" : `Default (${s.hide_below.default})`}
          onBlur={(e) => {
            const v = number(e.target.value);
            if (v !== (s.hide_below.overridden ? s.hide_below.value : null)) save.mutate({ hide_below: v });
          }}
        />
      </label>
      <label>
        Duplicates{" "}
        <select
          className="input"
          value={s.duplicates.overridden ? (s.duplicates.value ?? "") : ""}
          onChange={(e) => save.mutate({ duplicates: e.target.value || null })}
        >
          <option value="">Same as default ({matchLabels[s.duplicates.default ?? "off"]})</option>
          {Object.entries(matchLabels).map(([v, label]) => (
            <option key={v} value={v}>
              {label}
            </option>
          ))}
        </select>
      </label>
      {s.duplicates.value === "near" && (
        <label>
          Distance{" "}
          <input
            className="input"
            type="number"
            min={0}
            max={8}
            key={String(s.duplicate_distance.value)}
            defaultValue={s.duplicate_distance.overridden ? (s.duplicate_distance.value ?? "") : ""}
            placeholder={`Default (${s.duplicate_distance.default})`}
            onBlur={(e) => {
              const v = number(e.target.value);
              if (v !== (s.duplicate_distance.overridden ? s.duplicate_distance.value : null)) save.mutate({ duplicate_distance: v });
            }}
          />
        </label>
      )}
      {near && (deciders.data?.length ?? 0) > 0 && (
        <label>
          Confirm near copies with{" "}
          <select
            className="input"
            value={s.duplicate_decide.overridden ? (s.duplicate_decide.value ?? "") : ""}
            onChange={(e) => save.mutate({ duplicate_decide: e.target.value === "" ? null : Number(e.target.value) })}
          >
            <option value="">
              Same as default ({deciders.data!.find((i) => i.id === s.duplicate_decide.default)?.label ?? "distance only"})
            </option>
            {deciders.data!.map((i) => (
              <option key={i.id} value={i.id}>
                {i.label}
              </option>
            ))}
          </select>
        </label>
      )}
      {s.duplicates.value !== "off" && (
        <label>
          Keep{" "}
          <select
            className="input"
            value={s.duplicate_keep.overridden ? (s.duplicate_keep.value ?? "") : ""}
            onChange={(e) => save.mutate({ duplicate_keep: e.target.value || null })}
          >
            <option value="">Same as default ({keepLabels[s.duplicate_keep.default ?? "best"]})</option>
            {Object.entries(keepLabels).map(([v, label]) => (
              <option key={v} value={v}>
                {label}
              </option>
            ))}
          </select>
        </label>
      )}
      <button className="button" disabled={nothing || hide.isPending} onClick={() => hide.mutate()}>
        Hide now
      </button>
      {job && <JobLine label="Hiding" id={job} to={`/settings/jobs/${job}`} />}
      {(save.error ?? hide.error) && <p className="error">{(save.error ?? hide.error)!.message}</p>}
    </fieldset>
  );
}
