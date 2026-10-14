# Source Updates (Web) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The web shows each followed site source's update, lets the owner review it, ignore it, pause checks, or remove the source, and stops treating `url-import` as an app.

**Architecture:** INN-51, spec `docs/superpowers/specs/2026-10-14-source-updates-design.md`. This plan is 51c. It builds on 51a (`docs/superpowers/plans/2026-10-14-51a-source-updates-core.md`) and follows its "Contract for 51b and 51c" section.
- A **follower** is an instance whose plugin has `pull` and not `push`, such as `url-import`. One helper, `followers`, says which instances those are; every page that tells sources from apps uses it.
- Reviewing an update reuses the Review page, compare, and resolve. The Diff already offers a choice on every conflicting hunk and on the avatar, so taking the data, the avatar, or both needs no new control.
- Ignore this change, Ignore all, Check again, and autosync are calls to `POST /bindings/{id}/dismiss` and `PATCH /bindings/{id}`, wrapped once in `web/src/sync/sync.ts`.

**Tech Stack:** React, TypeScript, TanStack Query, openapi-fetch, Vitest with Testing Library. Task 1 is Go against PostgreSQL.

**Before you start:** 51a must be merged. Its Task 10 already ran `npm run gen`. Confirm the generated schema has the new fields, and never edit `web/openapi.json` or `web/src/api/schema.d.ts` by hand:

```bash
cd web && grep -c '"update"' src/api/schema.d.ts && grep -n 'paused' src/api/schema.d.ts | head -3
```

Expected: a count above 0, and `paused` lines. If not, run `npm run gen` and commit the result before Task 1.

**Tests:** run web tests from `web/` with `npx vitest run <file>`. The Go test needs the database: `$DB` means `INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres`; without it the test skips and proves nothing. No commit may be marked as a breaking change.

**Deviation from the contract:** search's target facets, target filter, and per-hit targets come from `binding_status` on the server, so the web can't drop followed sources from them. Task 1 fixes that in `internal/library/search.go`; it could move into 51a instead.

---

## File map

| File | Change |
|---|---|
| `internal/library/search.go`, `internal/targets/filter_test.go` | Search's targets leave out followed sources |
| `web/src/api/client.ts`, `client.test.ts` | `followers` |
| `web/src/api/queries.ts` | `bindingsQuery`, `useFollowers` |
| `web/src/sync/sync.ts`, `sync.test.ts` | `sourceRemoteId`, `siteOf`, `sourceLabel`, `editBinding`, `dismissBinding`, `removeSource`; `update` is reviewable; `changePath` for updates and lorebook rows |
| `web/src/sync/SyncPage.tsx`, `SyncPage.test.tsx` | Apps need `push`; a Sources line with the update count and Check for updates |
| `web/src/welcome/BringInCards.tsx`, `SetUpPlugins.tsx`, tests | Apps need `push` |
| `web/src/sync/Changes.tsx`, `Changes.test.tsx` | Source entries, lorebook rows, `update` state |
| `web/src/sync/Review.tsx`, `Review.test.tsx` | Followed source review: Bind only, the site's name, the entity's queue place |
| `web/src/card/Upstream.tsx` (new) | `useBindingEdits`, `UpdateBanner` |
| `web/src/card/Panel.tsx`, `Organize.tsx`, `Card.tsx`, `lorebook/Lorebook.tsx`, tests | Sync lists apps only; Sources shows each source's checks; the banner |

---

### Task 1: Search leaves followed sources out of targets

**Files:**
- Modify: `internal/library/search.go:144-147`, `:240`, `:353-354`
- Test: `internal/targets/filter_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/targets/filter_test.go`. It uses 51a's test helpers `addUpstream`, `imported`, `publish`, `follow`, and `vyrCard`:

```go
func TestSearchLeavesOutFollowedSources(t *testing.T) {
	e := setup(t, nil)
	e.addUpstream(nil)
	vyr := e.imported("vyr", vyrCard)
	e.publish("vyr", map[string]any{"name": "Vyr", "description": "A red dragon.", "first_mes": "Begone."}, "")
	if b := e.follow(vyr); b.RemoteState != "changed" {
		t.Fatalf("followed = %+v", b)
	}
	got, err := e.lib.Search(e.ctx, library.Card, library.Query{Include: []string{"targets"}, Facets: []string{"targets"},
		Filter: &library.Filter{IDs: []int64{vyr}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Hits) != 1 || len(got.Hits[0].Targets) != 0 || len(got.Facets.Targets) != 0 {
		t.Fatalf("hits = %+v, facets = %+v", got.Hits, got.Facets)
	}
	ids, err := e.lib.SearchIDs(e.ctx, library.Card, library.Query{Filter: &library.Filter{TargetState: &library.TargetFilter{}}})
	if err != nil || slices.Contains(ids, vyr) {
		t.Fatalf("pushed anywhere = %v, %v", ids, err)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `$DB go test ./internal/targets -run TestSearchLeavesOutFollowedSources -timeout 15m`
Expected: FAIL with `hits = [...Targets:[{Instance:... Is:remote_changed}]...]`.

- [ ] **Step 3: Read targets from pushed bindings only**

In `internal/library/search.go`, above the facet map that holds `"targets"`, add:

```go
// pushedStatus is binding_status for apps the library pushes to. A followed
// site source isn't a target.
const pushedStatus = `(SELECT bs.* FROM binding_status bs JOIN binding b ON b.id = bs.binding_id WHERE b.push)`
```

Change the `"targets"` facet to:

```go
	"targets": func(k *Kind, m string) string {
		return `SELECT instance_id, status, count(*)::int FROM ` + pushedStatus + ` bs
			WHERE ` + k.FK + ` IN (` + m + `) GROUP BY 1, 2 ORDER BY 1, 2`
	},
```

Change the `TargetState` filter's return to:

```go
		return `EXISTS (SELECT 1 FROM ` + pushedStatus + ` bs WHERE ` + cond + `)`, nil
