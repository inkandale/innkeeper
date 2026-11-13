import { useQuery } from "@tanstack/react-query";
import { Link } from "react-router";
import { exportFormats, thumb } from "../api/client";
import { entityQuery, instancesQuery, pluginsQuery } from "../api/queries";
import { nsfw, strings, text, type Data } from "../card/draft";
import { afterGreetings, beforeGreetings, greetingList } from "../card/Fields";
import { Icon } from "../shell/Icon";

/** contents lists the card's filled fields in Fields-tab order, with a count where the field holds several. */
function contents(data: Data): [string, number?][] {
  const filled = ([key]: [string, string]) => text(data[key]).trim() !== "";
  const greetings = greetingList(data.alternate_greetings).length;
  const book = data.character_book as { entries?: unknown[] } | undefined;
  const entries = Array.isArray(book?.entries) ? book.entries.length : 0;
  return [
    ...beforeGreetings.filter(filled).map(([, label]): [string] => [label]),
    ...(greetings ? [["Greetings", greetings] as [string, number]] : []),
    ...afterGreetings.filter(filled).map(([, label]): [string] => [label]),
    ...(entries ? [["Lorebook entries", entries] as [string, number]] : []),
  ];
}

type Props = { id: number; onPrev?: () => void; onNext?: () => void; onClose: () => void };

/** Preview shows a gallery card in full beneath its row: art, blurb, every tag, export, and what the card holds. */
export function Preview({ id, onPrev, onNext, onClose }: Props) {
  const card = useQuery(entityQuery("card", id));
  const plugins = useQuery(pluginsQuery);
  const instances = useQuery(instancesQuery);
  const data = (card.data?.revision.data ?? {}) as Data;
  const name = text(data.name) || `#${id}`;
  const creator = text(data.creator);
  const tags = strings(data.tags);
  const avatar = card.data?.revision.avatar;
  // Creator notes are written for people browsing; the description is the prompt, the fallback.
  const blurb = text(data.creator_notes) || text(data.description);
  return (
    <section className="preview" aria-label={`Preview of ${name}`} onKeyDown={(e) => e.key === "Escape" && onClose()}>
      {avatar ? <img className={nsfw(tags) ? "avatar nsfw" : "avatar"} src={thumb(avatar, 640)} alt="" /> : <span className="avatar empty">{name.slice(0, 1)}</span>}
      <div className="preview-body">
        <h2>{name}</h2>
        {creator && <p className="muted">@{creator}</p>}
        {card.isError && <p className="error">{card.error.message}</p>}
        {blurb && <p className="preview-blurb">{blurb}</p>}
        {tags.length > 0 && (
          <p className="preview-tags">
            {tags.map((t) => (
              <span key={t}>#{t}</span>
            ))}
          </p>
        )}
        <Link to={`/cards/${id}`}>Open the page →</Link>
      </div>
      <div className="preview-side">
        <div className="preview-nav">
          <button className="button ghost" aria-label="Previous card" disabled={!onPrev} onClick={onPrev}>
            ‹
          </button>
          <button className="button ghost" aria-label="Next card" disabled={!onNext} onClick={onNext}>
            ›
          </button>
          <button className="button ghost" aria-label="Close preview" onClick={onClose}>
            <Icon name="close" />
          </button>
        </div>
        {exportFormats(plugins.data, instances.data).map((f) => (
          <a key={f} className="button primary" href={`/api/cards/${id}/export?format=${encodeURIComponent(f)}`} download>
            Export {f.toUpperCase()}
          </a>
        ))}
        {card.data && (
          <>
            <h3 className="muted">In this card</h3>
            <ul className="preview-contents">
              {contents(data).map(([label, count]) => (
                <li key={label}>
                  {label}
                  {count !== undefined && <span className="muted">{count}</span>}
                </li>
              ))}
            </ul>
          </>
        )}
      </div>
    </section>
  );
}
