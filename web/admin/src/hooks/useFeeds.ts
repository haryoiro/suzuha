import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { feedsApi } from "../lib/api";

export function useFeeds() {
  return useQuery({
    queryKey: ["feeds"],
    queryFn: () => feedsApi.list(),
  });
}

export function useFeed(id: string) {
  return useQuery({
    queryKey: ["feeds", id],
    queryFn: () => feedsApi.get(id).then((r) => r.data),
    enabled: !!id,
  });
}

export function useCreateFeed() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: feedsApi.create,
    onSuccess: () => qc.invalidateQueries({ queryKey: ["feeds"] }),
  });
}

export function useUpdateFeed() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, ...body }: { id: string; name?: string; url?: string; channel_id?: string; enabled?: boolean }) =>
      feedsApi.update(id, body),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["feeds"] }),
  });
}

export function useDeleteFeed() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: feedsApi.delete,
    onSuccess: () => qc.invalidateQueries({ queryKey: ["feeds"] }),
  });
}

export function useFeedItems(feedId: string, params: { offset?: number; limit?: number }) {
  return useQuery({
    queryKey: ["feeds", feedId, "items", params],
    queryFn: () => feedsApi.items(feedId, params),
    enabled: !!feedId,
  });
}

export function useFeedStats() {
  return useQuery({
    queryKey: ["feeds", "stats"],
    queryFn: () => feedsApi.stats(),
  });
}
