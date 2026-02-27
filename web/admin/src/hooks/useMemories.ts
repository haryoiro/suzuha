import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { memoriesApi, type ListParams } from "../lib/api";

export function useDuplicates(threshold?: number) {
  return useQuery({
    queryKey: ["memory-duplicates", threshold],
    queryFn: () => memoriesApi.duplicates(threshold),
  });
}

export function useMemories(params: ListParams) {
  return useQuery({
    queryKey: ["memories", params],
    queryFn: () => memoriesApi.list(params),
  });
}

export function useMemory(id: string) {
  return useQuery({
    queryKey: ["memories", id],
    queryFn: () => memoriesApi.get(id).then((r) => r.data),
    enabled: !!id,
  });
}

export function useCreateMemory() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: memoriesApi.create,
    onSuccess: () => qc.invalidateQueries({ queryKey: ["memories"] }),
  });
}

export function useUpdateMemory() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, ...body }: { id: string; type?: string; content?: string; metadata?: Record<string, unknown> }) =>
      memoriesApi.update(id, body),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["memories"] }),
  });
}

export function useDeleteMemory() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: memoriesApi.delete,
    onSuccess: () => qc.invalidateQueries({ queryKey: ["memories"] }),
  });
}
