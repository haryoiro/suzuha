import dayjs from "dayjs";
import utc from "dayjs/plugin/utc";
import timezone from "dayjs/plugin/timezone";

dayjs.extend(utc);
dayjs.extend(timezone);

const TZ = "Asia/Tokyo";

/** Format ISO datetime string to JST display (YYYY-MM-DD HH:mm). */
export function formatJST(iso: string, fmt = "YYYY-MM-DD HH:mm"): string {
  return dayjs(iso).tz(TZ).format(fmt);
}

/** Parse ISO string to dayjs in JST. */
export function toJST(iso: string): dayjs.Dayjs {
  return dayjs(iso).tz(TZ);
}

/** Format ISO datetime as relative time string (e.g. "3時間前"). */
export function formatRelative(iso: string): string {
  const diff = Date.now() - new Date(iso).getTime();
  const mins = Math.floor(diff / 60000);
  if (mins < 1) return "たった今";
  if (mins < 60) return `${mins}分前`;
  const hours = Math.floor(mins / 60);
  if (hours < 24) return `${hours}時間前`;
  const days = Math.floor(hours / 24);
  return `${days}日前`;
}

export { dayjs, TZ };
