import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { locationApi } from "../lib/api";

export function useLocationDevices() {
  return useQuery({
    queryKey: ["location-devices"],
    queryFn: () => locationApi.listDevices(),
  });
}

export function useLocationPlaces() {
  return useQuery({
    queryKey: ["location-places"],
    queryFn: () => locationApi.listPlaces(),
  });
}

export function useUpsertDevice() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ deviceId, ownerName, userId }: { deviceId: string; ownerName: string; userId?: string }) =>
      locationApi.upsertDevice(deviceId, { owner_name: ownerName, user_id: userId }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["location-devices"] }),
  });
}

export function useDeleteDevice() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (deviceId: string) => locationApi.deleteDevice(deviceId),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["location-devices"] }),
  });
}

export function useCreatePlace() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: { name: string; latitude: number; longitude: number; radius_m: number }) =>
      locationApi.createPlace(body),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["location-places"] }),
  });
}

export function useUpdatePlace() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, ...body }: { id: string; name: string; latitude: number; longitude: number; radius_m: number }) =>
      locationApi.updatePlace(id, body),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["location-places"] }),
  });
}

export function useDeletePlace() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => locationApi.deletePlace(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["location-places"] }),
  });
}
