import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { preferencesApi } from "../lib/api";

export function usePreferences(stance?: string) {
  return useQuery({
    queryKey: ["preferences", stance],
    queryFn: () => preferencesApi.list(stance),
  });
}

export function usePreferenceStats() {
  return useQuery({
    queryKey: ["preferences", "stats"],
    queryFn: () => preferencesApi.stats(),
  });
}

export function useUpdatePreference() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({
      id,
      ...body
    }: {
      id: number;
      stance?: string;
      confidence?: number;
      reasoning?: string;
    }) => preferencesApi.update(id, body),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["preferences"] }),
  });
}

export function useDeletePreference() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: preferencesApi.delete,
    onSuccess: () => qc.invalidateQueries({ queryKey: ["preferences"] }),
  });
}
