import { useQuery } from "@tanstack/react-query";
import { toolsApi } from "../lib/api";

export function useTools() {
  return useQuery({
    queryKey: ["tools"],
    queryFn: () => toolsApi.list(),
  });
}
