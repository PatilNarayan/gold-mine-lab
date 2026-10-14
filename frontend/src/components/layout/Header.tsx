import { Menu } from 'lucide-react';
import { useAssets, useHealth } from '@/hooks';
import { useAssetStore } from '@/stores/useAssetStore';
import { useTimeframeStore } from '@/stores/useTimeframeStore';
import { useUiStore } from '@/stores/useUiStore';
import { TIMEFRAMES } from '@/lib/constants';
import { dataMode } from '@/lib/config';
import type { Timeframe } from '@/types/domain';
import { Badge } from '@/components/common/Badge';
import { StatusDot } from '@/components/common/StatusDot';
import { Select } from '@/components/common/Input';

/** Header: product name, asset/timeframe selectors, connectivity status. */
export function Header() {
  const setMobileNavOpen = useUiStore((s) => s.setMobileNavOpen);
  const assetId = useAssetStore((s) => s.assetId);
  const setAssetId = useAssetStore((s) => s.setAssetId);
  const timeframe = useTimeframeStore((s) => s.timeframe);
  const setTimeframe = useTimeframeStore((s) => s.setTimeframe);
  const assets = useAssets();
  const health = useHealth();

  const status =
    dataMode === 'mock' ? (
      <Badge tone="accent">Mock mode</Badge>
    ) : health.isLoading ? (
      <StatusDot status="muted" label="API connecting…" />
    ) : health.isError ? (
      <StatusDot status="error" label="API unreachable" />
    ) : (
      <StatusDot status="ok" label="API connected" />
    );

  return (
    <header className="flex h-12 shrink-0 items-center gap-3 border-b border-line bg-surface-1 px-3">
      <button
        type="button"
        aria-label="Open navigation"
        onClick={() => setMobileNavOpen(true)}
        className="rounded-sm p-1.5 text-content-2 hover:bg-surface-2 md:hidden"
      >
        <Menu className="h-4 w-4" aria-hidden />
      </button>

      <span className="text-sm font-semibold tracking-tight text-content-1">
        Gold <span className="text-accent-fg">Mine</span> Lab
      </span>

      <div className="ml-auto flex items-center gap-2">
        <label className="sr-only" htmlFor="hdr-asset">
          Asset
        </label>
        <Select
          id="hdr-asset"
          aria-label="Selected asset"
          value={assetId}
          onChange={(e) => setAssetId(e.target.value)}
          className="!w-auto !py-1 tnum text-xs"
        >
          {(assets.data ?? []).map((a) => (
            <option key={a.id} value={a.id}>
              {a.symbol}
            </option>
          ))}
          {!assets.data?.length ? <option value={assetId}>{assetId.toUpperCase()}</option> : null}
        </Select>

        <label className="sr-only" htmlFor="hdr-tf">
          Timeframe
        </label>
        <Select
          id="hdr-tf"
          aria-label="Selected timeframe"
          value={timeframe}
          onChange={(e) => setTimeframe(e.target.value as Timeframe)}
          className="!w-auto !py-1 tnum text-xs"
        >
          {TIMEFRAMES.map((t) => (
            <option key={t.value} value={t.value}>
              {t.label}
            </option>
          ))}
        </Select>

        <div className="hidden items-center gap-3 border-l border-line pl-3 sm:flex">
          {status}
          <StatusDot status="muted" label="Realtime: disabled" />
        </div>
      </div>
    </header>
  );
}
