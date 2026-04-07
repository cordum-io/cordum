import { useQuery } from "@tanstack/react-query";
import { fetchTopics } from "../api/client";
import type { TopicsResponse } from "../api/types";

export function useTopics() {
  return useQuery<TopicsResponse>({
    queryKey: ["topics"],
    queryFn: fetchTopics,
    staleTime: 15_000,
    refetchInterval: 15_000,
  });
}
