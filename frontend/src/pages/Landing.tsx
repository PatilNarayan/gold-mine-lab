import { useEffect } from 'react';
import { Link } from 'react-router-dom';
import {
  ArrowRight, KeyRound, Landmark, LockKeyhole, ShieldCheck, Vault, Wallet,
} from 'lucide-react';
import { useCandles, useMarketSummary } from '@/hooks';
import { useAssetStore } from '@/stores/useAssetStore';
import { useUiStore } from '@/stores/useUiStore';
import { formatPrice, formatSignedPercent } from '@/lib/formatters';
import { dataMode } from '@/lib/config';
import { Badge } from '@/components/common/Badge';
import { CandlestickChart } from '@/components/charts/CandlestickChart';

/**
 * Public marketing surface for the research terminal — a real preview, not
 * mock copy: the hero chart and ticker read the app's actual candle feed.
 * Rendered outside AppShell (no sidebar) at /landing.
 */
export default function Landing() {
  const theme = useUiStore((s) => s.theme);
  const assetId = useAssetStore((s) => s.assetId);
  const candles = useCandles(assetId, '1h');
  const summary = useMarketSummary(assetId, '1h');

  useEffect(() => {
    document.documentElement.dataset.theme = theme;
  }, [theme]);

  const up = (summary.data?.changePercent ?? 0) >= 0;

  return (
    <div className="min-h-full bg-surface-0 text-content-1">
      {/* nav */}
      <nav className="sticky top-0 z-50 border-b border-line bg-surface-0/80 backdrop-blur-md">
        <div className="mx-auto flex h-14 max-w-6xl items-center gap-6 px-4">
          <span className="text-sm font-semibold tracking-tight">
            Gold <span className="text-accent-fg">Mine</span> Lab
          </span>
          <div className="hidden gap-5 text-xs text-content-2 sm:flex">
            <a href="#security" className="hover:text-content-1">Security</a>
            <a href="#wallet" className="hover:text-content-1">Custody</a>
            <a href="#trust" className="hover:text-content-1">Trust</a>
          </div>
          <Link
            to="/dashboard"
            className="ml-auto inline-flex items-center gap-1.5 rounded-sm bg-accent px-3 py-1.5 text-xs font-semibold text-surface-0 hover:brightness-110"
          >
            Open terminal <ArrowRight className="h-3.5 w-3.5" aria-hidden />
          </Link>
        </div>
      </nav>

      {/* hero */}
      <header className="mx-auto grid max-w-6xl items-center gap-10 px-4 py-16 lg:grid-cols-2">
        <div>
          <Badge tone="accent">XAU/USD research terminal</Badge>
          <h1 className="mt-4 text-3xl font-bold leading-tight tracking-tight sm:text-4xl">
            Quantitative research on infrastructure that treats{' '}
            <span className="text-accent-fg">evidence as sacred</span>
          </h1>
          <p className="mt-3 max-w-md text-sm text-content-2">
            Backtest strategies on immutable datasets, hold out-of-sample
            windows sacred, and review simulated shadow execution — every
            number carries its provenance.
          </p>
          <div className="mt-6 flex gap-3">
            <Link
              to="/dashboard"
              className="inline-flex items-center gap-1.5 rounded-sm bg-accent px-4 py-2 text-sm font-semibold text-surface-0 hover:brightness-110"
            >
              Launch research terminal
            </Link>
            <a
              href="#security"
              className="inline-flex items-center rounded-sm border border-line-strong px-4 py-2 text-sm text-content-1 hover:bg-surface-2"
            >
              Security model
            </a>
          </div>
        </div>

        {/* live chart preview — real app feed */}
        <div className="border border-white/10 bg-white/5 p-3 backdrop-blur-md rounded-lg">
          <div className="flex items-baseline justify-between px-1 pb-2">
            <div>
              <div className="text-2xs uppercase tracking-widest text-content-3">
                {summary.data?.asset.symbol ?? 'XAU/USD'} · 1h preview
              </div>
              <div className="tnum text-xl font-semibold">
                {summary.data ? formatPrice(summary.data.lastClose) : '—'}
              </div>
            </div>
            {summary.data ? (
              <span className={`tnum text-xs font-semibold ${up ? 'text-buy' : 'text-sell'}`}>
                {formatSignedPercent(summary.data.changePercent)}
              </span>
            ) : null}
          </div>
          {candles.data?.candles.length ? (
            <CandlestickChart
              candles={candles.data.candles.slice(-120)}
              ema20={candles.data.overlays.ema20.slice(-120)}
              ema50={candles.data.overlays.ema50.slice(-120)}
              height={260}
            />
          ) : (
            <div className="h-[260px] animate-pulse rounded-sm bg-surface-2" aria-label="Loading chart" />
          )}
          <div className="mt-2 border-t border-line pt-2 text-2xs text-content-3">
            {dataMode === 'mock' ? 'Demo dataset' : 'Synced historical dataset'} — the same feed
            the backtester consumes.
          </div>
        </div>
      </header>

      {/* stats */}
      <div className="mx-auto grid max-w-6xl grid-cols-2 gap-3 px-4 pb-10 sm:grid-cols-4">
        {[
          ['Immutable', 'graph revisions & run records'],
          ['Holdout', 'windows reserved pre-analysis'],
          ['Zero', 'merged-portfolio illusions'],
          ['100%', 'cost assumptions declared'],
        ].map(([b, s]) => (
          <div key={b} className="border border-white/10 bg-white/5 px-4 py-3 backdrop-blur-md rounded-lg">
            <b className="tnum text-lg">{b}</b>
            <div className="text-2xs text-content-3">{s}</div>
          </div>
        ))}
      </div>

      {/* security */}
      <section id="security" className="mx-auto max-w-6xl px-4 py-12">
        <div className="mb-6">
          <div className="section-label">Security model</div>
          <h2 className="mt-1 text-2xl font-bold">Research integrity, enforced</h2>
          <p className="mt-1 max-w-xl text-sm text-content-2">
            The platform's protections are about statistical honesty — your
            results can't silently overfit, and simulated execution can never
            be mistaken for real trading.
          </p>
        </div>
        <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
          {[
            { icon: Vault, title: 'Reserved holdouts', body: 'Pre-registered studies freeze the spec and reserve an out-of-sample window before outcomes are computed.' },
            { icon: LockKeyhole, title: 'Immutable revisions', body: 'Every strategy-graph revision is content-hashed; every run records the exact revision it used.' },
            { icon: ShieldCheck, title: 'Simulation labeled', body: 'Shadow fills execute against real quotes but are marked SIMULATED everywhere — never presented as trades.' },
            { icon: Landmark, title: 'Declared costs', body: 'Commission, slippage, spread and position sizing are explicit inputs, visible on every result.' },
            { icon: KeyRound, title: 'Local secrets', body: 'API keys live in backend env vars — never in the frontend bundle or this repository.' },
            { icon: ArrowRight, title: 'Honest empty states', body: '"Not loaded", "no results" and "failed" are three different states — nothing fakes success.' },
          ].map(({ icon: Icon, title, body }) => (
            <div key={title} className="border border-white/10 bg-white/5 p-4 backdrop-blur-md rounded-lg">
              <div className="mb-3 inline-flex rounded-md border border-accent/30 bg-accent-soft p-2">
                <Icon className="h-4 w-4 text-accent-fg" aria-hidden />
              </div>
              <h3 className="text-sm font-semibold">{title}</h3>
              <p className="mt-1 text-xs text-content-2">{body}</p>
            </div>
          ))}
        </div>
      </section>

      {/* wallet / custody */}
      <section id="wallet" className="mx-auto grid max-w-6xl items-center gap-10 px-4 py-12 lg:grid-cols-2">
        <div>
          <div className="section-label">Execution posture</div>
          <h2 className="mt-1 text-2xl font-bold">Observe, simulate, verify — in that order</h2>
          <p className="mt-2 max-w-md text-sm text-content-2">
            Live market observation is strictly read-only. Shadow mode replays
            your strategy's decisions against real quotes and records every
            decision — evidence before any commitment.
          </p>
          <div className="mt-5 grid grid-cols-2 gap-2">
            {[
              ['Read-only live feed', 'Quotes never feed backtests'],
              ['Decision ledger', 'Every close evaluated, logged'],
              ['Simulated fills', 'At observed bid/ask, costs measured'],
              ['No broker keys', 'Nothing here can place real orders'],
            ].map(([b, s]) => (
              <div key={b} className="border border-line bg-surface-1 px-3 py-2.5">
                <b className="block text-xs">{b}</b>
                <span className="text-2xs text-content-3">{s}</span>
              </div>
            ))}
          </div>
        </div>
        <div className="border border-white/10 bg-white/5 p-4 backdrop-blur-md rounded-lg">
          <div className="section-label mb-3">Execution stages</div>
          {[
            { icon: Wallet, t: 'Observe', d: 'Provider quotes, mid-tagged, honest staleness', tone: 'info' as const },
            { icon: ShieldCheck, t: 'Simulate', d: 'Shadow engine replays decisions on real prices', tone: 'warning' as const },
            { icon: KeyRound, t: 'Execute', d: 'Not connected — by design, no real orders exist', tone: 'neutral' as const },
          ].map(({ icon: Icon, t, d, tone }) => (
            <div key={t} className="mb-2 flex items-center gap-3 rounded-sm border border-line bg-surface-1 px-3 py-2.5">
              <Icon className="h-4 w-4 shrink-0 text-content-3" aria-hidden />
              <div className="min-w-0">
                <div className="text-xs font-semibold">{t}</div>
                <div className="text-2xs text-content-3">{d}</div>
              </div>
              <Badge tone={tone}>{t === 'Execute' ? 'disabled' : 'live'}</Badge>
            </div>
          ))}
          <p className="mt-3 text-center text-2xs text-content-3">
            The terminal never holds exchange credentials — research stays research.
          </p>
        </div>
      </section>

      {/* trust */}
      <section id="trust" className="mx-auto max-w-6xl px-4 py-12">
        <div className="section-label mb-4">Trust indicators</div>
        <div className="flex flex-wrap gap-2">
          {[
            'Provenance on every run',
            'In-sample vs OOS separated',
            'Warm-up bars never counted',
            'Request IDs on errors',
            'No profitability claims',
            'Open methodology notes',
          ].map((t) => (
            <span
              key={t}
              className="inline-flex items-center gap-2 rounded-full border border-line bg-surface-1 px-3 py-1.5 text-xs font-medium text-content-2"
            >
              <ShieldCheck className="h-3.5 w-3.5 text-accent-fg" aria-hidden />
              {t}
            </span>
          ))}
        </div>
      </section>

      <footer className="border-t border-line py-6">
        <div className="mx-auto flex max-w-6xl flex-wrap justify-between gap-2 px-4 text-2xs text-content-3">
          <span>Gold Mine Lab — quantitative research terminal</span>
          <span>Historical results do not predict future performance. Nothing here places real orders.</span>
        </div>
      </footer>
    </div>
  );
}
