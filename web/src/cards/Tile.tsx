import { Link } from "react-router";
import { thumb } from "../api/client";
import type { components } from "../api/schema";

export type Hit = components["schemas"]["Hit"];
export type Names = { instances: Map<number, string>; collections: Map<number, string> };

const badgeClass: Record<string, string> = {
  in_sync: "ok",
  ahead: "ahead",
  remote_changed: "drift",
  diverged: "drift",
  missing: "drift",
  cant_check: "unknown",
};

function str(v: unknown): string {
  return typeof v === "string" ? v : "";
}

export function Badges({ hit, names }: { hit: Hit; names: Names }) {
  return (
    <span className="badges">
      {(hit.targets ?? []).map((t) => (
        <span
          key={t.instance}
          className={"badge " + badgeClass[t.is]}
          role="img"
          aria-label={`${names.instances.get(t.instance) ?? "#" + t.instance}: ${t.is.replace("_", " ")}`}
          title={`${names.instances.get(t.instance) ?? "#" + t.instance}: ${t.is.replace("_", " ")}`}
        />
      ))}
    </span>
  );
}

// alt is empty because the name link beside the image already names the card.
function Avatar({ hit, w }: { hit: Hit; w: 160 | 320 }) {
  const name = str(hit.fields.name);
  const avatar = str(hit.fields.avatar);
  if (!avatar) return <span className="avatar empty">{name.slice(0, 1)}</span>;
  return <img className="avatar" src={thumb(avatar, w)} alt="" loading="lazy" />;
}

export type PickProps = { selected: boolean; locked: boolean; onPick: (shift: boolean) => void };

// The change event of a checkbox comes from a click, so it carries the shift key.
function SelectBox({ hit, pick }: { hit: Hit; pick: PickProps }) {
  return (
    <input
      type="checkbox"
      className="select"
      aria-label={`Select ${str(hit.fields.name)}`}
      checked={pick.selected}
      disabled={pick.locked}
      onChange={(e) => pick.onPick((e.nativeEvent as MouseEvent).shiftKey === true)}
    />
  );
}

export function Tile({ hit, names, pick }: { hit: Hit; names: Names; pick: PickProps }) {
  const tags = hit.tags ?? [];
  const books = hit.lorebooks?.length ?? 0;
  return (
    <article className={pick.selected ? "tile selected" : "tile"}>
      <SelectBox hit={hit} pick={pick} />
      <Link to={`/cards/${hit.id}`} className="tile-image">
        <Avatar hit={hit} w={320} />
        <Badges hit={hit} names={names} />
        {books > 0 && <span className="lorebooks" role="img" aria-label={`${books} ${books === 1 ? "lorebook" : "lorebooks"}`}>
            <span aria-hidden="true">📖</span> {books}
          </span>}
      </Link>
      <Link to={`/cards/${hit.id}`} className="tile-name">
        {str(hit.fields.name)}
      </Link>
      <p className="tile-description">{str(hit.fields.description)}</p>
      <p className="tile-tags">
        {tags.slice(0, 3).map((t) => (
          <span key={t} className="tag">
            {t}
          </span>
        ))}
        {tags.length > 3 && <span className="tag more">+{tags.length - 3}</span>}
      </p>
    </article>
  );
}

export function Row({ hit, names, pick }: { hit: Hit; names: Names; pick: PickProps }) {
  return (
    <tr>
      <td>
        <SelectBox hit={hit} pick={pick} />
      </td>
      <td>
        <Avatar hit={hit} w={160} />
      </td>
      <td>
        <Link to={`/cards/${hit.id}`}>{str(hit.fields.name)}</Link>
      </td>
      <td>{(hit.tags ?? []).join(", ")}</td>
      <td>{hit.lorebooks?.length ?? 0}</td>
      <td>
        <Badges hit={hit} names={names} />
      </td>
    </tr>
  );
}
