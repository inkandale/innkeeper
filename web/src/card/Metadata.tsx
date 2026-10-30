import { useQuery } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { Link } from "react-router";
import { toast } from "sonner";
import type { Kind } from "../api/client";
import { librariesQuery } from "../api/queries";
import { toastError } from "../shell/Toasts";
import type { Entity } from "./Card";

/** Metadata lists a card's or lorebook's stored facts: dates, IDs, library, and provenance. */
export function Metadata({ kind, entity }: { kind: Kind; entity: Entity }) {
  const libraries = useQuery(librariesQuery).data;
  const library = libraries?.find((l) => l.id === entity.library_id);
  const rev = entity.revision;
  const hash = rev.content_hash;
  const copy = () =>
    (navigator.clipboard?.writeText(hash) ?? Promise.reject(new Error("Clipboard unavailable"))).then(
      () => toast.success("Copied the content hash"),
      (e: Error) => toastError(e.message),
    );
  const rows: [string, ReactNode][] = [
    ["Created", new Date(entity.created_at).toLocaleString()],
    ["Last edited", <Link to={`/${kind}s/${entity.id}/history?rev=${rev.id}`}>{new Date(rev.created_at).toLocaleString()}</Link>],
    ["Revision", `${rev.id} (${rev.source})`],
    ["Slug", entity.slug],
    ["ID", entity.id],
    ["Library", library?.name ?? (libraries && `#${entity.library_id}`)],
    ["Provenance", entity.provenance],
    [
      "Content hash",
      <button className="button ghost" title={hash} onClick={() => void copy()}>
        {hash.slice(0, 12)}
      </button>,
    ],
  ];
  return (
    <section>
      <h2>Details</h2>
      <dl className="metadata-list">
        {rows.map(([label, value]) => (
          <div key={label}>
            <dt>{label}</dt>
            <dd>{value}</dd>
          </div>
        ))}
      </dl>
    </section>
  );
}