```

Change the `"targets"` include to:

```go
		case "targets":
			targets = `(SELECT jsonb_agg(jsonb_build_object('instance', instance_id, 'is', status) ORDER BY instance_id)
				FROM ` + pushedStatus + ` bs WHERE ` + k.FK + ` = c.id)`
```

- [ ] **Step 4: Run the tests**

Run: `$DB go test ./internal/targets ./internal/library -timeout 15m`
Expected: PASS, including `TestSearchTargets` and `TestFilterTargetState`.

- [ ] **Step 5: Commit**

```bash
git add internal/library/search.go internal/targets/filter_test.go
git commit -m "fix(search): leave followed site sources out of targets"
```

---

### Task 2: Shared helpers for followed sources

**Files:**
- Modify: `web/src/api/client.ts`, `web/src/api/queries.ts`, `web/src/sync/sync.ts`
- Test: `web/src/api/client.test.ts`, `web/src/sync/sync.test.ts`

- [ ] **Step 1: Write the failing tests**

In `web/src/api/client.test.ts`, add `followers` to the import from `./client`, and append:

```ts
test("followers are the instances whose plugin pulls without pushing", () => {
  const plugins = [
    { name: "app", capabilities: ["push", "pull"] },
    { name: "url-import", capabilities: ["import", "sites", "pull"] },
    { name: "codec", capabilities: ["codec"] },
  ];
  const instances = [{ id: 1, plugin: "app" }, { id: 2, plugin: "url-import" }, { id: 3, plugin: "codec" }];
  expect([...followers(instances, plugins)]).toEqual([2]);
  expect(followers(instances, undefined).size).toBe(0);
});
```

In `web/src/sync/sync.test.ts`, add `changePath`, `queueReviews`, `siteOf`, `sourceLabel`, and `sourceRemoteId` to the import from `./sync`, and append:

```ts
test("a followed source's state names its site", () => {
  expect(siteOf("chub:alice/vyr")).toBe("chub");
  expect(siteOf("odd")).toBe("odd");
  expect(sourceRemoteId({ kind: "character-tavern", ref: "u/vyr" })).toBe("character-tavern:u/vyr");
  expect(sourceLabel("update", "risu:abc")).toBe("update on risu");
  expect(sourceLabel("changed", "risu:abc")).toBe("update on risu");
  expect(sourceLabel("missing", "chub:a/b")).toBe("no longer on chub");
  expect(sourceLabel("unchanged", "chub:a/b")).toBe("up to date");
});

test("a source update reviews against its card or lorebook, three ways", () => {
  const c = { instance_id: 4, state: "update" as const, remote_id: "chub:alice/vyr", binding_id: 21 };
  expect(changePath({ ...c, card_id: 8 })).toBe("/sync/4/card/chub%3Aalice%2Fvyr?card=8&queue=1");
  expect(changePath({ ...c, lorebook_id: 9 }, { state: "update" })).toBe("/sync/4/lorebook/chub%3Aalice%2Fvyr?card=9&queue=1&state=update");
  expect(queueReviews([{ lorebook_id: 9, apps: [c] }])).toEqual([{ ...c, card_id: undefined, lorebook_id: 9 }]);
});
```

- [ ] **Step 2: Run them to verify they fail**

Run: `cd web && npx vitest run src/api/client.test.ts src/sync/sync.test.ts`
Expected: FAIL: `followers is not a function`, `siteOf is not a function`.

- [ ] **Step 3: Add `followers`**

In `web/src/api/client.ts`, after `withCapability`, add:

```ts
/** followers are the instances that follow site sources: their plugin pulls but doesn't push. */
export function followers(
  instances: { id: number; plugin: string }[] | null | undefined,
  plugins: { name: string; capabilities?: string[] | null }[] | null | undefined,
): Set<number> {
  const pushers = new Set(withCapability(instances, plugins, "push").map((i) => i.id));
  return new Set(withCapability(instances, plugins, "pull").filter((i) => !pushers.has(i.id)).map((i) => i.id));
}
```

- [ ] **Step 4: Add the queries**

Replace `web/src/api/queries.ts`'s import lines with:

```ts
import { queryOptions, useQuery } from "@tanstack/react-query";
import { api, followers, must, route, withCapability, type Kind } from "./client";
```

and append:

```ts
/** useFollowers returns the IDs of the instances that follow site sources, empty until both lists load. */
export function useFollowers() {
  const instances = useQuery(instancesQuery);
  const plugins = useQuery(pluginsQuery);
  return followers(instances.data, plugins.data);
}

/** bindingsQuery is a card's or lorebook's bindings: the apps it is pushed to and the site sources it follows. */
export const bindingsQuery = (kind: Kind, id: number) =>
  queryOptions({
    queryKey: [kind, id, "bindings"],
    queryFn: async () => must(await api.GET(route(kind, "/cards/{id}/bindings"), { params: { path: { id } } })),
  });
```

- [ ] **Step 5: Add the source helpers to `sync.ts`**

In `web/src/sync/sync.ts`, change the first import to:

```ts
import { api, must, mustOk, route, type Kind } from "../api/client";
```

Replace `reviewable`, `QueueChange`, `queueReviews`, and `changePath` with:

```ts
/** reviewable changes open Review; the others are pushed again, unbound, ignored, or only shown. */
export const reviewable = (c: AppChange) =>
  c.state === "differs" || c.state === "both_changed" || c.state === "new" || c.state === "ahead" || c.state === "update";

/** QueueChange is a queue entry with its row's card or lorebook, which Review compares with when there's no snapshot. */
type QueueChange = AppChange & { card_id?: number; lorebook_id?: number };

/** queueReviews lists the queue's reviewable entries in order. */
export const queueReviews = (rows: { card_id?: number; lorebook_id?: number; apps: AppChange[] | null }[]): QueueChange[] =>
  rows.flatMap((r) => (r.apps ?? []).filter(reviewable).map((c) => ({ ...c, card_id: r.card_id, lorebook_id: r.lorebook_id })));
