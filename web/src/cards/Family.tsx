import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { Link } from "react-router";
import { api, must, route, type Kind } from "../api/client";
import { text } from "../card/draft";
import { useLibrary } from "../shell/library";
import type { Hit } from "./Tile";

/** Family is a grouped hit's "+N variants". Expanded, it lists every member, marking the ones the search matched. */
export function Family({ kind, hit }: { kind: Kind; hit: Hit }) {
  const [open, setOpen] = useState(false);
  const fam = hit.family;
  if (!fam || fam.size < 2) return null;
  const others = fam.size - 1;
  return (
    <div className="family">
      <button className="button" aria-expanded={open} onClick={() => setOpen(!open)}>
        +{others} {others === 1 ? "variant" : "variants"}
      </button>
      {open && <Members kind={kind} name={text(hit.fields.name)} rootId={fam.root_id} matched={fam.matched ?? []} />}
    </div>
  );
}

function Members({ kind, name, rootId, matched }: { kind: Kind; name: string; rootId: number; matched: number[] }) {
  const body = { library: useLibrary(), filter: { variant_of_root: rootId }, fields: ["name"], page: { size: 500 } };
  const members = useQuery({
    queryKey: ["search", kind, body],
    queryFn: async () => must(await api.POST(route(kind, "/cards/search"), { body })),
  });
  if (members.isPending) return <p className="muted">Loading…</p>;
  if (members.isError) return <p className="error">{members.error.message}</p>;
  return (
    <ul className="plain" aria-label={`Variants of ${name}`}>
      {(members.data.hits ?? []).map((m) => (
        <li key={m.id}>
          <Link to={`/${kind}s/${m.id}`}>{text(m.fields.name) || m.slug}</Link>
          {matched.includes(m.id) && <span className="muted"> matched</span>}
        </li>
      ))}
    </ul>
  );
}
