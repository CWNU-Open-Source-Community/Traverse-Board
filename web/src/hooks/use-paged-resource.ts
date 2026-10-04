import { useInfiniteQuery } from "@tanstack/react-query";
import type { APIClient, QueryValue } from "../api/client";

export function usePagedResource<T>(
  client: APIClient,
  queryKey: readonly unknown[],
  path: string,
  query: Record<string, QueryValue> = {},
  enabled = true,
) {
  return useInfiniteQuery({
    queryKey,
    queryFn: ({ pageParam, signal }) => client.getPage<T>(path, query, pageParam, signal),
    initialPageParam: "",
    getNextPageParam: (lastPage) => lastPage.page.next_cursor,
    enabled,
  });
}
