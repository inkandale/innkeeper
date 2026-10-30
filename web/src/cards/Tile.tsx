import { Link } from "react-router";
import { thumb } from "../api/client";
import type { components } from "../api/schema";
import { nsfw, text } from "../card/draft";
import { Logo, type App } from "../sync/Logo";
import { stateLabel } from "../sync/sync";
import { Family } from "./Family";

export type Hit = components["schemas"]["Hit"];
type EnrichmentKey = components["schemas"]["EnrichmentKey"];
export type Names = { instances: Map<number, string>; collections: Map<number, string>; lorebooks: Map<number, string>; logos: Map<number, App> };
/** Scorer is an enrich instance's keys and, for the top three scorers by priority, its medal. */
export type Scorer = { keys: EnrichmentKey[]; medal?: (typeof medals)[number] };
export const medals = ["gold", "silver", "bronze"] as const;

export const badgeClass: Record<string, string> = {
  in_sync: "ok",
  ahead: "ahead",
  remote_changed: "drift",
  diverged: "drift",
  missing: "drift",
  cant_check: "unknown",
};

// Logos say where the card is linked; one marker says whether any of those needs the owner.
function Badges({ hit, names }: { hit: Hit; names: Names }) {
  const targets = hit.targets ?? [];
  const name = (instance: number) => names.instances.get(instance) ?? "#" + instance;
  const attention = targets
    .filter((t) => t.is !== "in_sync")
    .map((t) => `${name(t.instance)}: ${stateLabel(t.is)}`)
    .join(" · ");
  return (
    <span className="badges">
      {attention && (
        <span className="attention" role="img" aria-label={`Needs attention: ${attention}`} title={attention}>
          !
        </span>
      )}
      {targets.map((t) => (
        <Logo key={t.instance} app={names.logos.get(t.instance)} label={name(t.instance)} />
      ))}
    </span>
  );
}

// alt is empty because the name link beside the image already names the card.
function Avatar({ hit, w }: { hit: Hit; w: 160 | 320 }) {
  const name = text(hit.fields.name);
  const avatar = text(hit.fields.avatar);
  if (!avatar) return <span className="avatar empty">{name.slice(0, 1)}</span>;
  return <img className={nsfw(hit.tags) ? "avatar nsfw" : "avatar"} src={thumb(avatar, w)} alt="" loading="lazy" />;
}

type PickProps = { selected: boolean; locked: boolean; onPick: (shift: boolean) => void };

// The change event of a checkbox comes from a click, so it carries the shift key.
function SelectBox({ hit, pick }: { hit: Hit; pick: PickProps }) {
  return (
    <input
      type="checkbox"
      className="select"
      aria-label={`Select ${text(hit.fields.name)}`}
      checked={pick.selected}
      disabled={pick.locked}
      onChange={(e) => pick.onPick((e.nativeEvent as MouseEvent).shiftKey === true)}
    />
  );
}

/** enrichmentText formats an enrichment value: a score to one decimal when its range spans 10 or less, else whole. */
export function enrichmentText(value: unknown, range?: number[] | null): string {
  if (typeof value === "number") {
    const span = range && range.length === 2 ? range[1]! - range[0]! : Infinity;
    return String(span <= 10 ? Math.round(value * 10) / 10 : Math.round(value));
  }
  if (Array.isArray(value)) return value.join(", ");
  return String(value);
}

type HitScore = NonNullable<Hit["score"]>;

/** scoreLabel names a score by its key's label, or is undefined when its instance is no scorer. */
function scoreLabel(v: HitScore, scorers: Map<number, Scorer>): string | undefined {
  const key = scorers.get(v.instance)?.keys.find((k) => k.key === v.key);
  if (!key) return undefined;
  const label = `${key.label} ${enrichmentText(v.value, key.range)}`;
  return v.stale ? `${label}, stale: from an older revision` : label;
}

/** Score shows the card's score on its instance's medal, dimmed when it is about an older revision; its title lists every scorer's value. */
function Score({ hit, scorers }: { hit: Hit; scorers: Map<number, Scorer> }) {
  const v = hit.score;
  const s = v && scorers.get(v.instance);
  const key = s?.keys.find((k) => k.key === v?.key);
  if (!v || !s || !key) return null;
  const value = enrichmentText(v.value, key.range);
  const label = scoreLabel(v, scorers)!;
  const others = (hit.scores ?? []).filter((o) => o.instance !== v.instance || o.key !== v.key).map((o) => scoreLabel(o, scorers));
  const title = [label, ...others].filter(Boolean).join("\n");
  return (
    <span className={["score", s.medal, key.type !== "score" && "text", v.stale && "stale"].filter(Boolean).join(" ")} role="img" aria-label={label} title={title}>
      {s.medal && <span aria-hidden="true">★ </span>}
      {value}
    </span>
  );
}

export function Tile({ hit, names, pick, scorers }: { hit: Hit; names: Names; pick: PickProps; scorers: Map<number, Scorer> }) {
  const tags = hit.tags ?? [];
  const books = hit.lorebooks?.length ?? 0;
  return (
    <article className={pick.selected ? "tile selected" : "tile"}>
      <SelectBox hit={hit} pick={pick} />
      <Link to={`/cards/${hit.id}`} className="tile-image">
        <Avatar hit={hit} w={320} />
        <Badges hit={hit} names={names} />
        <Score hit={hit} scorers={scorers} />
        {books > 0 && <span className="lorebooks" role="img" aria-label={`${books} ${books === 1 ? "lorebook" : "lorebooks"}`}>
            <span aria-hidden="true">📖</span> {books}
          </span>}
      </Link>
      <Link to={`/cards/${hit.id}`} className="tile-name">
        {text(hit.fields.name)}
      </Link>
      <Family kind="card" hit={hit} />
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
        <Link to={`/cards/${hit.id}`}>{text(hit.fields.name)}</Link>
        <Family kind="card" hit={hit} />
      </td>
      <td>{(hit.tags ?? []).join(", ")}</td>
      <td>{hit.lorebooks?.length ?? 0}</td>
      <td>
        <Badges hit={hit} names={names} />
      </td>
    </tr>
  );
}

/** LorebookRow is one lorebook in the lorebook list. cards maps linked card IDs to their hits. */
export function LorebookRow({ hit, names, cards, pick }: { hit: Hit; names: Names; cards: Map<number, Hit>; pick: PickProps }) {
  const entries = hit.entries ?? 0;
  return (
    <tr>
      <td>
        <SelectBox hit={hit} pick={pick} />
      </td>
      <td>
        <Link to={`/lorebooks/${hit.id}`}>{text(hit.fields.name)}</Link>
        <Family kind="lorebook" hit={hit} />
      </td>
      <td>
        {entries} {entries === 1 ? "entry" : "entries"}
      </td>
      <td className="linked">
        {(hit.cards ?? []).map((id) => {
          const card = cards.get(id);
          const name = card ? text(card.fields.name) : `Card ${id}`;
          return (
            <Link key={id} to={`/cards/${id}`} aria-label={name} title={name}>
              {card ? <Avatar hit={card} w={160} /> : <span className="avatar empty">#</span>}
            </Link>
          );
        })}
      </td>
      <td>
        <Badges hit={hit} names={names} />
      </td>
    </tr>
  );
}
