import { useQuery } from "@tanstack/react-query";
import { usersApi, type ListParams } from "../lib/api";

export function useUsers(params: ListParams) {
  return useQuery({
    queryKey: ["users", params],
    queryFn: () => usersApi.list(params),
  });
}

export function useUser(id: string) {
  return useQuery({
    queryKey: ["user", id],
    queryFn: () => usersApi.get(id),
    enabled: !!id,
  });
}

export function useAffinityEvents(userId: string, limit?: number) {
  return useQuery({
    queryKey: ["affinity-events", userId, limit],
    queryFn: () => usersApi.affinityEvents(userId, limit),
    enabled: !!userId,
  });
}
