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

export { dayjs, TZ };
