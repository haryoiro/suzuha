import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { promptsApi } from "../lib/api";

export function usePrompts() {
  return useQuery({
    queryKey: ["prompts"],
    queryFn: () => promptsApi.list(),
  });
}

export function useUpdatePrompt() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ name, content }: { name: string; content: string }) =>
      promptsApi.update(name, content),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["prompts"] }),
  });
}