```

and, keeping the `changePath` doc comment, add one sentence to it, "A source update compares three ways with its card or lorebook.", then replace its body:

```ts
export const changePath = (c: QueueChange, f: ChangesFilter = {}) =>
  c.state === "update"
    ? reviewPath(c.instance_id, c.lorebook_id ? "lorebook" : "card", c.remote_id ?? "", { card: c.lorebook_id ?? c.card_id, queue: 1, ...f })
    : reviewPath(
        c.instance_id,
        "card",
        c.remote_id ?? "",
        c.state === "ahead" ? { card: c.card_id, base: "none", queue: 1, ...f } : { job: c.job_id ?? 0, queue: 1, ...f },
      );
```

The `queueReviews` test expects `card_id: undefined` in the result: `toEqual` treats a key set to `undefined` as absent, so it passes either way.

Append to `sync.ts`:

```ts
/** sourceRemoteId is the remote ID of a followed source's binding: its kind and ref, as the core forms it. */
export const sourceRemoteId = (s: { kind: string; ref: string }) => `${s.kind}:${s.ref}`;

/** siteOf names a followed source's site: the kind before the remote ID's colon. */
export const siteOf = (remoteId: string) => {
  const i = remoteId.indexOf(":");
  return i > 0 ? remoteId.slice(0, i) : remoteId;
};

/** sourceLabel names a followed source's queue state or binding remote_state. */
export function sourceLabel(state: string, remoteId: string): string {
  const site = siteOf(remoteId);
  const labels: Record<string, string> = {
    update: `update on ${site}`,
    changed: `update on ${site}`,
    missing: `no longer on ${site}`,
    error: "check failing",
    unchanged: "up to date",
    unknown: "not checked yet",
  };
  return labels[state] ?? state;
}

/** BindingEdit is what PATCH /bindings/{id} changes; a field left out stays as it is. */
export type BindingEdit = { autosync?: boolean; paused?: boolean };

export async function editBinding(id: number, body: BindingEdit) {
  return must(await api.PATCH("/bindings/{id}", { params: { path: { id } }, body }));
}

/** dismissBinding ignores a followed source's change; the next one on the site shows again. */
export async function dismissBinding(id: number) {
  return must(await api.POST("/bindings/{id}/dismiss", { params: { path: { id } } }));
}

/** removeSource removes the card's or lorebook's source that a followed binding's remote ID names. */
export async function removeSource(kind: Kind, id: number, remoteId: string) {
  const list = must(await api.GET(route(kind, "/cards/{id}/sources"), { params: { path: { id } } }));
  const s = list.find((s) => sourceRemoteId(s) === remoteId);
  if (!s) return;
  mustOk(await api.DELETE(route(kind, "/cards/{id}/sources/{source_id}"), { params: { path: { id, source_id: s.id } } }));
}
```

- [ ] **Step 6: Run the tests and typecheck**

Run: `cd web && npx vitest run src/api/client.test.ts src/sync/sync.test.ts && npm run typecheck`
Expected: PASS, no type errors.

- [ ] **Step 7: Commit**

```bash
git add web/src/api web/src/sync/sync.ts web/src/sync/sync.test.ts
git commit -m "feat(web): helpers for followed site sources"
```

---

### Task 3: An app needs `push`

`url-import` now declares `pull` without `push`. These places pick apps by `pull` and would list it.

**Files:**
- Modify: `web/src/sync/SyncPage.tsx:59`, `web/src/welcome/BringInCards.tsx:74`, `web/src/welcome/SetUpPlugins.tsx:53`
- Test: `web/src/welcome/BringInCards.test.tsx`, `web/src/welcome/SetUpPlugins.test.tsx`

SyncPage's test comes in Task 7, with its Sources line.

- [ ] **Step 1: Write the failing tests**

Append to `web/src/welcome/BringInCards.test.tsx`:

```ts
test("a plugin that only follows site sources isn't offered as an app to sync", async () => {
  plugins = [{ ...plugin("url-import", ["import", "sites", "pull"]), inputs: ["url"] }];
  instances = [instance(7, "url-import", "url-import")];
  renderStep();
  expect(await screen.findByLabelText("Import from a page URL")).toBeInTheDocument();
  expect(screen.queryByRole("button", { name: "Sync url-import" })).not.toBeInTheDocument();
});
```

Append to `web/src/welcome/SetUpPlugins.test.tsx`:

```ts
test("a plugin that pulls without pushing moves on without checking an app", async () => {
  installed = [plugin("site-follower", ["pull"])];
  renderAt("/welcome/setup/site-follower");
  expect(await screen.findByText("cards step")).toBeInTheDocument();
  expect(sent).toEqual([{ method: "POST", path: "/api/instances", body: { plugin: "site-follower", name: "site-follower" } }]);
});
```

- [ ] **Step 2: Run them to verify they fail**

Run: `cd web && npx vitest run src/welcome`
Expected: FAIL: the Sync url-import button is found, and `sent` also holds `GET /api/instances/9/items`.

- [ ] **Step 3: Require `push`**

In `web/src/welcome/BringInCards.tsx`, replace the `apps` line with:

```ts
  // An app is read and pushed to; a plugin that only pulls follows site sources instead.
  const apps = withCapability(withCapability(instances.data, plugins.data, "pull"), plugins.data, "push");
```

In `web/src/welcome/SetUpPlugins.tsx`, replace the first line of `afterSave` with:

```ts
    if (!plugin.capabilities?.includes("pull") || !plugin.capabilities.includes("push")) return navigate(next);
```

In `web/src/sync/SyncPage.tsx`, replace the `targets` line with:

```ts
  const targets = withCapability(instances.data, plugins.data, "push");
```

- [ ] **Step 4: Run the tests**

Run: `cd web && npx vitest run src/welcome src/sync/SyncPage.test.tsx`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/welcome web/src/sync/SyncPage.tsx
git commit -m "fix(web): count only plugins that push as apps"
```

---

### Task 4: Source entries in the changes queue

