import { queryOptions, useQuery } from "@tanstack/react-query";
import { api, followers, must, route, withCapability, type Kind } from "./client";

export const instancesQuery = queryOptions({ queryKey: ["instances"], queryFn: async () => must(await api.GET("/instances")) });

export const pluginsQuery = queryOptions({ queryKey: ["plugins"], queryFn: async () => must(await api.GET("/plugins")) });

export const collectionsQuery = queryOptions({ queryKey: ["collections"], queryFn: async () => must(await api.GET("/collections")) });

export const meQuery = queryOptions({ queryKey: ["me"], queryFn: async () => must(await api.GET("/auth/me")) });

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
