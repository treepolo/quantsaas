import type { BacktestResult } from "../services/backtests";
import type { EquitySnapshot, PortfolioSummary } from "../services/dashboard";
import type { EvolutionTask, GenomeRecord } from "../services/evolution";
import type { StrategyInstance } from "../services/instances";

const now = Date.now();

export const mockInstances: StrategyInstance[] = [
  {
    id: 1,
    name: "BTC 核心配置",
    template_id: "sigmoid-dca-btc",
    symbol: "BTCUSDT",
    exchange: "Binance",
    status: "RUNNING",
    created_at: new Date(now - 8 * 24 * 3600_000).toISOString(),
    last_tick_at: new Date(now - 12 * 60_000).toISOString(),
    total_assets: 128450.42
  },
  {
    id: 2,
    name: "長期累積帳戶",
    template_id: "sigmoid-dca-btc",
    symbol: "BTCUSDT",
    exchange: "Binance",
    status: "STOPPED",
    created_at: new Date(now - 32 * 24 * 3600_000).toISOString(),
    last_tick_at: new Date(now - 2 * 24 * 3600_000).toISOString(),
    total_assets: 74218.18
  }
];

export function mockPortfolio(instanceId: number): PortfolioSummary {
  const base = instanceId === 2 ? 74218.18 : 128450.42;
  return {
    total_assets: base,
    long_term: base * 0.42,
    active_position: base * 0.18,
    available_funds: base * 0.34,
    sealed_assets: base * 0.06,
    first_run_at: new Date(now - 42 * 24 * 3600_000).toISOString(),
    last_decision_at: new Date(now - 16 * 60_000).toISOString(),
    decisions_count: instanceId === 2 ? 82 : 214,
    monthly_trades: instanceId === 2 ? 9 : 31
  };
}

export function mockEquity(days = 30): EquitySnapshot[] {
  return Array.from({ length: days }, (_, index) => {
    const drift = index * 520;
    const wave = Math.sin(index / 2.2) * 1800;
    const value = 112000 + drift + wave;
    return {
      time: new Date(now - (days - index - 1) * 24 * 3600_000).toISOString(),
      total_assets: Math.round(value * 100) / 100,
      benchmark: Math.round((108000 + index * 390 + Math.sin(index / 3) * 900) * 100) / 100
    };
  });
}

export const mockTasks: EvolutionTask[] = [
  {
    id: 1024,
    status: "running",
    progress: 0.48,
    current_generation: 12,
    max_generations: 25,
    best_score: 0.736,
    max_drawdown: 0.183,
    created_at: new Date(now - 38 * 60_000).toISOString(),
    started_at: new Date(now - 36 * 60_000).toISOString()
  },
  {
    id: 1019,
    status: "completed",
    progress: 1,
    current_generation: 25,
    max_generations: 25,
    best_score: 0.692,
    max_drawdown: 0.211,
    created_at: new Date(now - 2 * 24 * 3600_000).toISOString(),
    finished_at: new Date(now - 2 * 24 * 3600_000 + 42 * 60_000).toISOString()
  }
];

export const mockGenomes: GenomeRecord[] = [
  {
    id: 301,
    role: "champion",
    created_at: new Date(now - 3 * 24 * 3600_000).toISOString(),
    score_total: 0.711,
    max_drawdown: 0.196,
    window_score: { "6m": 0.74, "2y": 0.69, "5y": 0.72, "10y": 0.71 }
  },
  {
    id: 302,
    role: "candidate",
    created_at: new Date(now - 1 * 24 * 3600_000).toISOString(),
    score_total: 0.736,
    max_drawdown: 0.183,
    window_score: { "6m": 0.76, "2y": 0.72, "5y": 0.73, "10y": 0.75 }
  },
  {
    id: 299,
    role: "archived",
    created_at: new Date(now - 11 * 24 * 3600_000).toISOString(),
    score_total: 0.654,
    max_drawdown: 0.238,
    window_score: { "6m": 0.61, "2y": 0.67, "5y": 0.66, "10y": 0.65 }
  }
];

export const mockBacktest: BacktestResult = {
  id: 5001,
  status: "completed",
  total_return: 0.382,
  alpha: 0.117,
  max_drawdown: 0.204,
  final_equity: 146200,
  benchmark: 132400,
  nav: mockEquity(45),
  windows: { "6m": 0.72, "2y": 0.68, "5y": 0.71, "10y": 0.7 }
};