**Files:**
- Modify: `web/src/sync/Changes.tsx`
- Test: `web/src/sync/Changes.test.tsx`

- [ ] **Step 1: Write the failing test**

In `web/src/sync/Changes.test.tsx`, after the `cmp` constant, add:

```ts
const followed = {
  rows: [
    { lorebook_id: 9, name: "Forge lore", apps: [{ instance_id: 4, state: "missing", remote_id: "chub:alice/forge", binding_id: 22 }] },
    { card_id: 8, name: "Vyr", apps: [{ instance_id: 4, state: "update", remote_id: "chub:alice/vyr", binding_id: 21 }] },
  ],
  counts: { "4": { update: 1, missing: 1 } },
};
```

and append:

```ts
test("a followed source's entries offer review, ignoring, and removing the source", async () => {
  const base = fetch;
  const sent: { method: string; path: string; body?: unknown }[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (req: Request) => {
      const u = new URL(req.url);
      if (u.pathname === "/api/sync/changes") return json(followed);
      if (u.pathname === "/api/instances") return json([{ id: 4, name: "URL import", plugin: "url-import" }]);
      if (u.pathname === "/api/plugins") return json([{ name: "url-import", capabilities: ["import", "sites", "pull"] }]);
      if (req.method === "GET" && u.pathname === "/api/lorebooks/9/sources")
        return json([{ id: 30, kind: "chub", ref: "alice/forge", linked_by: "auto", created_at: "" }]);
      if (req.method !== "GET") {
        sent.push({ method: req.method, path: u.pathname, ...(req.method === "PATCH" ? { body: await req.json() } : {}) });
        return req.method === "DELETE" ? new Response(null, { status: 204 }) : json({ id: 21, instance_id: 4, remote_id: "chub:alice/vyr" });
      }
      return base(req);
    }),
  );
  renderChanges();
  const vyr = await screen.findByRole("row", { name: /Vyr/ });
  // Checkbox, Card, URL import, Action.
  await waitFor(() => expect(within(vyr).getAllByRole("cell")[2]).toHaveTextContent("update on chub"));
  expect(within(vyr).getByRole("link", { name: "Review" })).toHaveAttribute("href", "/sync/4/card/chub%3Aalice%2Fvyr?card=8&queue=1");
  const forge = screen.getByRole("row", { name: /Forge lore/ });
  expect(within(forge).getByRole("link", { name: "Forge lore" })).toHaveAttribute("href", "/lorebooks/9");
  expect(within(forge).getAllByRole("cell")[2]).toHaveTextContent("no longer on chub");
  expect(within(forge).queryByRole("button", { name: /Push again|Unbind/ })).not.toBeInTheDocument();

  const click = async (row: HTMLElement, name: string) => {
    const b = within(row).getByRole("button", { name });
    await waitFor(() => expect(b).toBeEnabled());
    await userEvent.click(b);
  };
  await click(vyr, "Ignore this change");
  await click(vyr, "Ignore all");
  await click(forge, "Remove source");
  await waitFor(() =>
    expect(sent).toEqual([
      { method: "POST", path: "/api/bindings/21/dismiss" },
      { method: "PATCH", path: "/api/bindings/21", body: { paused: true } },
      { method: "DELETE", path: "/api/lorebooks/9/sources/30" },
    ]),
  );
});
```

- [ ] **Step 2: Run it to verify it fails**

Run: `cd web && npx vitest run src/sync/Changes.test.tsx`
Expected: FAIL: the URL import cell reads `update`, not `update on chub`.

- [ ] **Step 3: Show source entries and their actions**

In `web/src/sync/Changes.tsx`:

Change the queries import to `import { instancesQuery, useFollowers } from "../api/queries";`, and the `./sync` import to:

```ts
import {
  bulkResolutions,
  changePath,
  changesFilter,
  compareItem,
  dismissBinding,
  editBinding,
  pushSummary,
  queueReviews,
  removeSource,
  reviewable,
  sourceLabel,
  stateLabel,
  type AppChange,
  type Option,
} from "./sync";
```

Replace `states` and `rowKey` with:

```ts
const states = ["differs", "both_changed", "new", "error", "ahead", "missing", "update"] as const;
const rowKey = (r: ChangeRow) =>
  r.card_id ? `card/${r.card_id}` : r.lorebook_id ? `lorebook/${r.lorebook_id}` : `app/${r.apps?.[0]?.instance_id}/${r.apps?.[0]?.remote_id}`;
```

After `const name = ...`, add:

```ts
  const follows = useFollowers();
```

After the `unbind` mutation, add:

```ts
  // A followed source has no app copy to push or unbind: its owner ignores the change, stops checking, or drops the source.
  const source = useMutation({
    mutationFn: async ({ r, c, act }: { r: ChangeRow; c: AppChange; act: "dismiss" | "pause" | "remove" }) => {
      const binding = c.binding_id ?? 0;
      if (act === "dismiss") await dismissBinding(binding);
      else if (act === "pause") await editBinding(binding, { paused: true });
      else if (r.lorebook_id) await removeSource("lorebook", r.lorebook_id, c.remote_id ?? "");
      else await removeSource("card", r.card_id ?? 0, c.remote_id ?? "");
    },
    onSuccess: done,
  });

  const sourceActions = (r: ChangeRow, c: AppChange) => {
    const button = (text: string, act: "dismiss" | "pause" | "remove") => (
      <button className="button" disabled={source.isPending} onClick={() => source.mutate({ r, c, act })}>
        {text}
      </button>
    );
    switch (c.state) {
      case "update":
        return (
          <>
            <Link to={changePath({ ...c, card_id: r.card_id, lorebook_id: r.lorebook_id }, filter)}>Review</Link>{" "}
            {button("Ignore this change", "dismiss")} {button("Ignore all", "pause")}
          </>
        );
      case "missing":
        return (
          <>
            {button("Ignore all", "pause")} {button("Remove source", "remove")}
          </>
        );
      default:
        return <span className="error">{c.error}</span>;
    }
  };
```

