import { useEffect, useRef, useState, type PointerEvent as ReactPointerEvent } from "react";
import { RotateCcw } from "lucide-react";
import { cn } from "../lib/cn";
import { Button } from "./Button";

export type ChartRange = { start: number; end: number };

type DragMode = "start" | "end" | "window";

type DragState = {
  mode: DragMode;
  startX: number;
  startRange: ChartRange;
  width: number;
};

export function ChartRangeSlider({
  range,
  total,
  startLabel,
  endLabel,
  onChange,
  onReset,
  className
}: {
  range: ChartRange | null;
  total: number;
  startLabel: string;
  endLabel: string;
  onChange: (range: ChartRange) => void;
  onReset?: () => void;
  className?: string;
}) {
  const trackRef = useRef<HTMLDivElement | null>(null);
  const dragRef = useRef<DragState | null>(null);
  const [dragging, setDragging] = useState<DragMode | null>(null);

  useEffect(() => {
    function onPointerMove(event: PointerEvent) {
      const drag = dragRef.current;
      if (!drag || !range || total <= 1) return;
      event.preventDefault();
      const indexDelta = Math.round(((event.clientX - drag.startX) / Math.max(1, drag.width)) * (total - 1));
      const size = drag.startRange.end - drag.startRange.start + 1;
      if (drag.mode === "window") {
        onChange(clampRangeBySize(drag.startRange.start + indexDelta, size, total));
        return;
      }
      if (drag.mode === "start") {
        onChange({ start: clampIndex(drag.startRange.start + indexDelta, 0, drag.startRange.end), end: drag.startRange.end });
        return;
      }
      onChange({ start: drag.startRange.start, end: clampIndex(drag.startRange.end + indexDelta, drag.startRange.start, total - 1) });
    }

    function onPointerUp() {
      dragRef.current = null;
      setDragging(null);
    }

    window.addEventListener("pointermove", onPointerMove);
    window.addEventListener("pointerup", onPointerUp);
    return () => {
      window.removeEventListener("pointermove", onPointerMove);
      window.removeEventListener("pointerup", onPointerUp);
    };
  }, [onChange, range, total]);

  if (!range || total <= 1) return null;

  const leftPct = (range.start / (total - 1)) * 100;
  const rightPct = (range.end / (total - 1)) * 100;

  function beginDrag(mode: DragMode, event: ReactPointerEvent<HTMLButtonElement | HTMLDivElement>) {
    if (!range || !trackRef.current) return;
    event.preventDefault();
    event.stopPropagation();
    dragRef.current = {
      mode,
      startX: event.clientX,
      startRange: range,
      width: Math.max(1, trackRef.current.clientWidth)
    };
    setDragging(mode);
  }

  return (
    <div className={cn("mt-4 rounded-lg border border-white/[0.04] bg-white/[0.02] p-3", className)}>
      <div className="mb-3 flex items-center justify-between gap-3 text-xs text-slate-500">
        <span>{startLabel}</span>
        {onReset ? (
          <Button icon={RotateCcw} variant="secondary" onClick={onReset}>
            重設
          </Button>
        ) : null}
        <span>{endLabel}</span>
      </div>
      <div ref={trackRef} className="relative h-9 select-none px-1">
        <div className="absolute left-1 right-1 top-1/2 h-2 -translate-y-1/2 rounded-full bg-slate-800" />
        <div
          className="absolute top-1/2 h-5 -translate-y-1/2 rounded-full border border-[#2dd4bf]/50 bg-[#2dd4bf]/20 shadow-[0_0_16px_rgba(45,212,191,0.16)]"
          style={{ left: `${leftPct}%`, width: `${Math.max(0.8, rightPct - leftPct)}%` }}
        >
          <button
            type="button"
            aria-label="調整顯示起點"
            className={cn("absolute -left-2 top-1/2 h-7 w-4 -translate-y-1/2 cursor-ew-resize rounded bg-[#2dd4bf]", dragging === "start" && "bg-[#99f6e4]")}
            onPointerDown={(event) => beginDrag("start", event)}
          />
          <div
            aria-label="移動顯示區間"
            className={cn("absolute inset-y-0 left-3 right-3 cursor-grab rounded-full", dragging === "window" && "cursor-grabbing")}
            onPointerDown={(event) => beginDrag("window", event)}
          />
          <button
            type="button"
            aria-label="調整顯示終點"
            className={cn("absolute -right-2 top-1/2 h-7 w-4 -translate-y-1/2 cursor-ew-resize rounded bg-[#2dd4bf]", dragging === "end" && "bg-[#99f6e4]")}
            onPointerDown={(event) => beginDrag("end", event)}
          />
        </div>
      </div>
    </div>
  );
}

function clampIndex(value: number, min: number, max: number) {
  return Math.max(min, Math.min(max, value));
}

function clampRangeBySize(start: number, size: number, length: number): ChartRange {
  const clampedSize = Math.max(1, Math.min(length, size));
  const nextStart = Math.max(0, Math.min(start, length - clampedSize));
  return { start: nextStart, end: nextStart + clampedSize - 1 };
}
