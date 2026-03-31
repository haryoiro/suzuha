import { useQuery } from "@tanstack/react-query";
import { diaryApi } from "../lib/api";
import type { DiaryEntry } from "../lib/api";

export interface DailyDiary {
  id: string;
  date: string;
  content: string;
}

export interface HourlyDigest {
  id: string;
  hour: string;
  content: string;
}

export interface DiaryDay {
  date: string;
  daily: DailyDiary | null;
  hourly: HourlyDigest[];
}

export function useDiary(targetDate: string) {
  return useQuery({
    queryKey: ["diary", targetDate],
    queryFn: async (): Promise<DiaryDay> => {
      const res = await diaryApi.list({ limit: 200 });

      let daily: DailyDiary | null = null;
      const hourly: HourlyDigest[] = [];

      for (const entry of res.data) {
        const periodDate = entry.period_start.slice(0, 10);

        if (entry.kind === "daily" && periodDate === targetDate) {
          daily = { id: entry.id, date: periodDate, content: entry.content };
        }

        if (entry.kind === "hourly" && periodDate === targetDate) {
          hourly.push({
            id: entry.id,
            hour: entry.period_start.slice(0, 16).replace(" ", "T"),
            content: entry.content,
          });
        }
      }

      // 時系列順にソート。
      hourly.sort((a, b) => a.hour.localeCompare(b.hour));

      return { date: targetDate, daily, hourly };
    },
    refetchInterval: 30000,
  });
}

/** 日記エントリがある日付の一覧を取得（DatePicker 用）。 */
export function useDiaryDates() {
  return useQuery({
    queryKey: ["diary-dates"],
    queryFn: async (): Promise<string[]> => {
      const res = await diaryApi.list({ limit: 500 });

      const dates = new Set<string>();
      for (const entry of res.data) {
        const periodDate = entry.period_start.slice(0, 10);
        dates.add(periodDate);
      }

      return Array.from(dates).sort().reverse();
    },
    refetchInterval: 60000,
  });
}