Make `actions` start with:

```ts
  const actions = (r: ChangeRow, c: AppChange) => {
    if (follows.has(c.instance_id)) return sourceActions(r, c);
```

Replace the name cell with:

```tsx
                  <td>
                    {r.card_id ? (
                      <Link to={`/cards/${r.card_id}`}>{r.name}</Link>
                    ) : r.lorebook_id ? (
                      <Link to={`/lorebooks/${r.lorebook_id}`}>{r.name}</Link>
                    ) : (
                      r.name
                    )}
                  </td>
```

and, in the per-app cell, replace `{stateLabel(c.state)}` with:

```tsx
{follows.has(c.instance_id) ? sourceLabel(c.state, c.remote_id ?? "") : stateLabel(c.state)}
```

Update `Changes`' doc comment to: "Changes lists every card some app needs the owner for, and every card or lorebook whose followed source changed, one row each, and settles the checked ones."

- [ ] **Step 4: Run the tests**

Run: `cd web && npx vitest run src/sync/Changes.test.tsx && npm run typecheck`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/sync/Changes.tsx web/src/sync/Changes.test.tsx
git commit -m "feat(web): source updates, gone sources, and failing checks in the changes queue"
```

---

### Task 5: Reviewing a source update

**Files:**
- Modify: `web/src/sync/Review.tsx`
- Test: `web/src/sync/Review.test.tsx`

- [ ] **Step 1: Write the failing test**

Append to `web/src/sync/Review.test.tsx`:

```ts
test("a followed source's update merges three ways with the site, with Bind its only action", async () => {
  const base = fetch;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (req: Request) => {
      const u = new URL(req.url);
      const path = decodeURIComponent(u.pathname);
      if (path === "/api/instances") return json([{ id: 4, name: "URL import", plugin: "url-import" }]);
      if (path === "/api/plugins") return json([{ name: "url-import", capabilities: ["import", "sites", "pull"] }]);
      if (path === "/api/instances/4/cards/chub:alice/vyr/compare") {
        compares.push(u.search);
        return json({ ...cardCmp, instance_id: 4, remote_id: "chub:alice/vyr", card_id: 9, accepted_hash: "acc", base: "accepted" });
      }
      if (path === "/api/instances/4/cards/chub:alice/vyr/resolve") {
        resolved.push(await req.json());
        return json({ remote_id: "chub:alice/vyr", action: "bind", card_id: 9 });
      }
      return base(req);
    }),
  );
  // Two cards follow one source; the URL's card says which entry this is.
  changes = {
    rows: [
      { card_id: 8, name: "Vyr", apps: [{ instance_id: 4, state: "update", remote_id: "chub:alice/vyr", binding_id: 21 }] },
      { card_id: 9, name: "Vyr, rewritten", apps: [{ instance_id: 4, state: "update", remote_id: "chub:alice/vyr", binding_id: 22 }] },
    ],
    counts: {},
  };
  const router = renderReview("/sync/4/card/chub%3Aalice%2Fvyr?card=9&queue=1");
  const user = userEvent.setup();
  expect(await screen.findByText("2 of 2")).toBeInTheDocument();
  expect(compares).toEqual(["?card_id=9"]);
  expect(screen.queryByRole("radiogroup", { name: "Action" })).not.toBeInTheDocument();
  expect(screen.getByText(/The site isn't changed/)).toBeInTheDocument();
  await user.click(await screen.findByLabelText("chub", { selector: "[name='card:chub:alice/vyr/description#1']" }));
  await user.click(screen.getByRole("button", { name: "Resolve" }));
  await waitFor(() => expect(at(router)).toBe("/sync/changes"));
  expect(resolved).toEqual([expect.objectContaining({ action: "bind", card_id: 9, accepted_hash: "acc", choices: { "description#1": "app" } })]);
  expect(resolved[0]).not.toHaveProperty("base");
});
```

- [ ] **Step 2: Run it to verify it fails**

Run: `cd web && npx vitest run src/sync/Review.test.tsx`
Expected: FAIL: `1 of 2` instead of `2 of 2`, since the queue place matches on the source alone.

- [ ] **Step 3: Review a followed source**

In `web/src/sync/Review.tsx`, add `import { useFollowers } from "../api/queries";`, and add `siteOf` to the `./sync` import.

After `const filter = changesFilter(search);`, add:

```ts
  const followed = useFollowers().has(instance);
```

Replace the `at` line with:

```ts
  // Several cards may follow one source, so a followed source's place in the queue is its card's or lorebook's.
  const entity = Number(search.get("card"));
  const at = reviews.findIndex(
    (c) => c.instance_id === instance && c.remote_id === remoteId && (!followed || (c.lorebook_id ?? c.card_id) === entity),
  );
```

Replace the `actions` declaration with:

```ts
  // A followed source can only merge into its card: importing or skipping it would mean nothing.
  const actions: Option["action"][] = followed
    ? ["bind"]
    : (["bind", "import", "skip"] as const).filter(
        (a) =>
          !item ||
          (item.options ?? []).some((o) => o.action === a) ||
          (a === "bind" && card > 0 && item.verdict !== "error"),
      );
```

Keep the comment above it about the snapshot's options; it still applies to the second branch.

In the resolve mutation's `onSuccess`, replace the toast line with:

```ts
      toast({ text: followed ? `Resolved ${name}.` : `Resolved ${item?.name || remoteId}. Push to send the library's side to the app.` });
```

Wrap the Action radiogroup `<span className="row" role="radiogroup" aria-label="Action">…</span>` in `{!followed && (…)}`.

Replace the muted line under the suggestion with:

```tsx
        <p className="muted">
          {followed
            ? "Resolving saves the merge as a new revision. The site isn't changed."
            : "Resolving changes the library and the binding, not the app. Push afterwards."}
        </p>
