import { Link } from "react-router-dom";
import { AreaChart, BrainCircuit, Database, Dices, FlaskConical, Gauge, Grid3X3, LineChart } from "lucide-react";
import { useI18n } from "../../i18n/useI18n";
import { Card, CardDescription, CardHeader, CardTitle } from "../../shared/ui/Card";

const items = [
  { to: "/evolution", icon: FlaskConical, title: "優化實驗室", body: "建立參數搜尋任務、觀察運算過程，管理候選與採用參數。" },
  { to: "/backtesting", icon: AreaChart, title: "回測", body: "用歷史資料檢查參數表現，確認結果是否穩定。" },
	{ to: "/control-analysis", icon: Dices, title: "隨機對照研究", body: "比較合法隨機參數、無意義規則與曝險順序打亂結果。" },
	{ to: "/kline-inverse", icon: LineChart, title: "K 線樣貌反推", body: "固定參數，搜尋可跑贏定投且具多樣性的合成 K 線樣貌。" },
  { to: "/robustness", icon: Grid3X3, title: "參數穩健性", body: "用一維、二維與多尺度區域檢查參數附近是否穩定。" },
  { to: "/dynamic-parameters", icon: BrainCircuit, title: "預測與動態參數", body: "建立樣本外預測、結構狀態與每日有效參數。" },
  { to: "/parameter-research", icon: Grid3X3, title: "參數地形研究", body: "完成分批探索、穩健區域、候選驗證與人工採用。" },
  { to: "/data", icon: Database, title: "研究資料", body: "匯入 BTC 與股指日線資料，檢查資料筆數與更新狀態。" },
  { to: "/status", icon: Gauge, title: "市場狀態", body: "套用採用參數，查看目前市場判斷與目標權重。" }
];

export function ResearchHomePage() {
  const { t } = useI18n();

  return (
    <section className="space-y-4">
      <div>
        <h1 className="text-2xl font-bold text-slate-100">{t("research.title")}</h1>
        <p className="mt-1 text-sm text-slate-400">{t("research.subtitle")}</p>
      </div>
      <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-6">
        {items.map((item) => {
          const Icon = item.icon;
          const content = (
            <Card className="h-full transition hover:border-white/10">
              <CardHeader>
                <div>
                  <CardTitle>{item.title}</CardTitle>
                  <CardDescription>{item.body}</CardDescription>
                </div>
                <div className="flex h-10 w-10 items-center justify-center rounded-lg border border-[#2dd4bf]/20 bg-[#2dd4bf]/10">
                  <Icon className="h-5 w-5 text-[#99f6e4]" />
                </div>
              </CardHeader>
            </Card>
          );
          return item.to ? (
            <Link key={item.title} to={item.to} className="block">
              {content}
            </Link>
          ) : (
            <div key={item.title} className="opacity-70">
              {content}
            </div>
          );
        })}
      </div>
    </section>
  );
}
