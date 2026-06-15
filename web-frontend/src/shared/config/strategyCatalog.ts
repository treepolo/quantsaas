export type StrategyCatalogItem = {
  id: string;
  nameKey: string;
  descriptionKey: string;
  exchange: string;
  symbols: string[];
  color: string;
  supportsOptimization: boolean;
};

export const strategyCatalog: StrategyCatalogItem[] = [
  {
    id: "sigmoid-dca-btc",
    nameKey: "templates.dynamicName",
    descriptionKey: "templates.dynamicDesc",
    exchange: "Binance",
    symbols: ["BTCUSDT"],
    color: "#2dd4bf",
    supportsOptimization: true
  }
];
