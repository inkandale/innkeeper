import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { api, must, route, type Kind } from "../api/client";

/** CardPicker searches the library's cards or lorebooks by text and offers each hit to pick. label names its search box. */
export function CardPicker({ kind, label, onPick }: { kind: Kind; label: string; onPick: (id: number, name: string) => void }) {
  const [text, setText] = useState("");
  const [q, setQ] = useState("");
  const hits = useQuery({
    enabled: !!q,
    queryKey: ["search", kind, "picker", q],
    queryFn: async () => {
      const body = { text: q, page: { size: 10 } };
      return must(await api.POST(route(kind, "/cards/search"), { body }));
    },
  });
  return (
    <form
      className="row"
      role="search"
      onSubmit={(e) => {
        e.preventDefault();
        setQ(text.trim());
      }}
    >
      <input className="input" aria-label={label} placeholder={`${label}…`} value={text} onChange={(e) => setText(e.target.value)} />
      <button className="button">Search</button>
      {hits.data && !hits.data.hits?.length && <span className="muted">No matches</span>}
      {(hits.data?.hits ?? []).map((h) => {
        const name = String(h.fields?.name ?? "#" + h.id);
        return (
          <button key={h.id} type="button" className="button" onClick={() => onPick(h.id, name)}>
            {name}
          </button>
        );
      })}
    </form>
  );
}
