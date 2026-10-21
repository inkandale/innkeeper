import { MutationCache, QueryClient } from "@tanstack/react-query";
import { ApiError } from "./client";

/** newQueryClient is the app's query client. Searches stay fresh for a while, so any change marks them out of date. */
export function newQueryClient(): QueryClient {
  const client: QueryClient = new QueryClient({
    // Retry network errors and 5xx, not 4xx.
    defaultOptions: { queries: { retry: (n, err) => n < 2 && (!(err instanceof ApiError) || err.status >= 500) } },
    mutationCache: new MutationCache({ onSuccess: () => client.invalidateQueries({ queryKey: ["search"] }) }),
  });
  return client;
}
