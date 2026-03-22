import { useQuery } from "@tanstack/react-query";
import { memoriesApi } from "../lib/api";
import type { Memory } from "../lib/api";

export interface DailyDiary extends Memory {
  date: string;
}

export interface HourlyDigest extends Memory {
  hour: string;
}

export interface DiaryDay {
  date: string;
  daily: DailyDiary | null;
  hourly: HourlyDigest[];
}

function getDiaryMeta(mem: Memory): { kind?: string; hour?: string; date?: string } {
  const md = mem.metadata ?? {};
  return {
    kind: md.kind as string | undefined,
    hour: md.hour as string | undefined,
    date: md.date as string | undefined,
  };
}

export function useDiary(targetDate: string) {
  return useQuery({
    queryKey: ["diary", targetDate],
    queryFn: async (): Promise<DiaryDay> => {
      // Fetch self memories, ordered by creation date descending.
      // We fetch a generous amount to cover all hourly digests + daily.
      const res = await memoriesApi.list({
        type: "self",
        limit: 100,
        order: "created_at",
        dir: "desc",
      });

      let daily: DailyDiary | null = null;
      const hourly: HourlyDigest[] = [];

      for (const mem of res.data) {
        const meta = getDiaryMeta(mem);

        if (meta.kind === "daily_diary" && meta.date === targetDate) {
          daily = { ...mem, date: meta.date };
        }

        if (meta.kind === "hourly_digest" && meta.hour?.startsWith(targetDate)) {
          hourly.push({ ...mem, hour: meta.hour });
        }
      }

      // Sort hourly chronologically.
      hourly.sort((a, b) => a.hour.localeCompare(b.hour));

      return { date: targetDate, daily, hourly };
    },
    refetchInterval: 30000,
  });
}

/** Get a list of dates that have diary entries (for the date picker). */
export function useDiaryDates() {
  return useQuery({
    queryKey: ["diary-dates"],
    queryFn: async (): Promise<string[]> => {
      const res = await memoriesApi.list({
        type: "self",
        limit: 500,
        order: "created_at",
        dir: "desc",
      });

      const dates = new Set<string>();
      for (const mem of res.data) {
        const meta = getDiaryMeta(mem);
        if (meta.kind === "daily_diary" && meta.date) {
          dates.add(meta.date);
        }
        if (meta.kind === "hourly_digest" && meta.hour) {
          dates.add(meta.hour.slice(0, 10));
        }
      }

      return Array.from(dates).sort().reverse();
    },
    refetchInterval: 60000,
  });
}
