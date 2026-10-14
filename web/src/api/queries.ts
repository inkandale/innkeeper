import { queryOptions, useQuery } from "@tanstack/react-query";
import { api, must, withCapability } from "./client";

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
