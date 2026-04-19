import { useQuery } from "@tanstack/react-query";
import { diaryApi } from "../lib/api";
import { toJST } from "../lib/date";

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
        const jst = toJST(entry.period_start);
        const periodDate = jst.format("YYYY-MM-DD");

        if (entry.kind === "daily" && periodDate === targetDate) {
          daily = { id: entry.id, date: periodDate, content: entry.content };
        }

        if (entry.kind === "hourly" && periodDate === targetDate) {
          hourly.push({
            id: entry.id,
            hour: jst.format("HH:mm"),
            content: entry.content,
          });
        }
      }

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
        const periodDate = toJST(entry.period_start).format("YYYY-MM-DD");
        dates.add(periodDate);
      }

      return Array.from(dates).sort().reverse();
    },
    refetchInterval: 60000,
  });
}
