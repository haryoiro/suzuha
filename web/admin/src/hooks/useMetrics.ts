import { useQuery } from "@tanstack/react-query";
import { metricsApi } from "../lib/api";

export function useMetrics() {
  return useQuery({
    queryKey: ["metrics"],
    queryFn: metricsApi.json,
    refetchInterval: 5000,
  });
}
