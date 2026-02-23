import { useQuery } from "@tanstack/react-query";
import { contextApi } from "../lib/api";

export function useAgentContext() {
  return useQuery({
    queryKey: ["agent-context"],
    queryFn: contextApi.get,
    refetchInterval: 5000,
  });
}
