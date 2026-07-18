import { useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Archive, Check, Pencil, Save, Search, X } from "lucide-react";
import { Link } from "react-router-dom";
import { evolutionApi, type GenomeRecord } from "../../shared/services/evolution";
import { Button } from "../../shared/ui/Button";
import { Card, CardDescription, CardHeader, CardTitle } from "../../shared/ui/Card";

const roleLabels: Record<GenomeRecord["role"], string> = {
  champion: "已採用",
  challenger: "待評估",
  candidate: "候選",
  retired: "已退役",
  archived: "已封存"
};

function parseTags(value: string) {
  return Array.from(new Set(value.split(",").map((item) => item.trim()).filter(Boolean))).slice(0, 24);
}

function formatDate(value?: string | null) {
  if (!value) return "—";
  return new Intl.DateTimeFormat("zh-TW", { dateStyle: "medium", timeStyle: "short" }).format(new Date(value));
}

function formatPercent(value: number) {
  return Number.isFinite(value) ? `${(value * 100).toFixed(2)}%` : "—";
}

function Scope({ genome }: { genome: GenomeRecord }) {
  return (
    <div className="grid grid-cols-2 gap-2 text-xs text-slate-400 sm:grid-cols-4">
      <span>標的 <strong className="text-slate-200">{genome.instrument_id || "—"}</strong></span>
      <span>來源 <strong className="text-slate-200">{genome.data_source || "—"}</strong></span>
      <span>週期 <strong className="text-slate-200">{genome.interval || "—"}</strong></span>
      <span>執行 <strong className="text-slate-200">{genome.execution_mode || "—"}</strong></span>
    </div>
  );
}

function GenomeCard({ genome }: { genome: GenomeRecord }) {
  const queryClient = useQueryClient();
  const [editing, setEditing] = useState(false);
  const [name, setName] = useState(genome.name || "");
  const [notes, setNotes] = useState(genome.notes || "");
  const [tags, setTags] = useState((genome.tags || []).join(", "));
  const [confirmPromote, setConfirmPromote] = useState(false);
  const refresh = () => queryClient.invalidateQueries({ queryKey: ["genomes"] });
  const update = useMutation({
    mutationFn: () => evolutionApi.updateGenome(genome.id, { name, notes, tags: parseTags(tags) }),
    onSuccess: () => { setEditing(false); refresh(); }
  });
  const promote = useMutation({
    mutationFn: () => evolutionApi.promote(genome.id),
    onSuccess: () => { setConfirmPromote(false); refresh(); }
  });
  const archive = useMutation({ mutationFn: () => evolutionApi.deleteGenome(genome.id), onSuccess: refresh });
  const canPromote = genome.role !== "champion";

  return (
    <Card className={genome.role === "champion" ? "border-teal-400/30" : ""}>
      <CardHeader>
        <div>
          <CardTitle>{genome.name?.trim() || `參數 #${genome.id}`}</CardTitle>
          <CardDescription>#{genome.id} · {roleLabels[genome.role]} · {formatDate(genome.created_at)}</CardDescription>
        </div>
        <span className="rounded-md border border-white/10 px-2 py-1 text-xs text-slate-300">{roleLabels[genome.role]}</span>
      </CardHeader>
      <Scope genome={genome} />
      <div className="mt-3 grid grid-cols-2 gap-2 rounded-lg border border-white/5 bg-slate-950/30 p-3 text-sm">
        <span className="text-slate-400">歷史評分 <strong className="ml-1 text-slate-200">{genome.score_total.toFixed(4)}</strong></span>
        <span className="text-slate-400">最大回撤 <strong className="ml-1 text-slate-200">{formatPercent(genome.max_drawdown)}</strong></span>
      </div>
      {editing ? (
        <div className="mt-3 space-y-3 rounded-lg border border-white/10 p-3">
          <label className="block text-xs text-slate-400">名稱<input className="mt-1 h-10 w-full rounded-lg border border-slate-700 bg-slate-950 px-3 text-sm text-slate-100" value={name} onChange={(event) => setName(event.target.value)} /></label>
          <label className="block text-xs text-slate-400">標籤<input className="mt-1 h-10 w-full rounded-lg border border-slate-700 bg-slate-950 px-3 text-sm text-slate-100" value={tags} onChange={(event) => setTags(event.target.value)} placeholder="用逗號分隔" /></label>
          <label className="block text-xs text-slate-400">備註<textarea className="mt-1 min-h-24 w-full rounded-lg border border-slate-700 bg-slate-950 p-3 text-sm text-slate-100" value={notes} onChange={(event) => setNotes(event.target.value)} /></label>
          <div className="flex gap-2"><Button size="sm" icon={Save} loading={update.isPending} onClick={() => update.mutate()}>儲存</Button><Button size="sm" icon={X} variant="secondary" onClick={() => setEditing(false)}>取消</Button></div>
        </div>
      ) : (
        <>
          <div className="mt-3 flex flex-wrap gap-1">{(genome.tags || []).map((tag) => <span key={tag} className="rounded bg-teal-400/10 px-2 py-1 text-xs text-teal-300">{tag}</span>)}</div>
          {genome.notes ? <p className="mt-3 text-sm text-slate-300">{genome.notes}</p> : null}
        </>
      )}
      <div className="mt-4 flex flex-wrap gap-2">
        {canPromote ? confirmPromote ? <Button size="sm" icon={Check} loading={promote.isPending} onClick={() => promote.mutate()}>確認設為已採用</Button> : <Button size="sm" onClick={() => setConfirmPromote(true)}>設為已採用</Button> : null}
        <Button size="sm" icon={Pencil} variant="secondary" onClick={() => setEditing(true)}>編輯</Button>
        <Link to={`/backtesting?genome=${genome.id}`} className="inline-flex min-h-8 items-center rounded-lg border border-white/10 bg-white/[0.04] px-3 py-1 text-xs font-semibold text-slate-200">回測</Link>
        <Button size="sm" icon={Archive} variant="danger" loading={archive.isPending} onClick={() => archive.mutate()}>封存／刪除</Button>
      </div>
      {(update.error || promote.error || archive.error) ? <p className="mt-3 text-sm text-rose-300">{String((update.error || promote.error || archive.error) as Error)}</p> : null}
    </Card>
  );
}

