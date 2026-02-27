import { useQuery } from "@tanstack/react-query";
import { usersApi, guildsApi, actionsApi, type ListParams } from "../lib/api";

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

export function useUserGuilds(userId: string) {
  return useQuery({
    queryKey: ["user-guilds", userId],
    queryFn: () => usersApi.guilds(userId),
    enabled: !!userId,
  });
}

export function useUserMemories(userId: string, limit?: number) {
  return useQuery({
    queryKey: ["user-memories", userId, limit],
    queryFn: () => usersApi.memories(userId, limit),
    enabled: !!userId,
  });
}

export function useGuilds() {
  return useQuery({
    queryKey: ["guilds"],
    queryFn: () => guildsApi.list(),
  });
}

export function useGuildChannels(guildId: string) {
  return useQuery({
    queryKey: ["guild-channels", guildId],
    queryFn: () => guildsApi.channels(guildId),
    enabled: !!guildId,
  });
}

export function useScheduledActions(status?: string) {
  return useQuery({
    queryKey: ["scheduled-actions", status],
    queryFn: () => actionsApi.list(status),
  });
}

export function useAllChannels() {
  return useQuery({
    queryKey: ["all-channels"],
    queryFn: () => guildsApi.allChannels(),
  });
}
