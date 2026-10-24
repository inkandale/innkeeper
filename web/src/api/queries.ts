import { queryOptions, useQuery } from "@tanstack/react-query";
import { api, followers, must, route, withCapability, type Kind } from "./client";
import type { components } from "./schema";

export const instancesQuery = queryOptions({ queryKey: ["instances"], queryFn: async () => must(await api.GET("/instances")) });

export const pluginsQuery = queryOptions({ queryKey: ["plugins"], queryFn: async () => must(await api.GET("/plugins")) });

export const librariesQuery = queryOptions({ queryKey: ["libraries"], queryFn: async () => must(await api.GET("/libraries")) });

export const collectionsQuery = (library: number) =>
  queryOptions({
    queryKey: ["collections", library],
    queryFn: async () => must(await api.GET("/collections", { params: { query: { library } } })),
  });

export const meQuery = queryOptions({ queryKey: ["me"], queryFn: async () => must(await api.GET("/auth/me")) });

/** enrichmentKeysQuery is the scorers, by priority, and each enrich instance's keys; the grid, filters, and sort use them. */
export const enrichmentKeysQuery = queryOptions({
  queryKey: ["enrichment-keys"],
  queryFn: async () => must(await api.GET("/enrichments/keys")),
});

export const librarySettingsQuery = (id: number) =>
  queryOptions({
    queryKey: ["library-settings", id],
    queryFn: async () => must(await api.GET("/libraries/{id}/settings", { params: { path: { id } } })),
  });

export const settingsQuery = queryOptions({ queryKey: ["settings"], queryFn: async () => must(await api.GET("/settings")) });

/** useInstancesWith returns the instances whose plugin offers any of caps, empty until both lists load. */
export function useInstancesWith(...caps: string[]) {
  const instances = useQuery(instancesQuery);
  const plugins = useQuery(pluginsQuery);
  return withCapability(instances.data, plugins.data, ...caps);
}

/** useFollowers returns the IDs of the instances that follow site sources, undefined while either list loads. */
export function useFollowers() {
  const instances = useQuery(instancesQuery);
  const plugins = useQuery(pluginsQuery);
  if (instances.isPending || plugins.isPending) return undefined;
  return followers(instances.data, plugins.data);
}

/** bindingsQuery is a card's or lorebook's bindings: the apps it is pushed to and the site sources it follows. */
export const bindingsQuery = (kind: Kind, id: number) =>
  queryOptions({
    queryKey: [kind, id, "bindings"],
    queryFn: async () => must(await api.GET(route(kind, "/cards/{id}/bindings"), { params: { path: { id } } })),
  });

/** searchQuery is a card or lorebook search. It stays fresh for 30 seconds, and a search nothing shows any more is cancelled. */
export const searchQuery = (kind: Kind, body: components["schemas"]["Query"]) =>
  queryOptions({
    queryKey: ["search", kind, body],
    queryFn: async ({ signal }) => must(await api.POST(route(kind, "/cards/search"), { body, signal })),
    staleTime: 30_000,
  });