```

and give the main `Diff` the site's name for the app side:

```tsx
            <Diff
              scope={`${kind === "card" ? "card" : "book"}:${remoteId}`}
              cmp={cmp.data}
              sides={followed ? { library: "Library", app: siteOf(remoteId) } : undefined}
              choices={choices}
              onChoose={act === "bind" ? (id, c) => setChoices({ ...choices, [id]: c }) : undefined}
            />
```

- [ ] **Step 4: Run the tests**

Run: `cd web && npx vitest run src/sync && npm run typecheck`
Expected: PASS, including every earlier Review test.

- [ ] **Step 5: Commit**

```bash
git add web/src/sync/Review.tsx web/src/sync/Review.test.tsx
git commit -m "feat(web): review a site source's update"
```

---

### Task 6: The card and lorebook pages

**Files:**
- Create: `web/src/card/Upstream.tsx`
- Modify: `web/src/card/Panel.tsx` (`Sync`), `web/src/card/Organize.tsx` (`Sources`), `web/src/card/Card.tsx`, `web/src/lorebook/Lorebook.tsx`
- Test: `web/src/card/Card.test.tsx`, `web/src/lorebook/Lorebook.test.tsx`

- [ ] **Step 1: Write the failing card tests**

In `web/src/card/Card.test.tsx`, after the `entity` constant, add:

```ts
const lumiverse = { id: 1, instance_id: 2, remote_id: "r", ahead: true, remote_state: "unchanged", autosync: false, push: true, paused: false, check_failures: 0 };
const chub = {
  id: 2, instance_id: 4, remote_id: "chub:dg/brakka", status: "in_sync", ahead: false, remote_state: "unchanged",
  autosync: false, push: false, paused: false, check_failures: 0,
};
const chubChanged = { ...chub, status: "remote_changed", remote_state: "changed" };
```

Add `let followed: (typeof chub)[];` and `let sent: string[];` beside the other `let`s, and `followed = [];` and `sent = [];` at the top of `beforeEach`.

In the fetch mock, right after the PATCH block, add:

```ts
      if (req.method === "POST" && path === "/api/bindings/2/dismiss") {
        sent.push(path);
        followed = followed.map((b) => ({ ...b, status: "in_sync", remote_state: "unchanged" }));
        return json(followed[0]);
      }
      if (req.method === "POST" && path === "/api/cards/7/source-check") {
        sent.push(path);
        followed = [chubChanged];
        return json([{ ...lumiverse, status: bindingStatus }, ...followed]);
      }
```

Replace the `/api/cards/7/bindings` reply with:

```ts
      if (path === "/api/cards/7/bindings") return json([{ ...lumiverse, status: bindingStatus }, ...followed]);
```

and add `{ id: 4, name: "URL import", plugin: "url-import" }` to the `/api/instances` reply, after Lumiverse.

Append:

```ts
test("a followed source's update shows a banner that reviews it or ignores this change", async () => {
  followed = [chubChanged];
  renderAt("/cards/7");
  expect(await screen.findByText(/Update on chub/)).toBeInTheDocument();
  expect(screen.getByRole("link", { name: "Review" })).toHaveAttribute("href", "/sync/4/card/chub%3Adg%2Fbrakka?card=7");
  expect(screen.queryByText(/URL import/)).not.toBeInTheDocument();
  await userEvent.click(screen.getByRole("button", { name: "Ignore this change" }));
  await waitFor(() => expect(screen.queryByText(/Update on chub/)).not.toBeInTheDocument());
  expect(sent).toEqual(["/api/bindings/2/dismiss"]);
});

test("Sources show each followed source's checks, with autosync and Ignore all", async () => {
  followed = [chub];
  renderAt("/cards/7");
  expect(await screen.findByText(/up to date/)).toBeInTheDocument();
  await userEvent.click(screen.getByRole("checkbox", { name: "Autosync from chub: dg/brakka" }));
  await waitFor(() => expect(patches).toEqual([{ path: "/api/bindings/2", body: { autosync: true } }]));
  const pause = screen.getByRole("button", { name: "Ignore all" });
  await waitFor(() => expect(pause).toBeEnabled());
  await userEvent.click(pause);
  await waitFor(() => expect(patches[1]).toEqual({ path: "/api/bindings/2", body: { paused: true } }));
});

test("a paused source says so, and Check again resumes it", async () => {
  followed = [{ ...chub, paused: true }];
  renderAt("/cards/7");
  expect(await screen.findByText(/Not checking for updates/)).toBeInTheDocument();
  await userEvent.click(screen.getByRole("button", { name: "Check again" }));
  await waitFor(() => expect(patches).toEqual([{ path: "/api/bindings/2", body: { paused: false } }]));
});

test("Check for updates checks the card's sources now", async () => {
  renderAt("/cards/7");
  await userEvent.click(await screen.findByRole("button", { name: "Check for updates" }));
  await waitFor(() => expect(sent).toEqual(["/api/cards/7/source-check"]));
  expect(await screen.findByText(/Update on chub/)).toBeInTheDocument();
});
```

- [ ] **Step 2: Write the failing lorebook test**

In `web/src/lorebook/Lorebook.test.tsx`, add `let bindings: unknown[];` beside `let head`, `bindings = [];` in `beforeEach`, and this line to the fetch mock before `return json([]);`:

```ts
      if (path === "/api/lorebooks/3/bindings") return json(bindings);
```

Append:

```ts
test("a lorebook's followed source update shows a banner", async () => {
  bindings = [
    { id: 5, instance_id: 4, remote_id: "chub:alice/forge", status: "remote_changed", ahead: false, remote_state: "changed",
      autosync: false, push: false, paused: false, check_failures: 0 },
  ];
  renderAt("/lorebooks/3");
  expect(await screen.findByText(/Update on chub/)).toBeInTheDocument();
  expect(screen.getByRole("link", { name: "Review" })).toHaveAttribute("href", "/sync/4/lorebook/chub%3Aalice%2Fforge?card=3");
});
```

- [ ] **Step 3: Run them to verify they fail**

Run: `cd web && npx vitest run src/card/Card.test.tsx src/lorebook/Lorebook.test.tsx`
Expected: FAIL: no `Update on chub` text, no `Check for updates` button.

- [ ] **Step 4: Create `Upstream.tsx`**

Create `web/src/card/Upstream.tsx`:

```tsx
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "react-router";
import { api, must, route, type Kind } from "../api/client";
import { bindingsQuery } from "../api/queries";
import { dismissBinding, editBinding, reviewPath, siteOf, type BindingEdit } from "../sync/sync";

