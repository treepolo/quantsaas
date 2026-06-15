import { apiFetch } from "./client";

export type InstanceStatus = "running" | "stopped" | "error" | "halted" | "RUNNING" | "STOPPED" | "ERROR";

export type StrategyInstance = {
  id: number;
  name: string;
  template_id: string;
  symbol: string;
  exchange: string;
  status: InstanceStatus;
  created_at: string;
  updated_at?: string;
  last_tick_at?: string | null;
  total_assets?: number;
};

type RawInstance = Partial<StrategyInstance> & {
  ID?: number;
  Name?: string;
  TemplateID?: string;
  Symbol?: string;
  Exchange?: string;
  Status?: InstanceStatus;
  CreatedAt?: string;
  UpdatedAt?: string;
  LastTickAt?: string | null;
};

export type CreateInstanceInput = {
  name: string;
  template_id: string;
  symbol: string;
  exchange: string;
  initial_usdt: number;
  monthly_inject_usdt?: number;
  cold_sealed_btc?: number;
  risk_limit?: number;
};

function normalize(row: RawInstance): StrategyInstance {
  return {
    id: Number(row.id ?? row.ID ?? 0),
    name: String(row.name ?? row.Name ?? "未命名實例"),
    template_id: String(row.template_id ?? row.TemplateID ?? "sigmoid-dca-btc"),
    symbol: String(row.symbol ?? row.Symbol ?? "BTCUSDT"),
    exchange: String(row.exchange ?? row.Exchange ?? "Binance"),
    status: (row.status ?? row.Status ?? "STOPPED") as InstanceStatus,
    created_at: String(row.created_at ?? row.CreatedAt ?? new Date().toISOString()),
    updated_at: row.updated_at ?? row.UpdatedAt,
    last_tick_at: row.last_tick_at ?? row.LastTickAt ?? null,
    total_assets: row.total_assets
  };
}

export const instancesApi = {
  async list() {
    const rows = await apiFetch<RawInstance[]>("/instances");
    return rows.map(normalize);
  },
  async create(input: CreateInstanceInput) {
    const row = await apiFetch<RawInstance>("/instances", {
      method: "POST",
      body: JSON.stringify(input)
    });
    return normalize(row);
  },
  start(id: number) {
    return apiFetch<{ status: string }>(`/instances/${id}/start`, { method: "POST" });
  },
  stop(id: number) {
    return apiFetch<{ status: string }>(`/instances/${id}/stop`, { method: "POST" });
  },
  patchStatus(id: number, status: "running" | "stopped") {
    return apiFetch<{ status: string }>(`/instances/${id}`, {
      method: "PATCH",
      body: JSON.stringify({ status })
    });
  },
  remove(id: number) {
    return apiFetch<{ status: string }>(`/instances/${id}`, { method: "DELETE" });
  }
};
