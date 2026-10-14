import { useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { Check, Moon, Sun } from 'lucide-react';
import { useAssets } from '@/hooks';
import { useAssetStore } from '@/stores/useAssetStore';
import { useTimeframeStore } from '@/stores/useTimeframeStore';
import { useUiStore } from '@/stores/useUiStore';
import { useSettingsStore } from '@/stores/useSettingsStore';
import { TIMEFRAMES } from '@/lib/constants';
import { dataMode } from '@/lib/config';
import type { Timeframe } from '@/types/domain';
import { Badge } from '@/components/common/Badge';
import { Button } from '@/components/common/Button';
import { Field } from '@/components/common/Field';
import { Input, Select } from '@/components/common/Input';

function isValidHttpUrl(v: string): boolean {
  try {
    const u = new URL(v);
    return u.protocol === 'http:' || u.protocol === 'https:';
  } catch {
    return false;
  }
}

function isValidWsUrl(v: string): boolean {
  try {
    const u = new URL(v);
    return u.protocol === 'ws:' || u.protocol === 'wss:';
  } catch {
    return false;
  }
}

export default function Settings() {
  const qc = useQueryClient();
  const assets = useAssets();
  const { apiUrl, wsUrl, setApiUrl, setWsUrl } = useSettingsStore();
  const { assetId, setAssetId } = useAssetStore();
  const { timeframe, setTimeframe } = useTimeframeStore();
  const { theme, setTheme } = useUiStore();

  const [apiDraft, setApiDraft] = useState(apiUrl);
  const [wsDraft, setWsDraft] = useState(wsUrl);
  const [saved, setSaved] = useState(false);
  const [errors, setErrors] = useState<{ apiUrl?: string; wsUrl?: string }>({});

  const save = (e: React.FormEvent) => {
    e.preventDefault();
    const next: typeof errors = {};
    if (!isValidHttpUrl(apiDraft)) next.apiUrl = 'Must be a valid http(s) URL';
    if (!isValidWsUrl(wsDraft)) next.wsUrl = 'Must be a valid ws(s) URL';
    setErrors(next);
    if (next.apiUrl || next.wsUrl) return;
    setApiUrl(apiDraft.trim());
    setWsUrl(wsDraft.trim());
    // Base URL changed — refetch everything (esp. health).
    qc.invalidateQueries();
    setSaved(true);
    setTimeout(() => setSaved(false), 2000);
  };

  return (
    <div className="space-y-3">
      <h1 className="section-label">Settings</h1>

      <form
        onSubmit={save}
        className="max-w-2xl space-y-4 border border-line bg-surface-1 p-4"
        aria-label="Settings form"
      >
        <section aria-label="Endpoints" className="space-y-3">
          <h2 className="section-label">Endpoints</h2>
          <Field label="API URL" htmlFor="set-api" error={errors.apiUrl} hint="Base URL of the Go REST API">
            <Input
              id="set-api"
              type="url"
              className="tnum"
              value={apiDraft}
              onChange={(e) => setApiDraft(e.target.value)}
              placeholder="http://localhost:8080"
            />
          </Field>
          <Field
            label="WebSocket URL"
            htmlFor="set-ws"
            error={errors.wsUrl}
            hint="Realtime feed endpoint (disabled in Phase 1)"
          >
            <Input
              id="set-ws"
              type="url"
              className="tnum"
              value={wsDraft}
              onChange={(e) => setWsDraft(e.target.value)}
              placeholder="ws://localhost:8080/ws"
            />
          </Field>
        </section>

        <section aria-label="Defaults" className="space-y-3">
          <h2 className="section-label">Defaults</h2>
          <div className="grid grid-cols-2 gap-3">
            <Field label="Default asset" htmlFor="set-asset">
              <Select id="set-asset" value={assetId} onChange={(e) => setAssetId(e.target.value)}>
                {(assets.data ?? []).map((a) => (
                  <option key={a.id} value={a.id}>
                    {a.symbol} — {a.name}
                  </option>
                ))}
                {!assets.data?.length ? <option value={assetId}>{assetId.toUpperCase()}</option> : null}
              </Select>
            </Field>
            <Field label="Default timeframe" htmlFor="set-tf">
              <Select
                id="set-tf"
                value={timeframe}
                onChange={(e) => setTimeframe(e.target.value as Timeframe)}
              >
                {TIMEFRAMES.map((t) => (
                  <option key={t.value} value={t.value}>
                    {t.label}
                  </option>
                ))}
              </Select>
            </Field>
          </div>
        </section>

        <section aria-label="Appearance" className="space-y-2">
          <h2 className="section-label">Appearance</h2>
          <div className="flex gap-1" role="group" aria-label="Theme">
            <button
              type="button"
              aria-pressed={theme === 'dark'}
              onClick={() => setTheme('dark')}
              className={`flex items-center gap-1.5 rounded-sm border px-3 py-1.5 text-xs font-medium ${
                theme === 'dark'
                  ? 'border-accent/50 bg-accent-soft text-accent-fg'
                  : 'border-line bg-surface-2 text-content-2 hover:bg-surface-3'
              }`}
            >
              <Moon className="h-3.5 w-3.5" aria-hidden /> Dark
            </button>
            <button
              type="button"
              aria-pressed={theme === 'light'}
              onClick={() => setTheme('light')}
              className={`flex items-center gap-1.5 rounded-sm border px-3 py-1.5 text-xs font-medium ${
                theme === 'light'
                  ? 'border-accent/50 bg-accent-soft text-accent-fg'
                  : 'border-line bg-surface-2 text-content-2 hover:bg-surface-3'
              }`}
            >
              <Sun className="h-3.5 w-3.5" aria-hidden /> Light
            </button>
          </div>
        </section>

        <section aria-label="Data mode" className="space-y-2">
          <h2 className="section-label">Data mode</h2>
          <div className="flex items-center gap-2">
            <Badge tone={dataMode === 'mock' ? 'accent' : 'info'}>{dataMode}</Badge>
            <span className="text-2xs text-content-3">
              Set via <code className="tnum">VITE_DATA_MODE</code> at build time — read-only here.
            </span>
          </div>
        </section>

        <div className="flex items-center gap-2 border-t border-line pt-3">
          <Button type="submit">Save endpoints</Button>
          {saved ? (
            <span className="flex items-center gap-1 text-xs text-buy" role="status">
              <Check className="h-3.5 w-3.5" aria-hidden /> Saved
            </span>
          ) : null}
        </div>
      </form>
    </div>
  );
}
