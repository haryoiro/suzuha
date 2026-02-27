import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { channelSettingsApi, guildsApi } from "../lib/api";

export function useGuildList() {
  return useQuery({
    queryKey: ["guilds"],
    queryFn: () => guildsApi.list(),
  });
}

export function useChannelSettings(guildId?: string) {
  return useQuery({
    queryKey: ["channel-settings", guildId],
    queryFn: () => channelSettingsApi.list(guildId),
  });
}

export function useUpsertChannelSetting() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({
      channelId,
      ...body
    }: {
      channelId: string;
      mode: string;
      use_identity: boolean;
      guild_id?: string;
    }) => channelSettingsApi.upsert(channelId, body),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["channel-settings"] }),
  });
}

export function useDeleteChannelSetting() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: channelSettingsApi.delete,
    onSuccess: () => qc.invalidateQueries({ queryKey: ["channel-settings"] }),
  });
}
