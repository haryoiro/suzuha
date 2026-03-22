import { useQuery } from "@tanstack/react-query";
import { contextApi } from "../lib/api";
import type { ContextSource } from "../lib/api";

export function useAgentContext(source?: ContextSource) {
  return useQuery({
    queryKey: ["agent-context", source ?? "discord"],
    queryFn: () => contextApi.get(source),
    refetchInterval: 5000,
  });
}