/** useBindingEdits changes a card's or lorebook's bindings, or checks its sources now, then refreshes them and the queue. */
export function useBindingEdits(kind: Kind, id: number) {
  const qc = useQueryClient();
  const key = bindingsQuery(kind, id).queryKey;
  const done = () => Promise.all([qc.invalidateQueries({ queryKey: key }), qc.invalidateQueries({ queryKey: ["sync", "changes"] })]);
  const edit = useMutation({
    mutationFn: ({ binding, body }: { binding: number; body: BindingEdit }) => editBinding(binding, body),
    onSuccess: done,
  });
  const dismiss = useMutation({ mutationFn: dismissBinding, onSuccess: done });
  const check = useMutation({
    mutationFn: async () => must(await api.POST(route(kind, "/cards/{id}/source-check"), { params: { path: { id } } })),
    onSuccess: (list) => {
      qc.setQueryData(key, list);
      void qc.invalidateQueries({ queryKey: ["sync", "changes"] });
    },
  });
  return { edit, dismiss, check };
}

/** UpdateBanner offers each followed source's update: review it, or ignore this change. */
export function UpdateBanner({ kind, id }: { kind: Kind; id: number }) {
  const bindings = useQuery(bindingsQuery(kind, id));
  const { dismiss } = useBindingEdits(kind, id);
  const updates = (bindings.data ?? []).filter((b) => !b.push && !b.paused && b.remote_state === "changed");
  return (
    <>
      {updates.map((b) => (
        <p key={b.id} className="banner">
          Update on {siteOf(b.remote_id)}. <Link to={reviewPath(b.instance_id, kind, b.remote_id, { card: id })}>Review</Link>{" "}
          <button className="button" disabled={dismiss.isPending} onClick={() => dismiss.mutate(b.id)}>
            Ignore this change
          </button>
        </p>
      ))}
    </>
  );
}
```

- [ ] **Step 5: Sync lists apps only**

In `web/src/card/Panel.tsx`, change the queries import to `import { bindingsQuery, instancesQuery, pluginsQuery, useInstancesWith } from "../api/queries";`, add `import { useBindingEdits } from "./Upstream";`, and replace `Sync`'s body up to its `return` with:

```tsx
export function Sync({ kind, id }: { kind: Kind; id: number }) {
  const bindings = useQuery(bindingsQuery(kind, id));
  const instances = useQuery(instancesQuery);
  const plugins = useQuery(pluginsQuery);
  const logos = apps(instances.data, plugins.data);
  const name = (n: number) => instances.data?.find((i) => i.id === n)?.name ?? `#${n}`;
  const { edit } = useBindingEdits(kind, id);
  // Followed site sources show under Sources.
  const pushed = (bindings.data ?? []).filter((b) => b.push);
  if (!pushed.length) return null;
```

In its JSX, map over `pushed` instead of `bindings.data`, and change the autosync checkbox's `disabled` and `onChange` to:

```tsx
                disabled={edit.isPending}
                onChange={(e) => edit.mutate({ binding: b.id, body: { autosync: e.target.checked } })}
```

Delete the old `qc` and `setAutosync` lines from `Sync`.

- [ ] **Step 6: Sources show each source's checks**

In `web/src/card/Organize.tsx`, add to the imports:

```ts
import type { components } from "../api/schema";
import { bindingsQuery } from "../api/queries";
import { sourceLabel, sourceRemoteId } from "../sync/sync";
import { useBindingEdits } from "./Upstream";
```

(merge `bindingsQuery` into the existing `../api/queries` import). Add `type Binding = components["schemas"]["Binding"];` below the imports.

Change the `Sources` doc comment to: "Sources lists the sites a card or lorebook was published on, and how checking each for updates stands, with remove, add by URL, and a check now."

In `Sources`, after the `remove` mutation, add:

```ts
  const bindings = useQuery(bindingsQuery(kind, id));
  const edits = useBindingEdits(kind, id);
  const following = (s: { kind: string; ref: string }) => bindings.data?.find((b) => !b.push && b.remote_id === sourceRemoteId(s));
```

Replace the list's `map` callback so each source shows its checks:

```tsx
        {(list.data ?? []).map((s) => {
          const b = following(s);
          return (
            <li key={s.id}>
              {/* Only https links, so a stored URL can't run script. */}
              {s.url?.startsWith("https://") ? (
                <a href={s.url} target="_blank" rel="noreferrer">
                  {s.kind}: {s.ref}
                </a>
              ) : (
                `${s.kind}: ${s.ref}`
              )}
              <button className="chip-x" aria-label={`Remove source ${s.ref}`} disabled={remove.isPending} onClick={() => remove.mutate(s.id)}>
                ×
              </button>
              {b && <Follow b={b} label={`${s.kind}: ${s.ref}`} edits={edits} />}
            </li>
          );
        })}
```

Between `</ul>` and `<form`, add:

```tsx
      {!!list.data?.length && (
        <button className="button" disabled={edits.check.isPending} onClick={() => edits.check.mutate()}>
          Check for updates
        </button>
      )}
```

Append to `Organize.tsx`:

```tsx
/** Follow says how checking one source for updates stands, and pauses it, resumes it, or takes its updates without review. */
function Follow({ b, label, edits }: { b: Binding; label: string; edits: ReturnType<typeof useBindingEdits> }) {
  const busy = edits.edit.isPending;
  const set = (body: { paused?: boolean; autosync?: boolean }) => edits.edit.mutate({ binding: b.id, body });
  if (b.paused)
    return (
      <span className="muted">
        {" "}
        Not checking for updates{" "}
        <button className="button" disabled={busy} onClick={() => set({ paused: false })}>
          Check again
        </button>
      </span>
    );
  return (
    <span className="muted">
      {" "}
      {sourceLabel(b.remote_state, b.remote_id)}
      {b.check_error && <span className="error"> {b.check_error}</span>}{" "}
      <button className="button" disabled={busy} onClick={() => set({ paused: true })}>
        Ignore all
      </button>{" "}
      <label className="autosync">
        <input
          type="checkbox"
          checked={b.autosync}
          aria-label={`Autosync from ${label}`}
          disabled={busy}
          onChange={(e) => set({ autosync: e.target.checked })}
        />{" "}
        autosync
      </label>
    </span>
  );
}
```

- [ ] **Step 7: Put the banner on both pages**

In `web/src/card/Card.tsx`, add `import { UpdateBanner } from "./Upstream";`, and in `card-main`, before `<Stale kind="card" ed={ed} />`, add:

```tsx
        <UpdateBanner kind="card" id={id} />
```

In `web/src/lorebook/Lorebook.tsx`, add `import { UpdateBanner } from "../card/Upstream";`, and in `card-main`, before `<Stale kind="lorebook" ed={ed} />`, add:

```tsx
        <UpdateBanner kind="lorebook" id={id} />
```

- [ ] **Step 8: Run the tests**

Run: `cd web && npx vitest run src/card src/lorebook && npm run typecheck`
Expected: PASS, including the existing autosync, sync panel, and sources tests.

- [ ] **Step 9: Commit**

```bash
git add web/src/card web/src/lorebook
git commit -m "feat(web): show and settle site source updates on the card and lorebook pages"
```

---

### Task 7: Sources on the Sync page

**Files:**
- Modify: `web/src/sync/SyncPage.tsx`
- Test: `web/src/sync/SyncPage.test.tsx`

- [ ] **Step 1: Write the failing test**

In `web/src/sync/SyncPage.test.tsx`'s `beforeEach` mock, add `{ id: 4, name: "URL import", plugin: "url-import" }` to the `/api/instances` reply, `{ name: "url-import", capabilities: ["import", "sites", "pull"] }` to the `/api/plugins` reply, and `"4": { update: 2 }` to the `/api/sync/changes` counts.

Append:

```ts
test("site sources get their own line: their updates and a check of every source", async () => {
  renderSync();
  const updates = await screen.findByRole("link", { name: "2 updates on their sites" });
  expect(updates).toHaveAttribute("href", "/sync/changes?state=update");
  expect(screen.queryByRole("row", { name: /URL import/ })).not.toBeInTheDocument();
  await userEvent.click(screen.getByRole("button", { name: "Check for updates" }));
  await waitFor(() => expect(posts).toEqual([{ path: "/api/source-checks", body: undefined }]));
  expect(await screen.findByText("Checking every source for updates…")).toBeInTheDocument();
});
```

- [ ] **Step 2: Run it to verify it fails**

Run: `cd web && npx vitest run src/sync/SyncPage.test.tsx`
Expected: FAIL: no link named `2 updates on their sites`.

- [ ] **Step 3: Add the Sources line**

In `web/src/sync/SyncPage.tsx`, change the client import to `import { api, followers, must, withCapability } from "../api/client";`.

After the `targets` line, add:

```ts
  const follows = followers(instances.data, plugins.data);
  const updates = [...follows].reduce((n, i) => n + (changes.data?.counts[String(i)]?.update ?? 0), 0);
```

After the `sync` `useRun`, add:

```ts
  const checkSources = useRun(async () => must(await api.POST("/source-checks")), "Checking every source for updates…");
```

Just before the closing `</section>`, add:

```tsx
      {!!follows.size && (
        <section>
          <h2>Sources</h2>
          <div className="toolbar">
            <Link to="/sync/changes?state=update">
              {updates} {updates === 1 ? "update" : "updates"} on their sites
            </Link>
            <button className="button" disabled={checkSources.busy} onClick={checkSources.start}>
              Check for updates
            </button>
          </div>
        </section>
      )}
```

Change `useRun`'s doc comment to: "useRun starts a job and watches it until it finishes, then refreshes the page's counts."

- [ ] **Step 4: Run the tests**

Run: `cd web && npx vitest run src/sync/SyncPage.test.tsx`
Expected: PASS, including the existing SyncPage tests.

- [ ] **Step 5: Commit**

```bash
git add web/src/sync/SyncPage.tsx web/src/sync/SyncPage.test.tsx
git commit -m "feat(web): check every site source from the Sync page"
```

---

### Task 8: Verify

- [ ] **Step 1: Run what CI runs**

```bash
go vet ./...
INNKEEPER_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:55432/postgres go test -race -timeout 15m ./...
cd web && npm run typecheck && npm test
```

Expected: all pass, with the database tests run, not skipped.

- [ ] **Step 2: Read the diff**

Run: `git diff origin/main --stat` and read every changed file once more. Look for:
- dead code, such as an unused `useMutation` or `qc` left in `Panel.tsx`'s `Sync`;
- a hand edit to `web/openapi.json` or `web/src/api/schema.d.ts`;
- any place that still picks apps by `pull` alone: `grep -rn '"pull"' web/src --include='*.tsx' | grep -v test`.

---

## Self-review notes

- Spec coverage:
  - The queue's `update`, `missing`, and `error` entries come in Task 4.
  - Review, Ignore this change, Ignore all, and Remove source come in Tasks 4 to 6.
  - The banner on both pages, the Sources panel's pause, Check again, and autosync, and the per-entity check come in Task 6.
  - The library-wide check comes in Task 7.
  - Apps needing `push` come in Tasks 1 and 3.
- Taking data, avatar, or both uses the Diff's existing hunk and avatar choices. A "take everything from the site" shortcut isn't built; add it if reviewing long updates hunk by hunk proves slow.
