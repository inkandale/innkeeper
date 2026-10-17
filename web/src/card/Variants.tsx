import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { Link, useNavigate } from "react-router";
import { api, must, mustOk, route, type Kind } from "../api/client";
import { librariesQuery } from "../api/queries";
import { CardPicker } from "../cards/CardPicker";
import type { Entity } from "./Card";

/** useRefresh reloads every card or lorebook page and search, since a link shows on both ends and in grouped lists. */
function useRefresh(kind: Kind) {
  const qc = useQueryClient();
  return () => Promise.all([qc.invalidateQueries({ queryKey: [kind] }), qc.invalidateQueries({ queryKey: ["search"] })]);
}

/** VariantOf links to the card or lorebook this one is a variant of, with × to unlink. */
export function VariantOf({ kind, entity }: { kind: Kind; entity: Entity }) {
  const refresh = useRefresh(kind);
  const unlink = useMutation({
    mutationFn: async () => mustOk(await api.DELETE(route(kind, "/cards/{id}/variant_of"), { params: { path: { id: entity.id } } })),
    onSuccess: refresh,
  });
  const parent = entity.variant_of;
  if (!parent) return null;
  const name = parent.name || parent.slug;
  return (
    <p className="muted">
      Variant of <Link to={`/${kind}s/${parent.id}`}>{name}</Link>
      <button className="chip-x" aria-label={`Unlink from ${name}`} disabled={unlink.isPending} onClick={() => unlink.mutate()}>
        ×
      </button>
    </p>
  );
}

/** Variants lists the direct variants, forks a new one here or into any library, or links an existing one as a variant of this. */
export function Variants({ kind, entity }: { kind: Kind; entity: Entity }) {
  const refresh = useRefresh(kind);
  const navigate = useNavigate();
  const [linking, setLinking] = useState(false);
  const [forking, setForking] = useState(false);
  const libraries = useQuery({ ...librariesQuery, enabled: forking });
  const fork = useMutation({
    mutationFn: async (library?: number) =>
      must(await api.POST(route(kind, "/cards/{id}/variants"), { params: { path: { id: entity.id } }, body: { source: "ui", library } })),
    onSuccess: (v) => {
      void refresh();
      navigate(`/${kind}s/${v.id}`);
    },
  });
  const link = useMutation({
    mutationFn: async (child: number) =>
      mustOk(await api.PUT(route(kind, "/cards/{id}/variant_of"), { params: { path: { id: child } }, body: { id: entity.id } })),
    onSuccess: () => {
      setLinking(false);
      return refresh();
    },
  });
  return (
    <section>
      <h2>Variants</h2>
      <ul className="plain">
        {(entity.variants ?? []).map((v) => (
          <li key={v.id}>
            <Link to={`/${kind}s/${v.id}`}>{v.name || v.slug}</Link>
          </li>
        ))}
      </ul>
      <div className="row">
        <button className="button" disabled={fork.isPending} onClick={() => fork.mutate(undefined)}>
          New variant
        </button>
        <button className="button" aria-expanded={forking} onClick={() => setForking(!forking)}>
          Fork into…
        </button>
        <button className="button" aria-expanded={linking} onClick={() => setLinking(!linking)}>
          Link existing…
        </button>
      </div>
      {forking && (
        <div className="row" role="group" aria-label="Fork into">
          {(libraries.data ?? []).map((l) => (
            <button key={l.id} className="button" disabled={fork.isPending} onClick={() => fork.mutate(l.id)}>
              {l.name}
            </button>
          ))}
        </div>
      )}
      {linking && <CardPicker kind={kind} library={entity.library_id} label="Find a variant" onPick={(id) => !link.isPending && link.mutate(id)} />}
    </section>
  );
}
