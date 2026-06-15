export function formatMoney(value: number | null | undefined, currency = "USDT") {
  const amount = Number.isFinite(value ?? NaN) ? value! : 0;
  return `${amount.toLocaleString("zh-TW", { minimumFractionDigits: 2, maximumFractionDigits: 2 })} ${currency}`;
}

export function formatAsset(value: number | null | undefined, symbol = "BTC") {
  const amount = Number.isFinite(value ?? NaN) ? value! : 0;
  return `${amount.toLocaleString("zh-TW", { minimumFractionDigits: 6, maximumFractionDigits: 6 })} ${symbol}`;
}

export function formatPercent(value: number | null | undefined) {
  const amount = Number.isFinite(value ?? NaN) ? value! : 0;
  return `${(amount * 100).toLocaleString("zh-TW", { minimumFractionDigits: 2, maximumFractionDigits: 2 })}%`;
}

export function relativeTime(dateLike?: string | null) {
  if (!dateLike) return "尚無紀錄";
  const date = new Date(dateLike);
  const diffMs = Date.now() - date.getTime();
  const minutes = Math.max(1, Math.round(diffMs / 60_000));
  if (minutes < 60) return `${minutes} 分鐘前`;
  const hours = Math.round(minutes / 60);
  if (hours < 24) return `${hours} 小時前`;
  const days = Math.round(hours / 24);
  if (days < 30) return `${days} 天前`;
  const months = Math.round(days / 30);
  return `${months} 個月前`;
}

export function shortDateTime(dateLike?: string | null) {
  if (!dateLike) return "尚無紀錄";
  return new Intl.DateTimeFormat("zh-TW", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit"
  }).format(new Date(dateLike));
}
