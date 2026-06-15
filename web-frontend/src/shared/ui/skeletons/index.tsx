import { cn } from "../../lib/cn";

function PulseBlock({ className }: { className?: string }) {
  return <div className={cn("animate-pulse rounded-lg bg-slate-800/40", className)} />;
}

export function CardSkeleton() {
  return (
    <div className="qs-card space-y-4 p-4">
      <PulseBlock className="h-5 w-1/2" />
      <PulseBlock className="h-16 w-full" />
      <PulseBlock className="h-4 w-2/3" />
    </div>
  );
}

export function PnLChartSkeleton() {
  return (
    <div className="space-y-4">
      <PulseBlock className="h-5 w-40" />
      <PulseBlock className="h-72 w-full" />
    </div>
  );
}

export function TableSkeleton({ rows = 5 }: { rows?: number }) {
  return (
    <div className="space-y-3">
      {Array.from({ length: rows }, (_, index) => (
        <PulseBlock key={index} className="h-12 w-full" />
      ))}
    </div>
  );
}
