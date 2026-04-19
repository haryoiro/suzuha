import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { schedulerApi } from "../lib/api";

export function useSchedulerJobs() {
  return useQuery({
    queryKey: ["scheduler-jobs"],
    queryFn: schedulerApi.jobs,
    refetchInterval: 30_000,
  });
}

export function useTriggerJob() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (task: string) => schedulerApi.trigger(task),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["scheduler-jobs"] }),
  });
}