export function EvolutionPage() {
  const [filter, setFilter] = useState("");
  const genomes = useQuery({ queryKey: ["genomes"], queryFn: evolutionApi.listGenomes });
  const tasks = useQuery({ queryKey: ["evolution-tasks-history"], queryFn: evolutionApi.listTasks });
  const visible = useMemo(() => {
    const needle = filter.trim().toLowerCase();
    if (!needle) return genomes.data || [];
    return (genomes.data || []).filter((item) => [item.id, item.name, item.instrument_id, item.interval, roleLabels[item.role], ...(item.tags || [])].some((value) => String(value || "").toLowerCase().includes(needle)));
  }, [filter, genomes.data]);

  return (
    <section className="space-y-5">
      <div><h1 className="text-2xl font-bold text-slate-100">參數庫</h1><p className="mt-1 text-sm text-slate-400">管理候選、已採用與已退役參數；新的參數探索統一由參數研究工作區進行。</p></div>
      <Card>
        <label className="relative block max-w-xl"><Search className="absolute left-3 top-3 h-4 w-4 text-slate-500" /><input className="h-10 w-full rounded-lg border border-slate-700 bg-slate-950 pl-10 pr-3 text-sm text-slate-100" value={filter} onChange={(event) => setFilter(event.target.value)} placeholder="搜尋名稱、標的、週期、角色或標籤" /></label>
      </Card>
      {genomes.isLoading ? <Card className="text-sm text-slate-400">載入參數庫…</Card> : null}
      {!genomes.isLoading && visible.length === 0 ? <Card className="text-sm text-slate-400">沒有符合條件的參數。</Card> : null}
      <div className="grid gap-4 xl:grid-cols-2">{visible.map((genome) => <GenomeCard key={genome.id} genome={genome} />)}</div>
      <Card>
        <CardHeader><div><CardTitle>歷史搜尋任務摘要</CardTitle><CardDescription>舊搜尋已停止新建；這裡只保留既有任務的狀態與追溯資訊，不會輪詢或重新啟動。</CardDescription></div></CardHeader>
        <div className="overflow-x-auto"><table className="w-full min-w-[680px] text-left text-xs"><thead className="text-slate-500"><tr><th className="p-2">任務</th><th>標的</th><th>週期</th><th>狀態</th><th>進度</th><th>建立時間</th></tr></thead><tbody>{(tasks.data?.tasks || []).map((task) => <tr key={task.id} className="border-t border-white/5"><td className="p-2">#{task.id}</td><td>{task.instrument_id || task.pair || "—"}</td><td>{task.interval || "—"}</td><td>{task.status}</td><td>{Math.round((task.progress || 0) * 100)}%</td><td>{formatDate(task.created_at)}</td></tr>)}</tbody></table></div>
        {!tasks.isLoading && !(tasks.data?.tasks.length) ? <p className="mt-3 text-sm text-slate-500">沒有歷史任務。</p> : null}
      </Card>
    </section>
  );
}
