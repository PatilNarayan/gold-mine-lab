import type { Strategy } from '@/types/domain';

/**
 * Static strategy registry. `parameters` describes a renderable schema — the
 * backtest form is generated from it, never hard-coded per strategy.
 */
export const MOCK_STRATEGIES: Strategy[] = [
  {
    id: 'ema-cross',
    name: 'EMA Crossover',
    version: '1.2.0',
    description:
      'Trend-following system that enters long on EMA20/EMA50 bullish crosses and shorts bearish crosses, with an ATR-based stop.',
    supportedAssets: ['xauusd'],
    timeframe: '1h',
    status: 'active',
    parameters: [
      { name: 'fastLength', label: 'Fast EMA', type: 'number', default: 20, min: 2, max: 100, step: 1 },
      { name: 'slowLength', label: 'Slow EMA', type: 'number', default: 50, min: 10, max: 300, step: 1 },
      { name: 'stopLossPct', label: 'Stop loss %', type: 'number', default: 1.5, min: 0.1, max: 10, step: 0.1 },
      {
        name: 'direction',
        label: 'Direction',
        type: 'select',
        default: 'both',
        options: ['both', 'long-only', 'short-only'],
      },
    ],
  },
  {
    // Mirrors the real backend strategy (rsi-mean-reversion v1.0.0).
    id: 'rsi-mean-reversion',
    name: 'RSI Mean Reversion',
    version: '1.0.0',
    description:
      'Long-only mean reversion: buys when Wilder RSI crosses up through the oversold threshold, exits when it crosses down through overbought.',
    supportedAssets: ['xauusd'],
    timeframe: '1d',
    supportedTimeframes: ['15m', '1h', '4h', '1d'],
    status: 'active',
    parameters: [
      { name: 'rsi_period', label: 'RSI Period', type: 'number', default: 14, min: 2, max: 100, step: 1 },
      { name: 'oversold', label: 'Oversold', type: 'number', default: 30, min: 1, max: 49, step: 1 },
      { name: 'overbought', label: 'Overbought', type: 'number', default: 70, min: 51, max: 99, step: 1 },
    ],
  },
  {
    // Mirrors the real backend strategy (donchian-breakout v1.0.0).
    id: 'donchian-breakout',
    name: 'Donchian Breakout',
    version: '1.0.0',
    description:
      'Long-only breakout: buys when the close exceeds the highest high of the preceding N candles, exits when it breaks the lowest low of the preceding N.',
    supportedAssets: ['xauusd'],
    timeframe: '1d',
    supportedTimeframes: ['15m', '1h', '4h', '1d'],
    status: 'active',
    parameters: [
      { name: 'lookback_period', label: 'Lookback Period', type: 'number', default: 20, min: 5, max: 200, step: 1 },
    ],
  },
  {
    id: 'atr-breakout',
    name: 'ATR Breakout',
    version: '0.9.0',
    description:
      'Enters on range breakouts confirmed by ATR expansion; trails stops with a chandelier exit.',
    supportedAssets: ['xauusd'],
    timeframe: '4h',
    status: 'inactive',
    parameters: [
      { name: 'atrLength', label: 'ATR length', type: 'number', default: 14, min: 5, max: 50, step: 1 },
      { name: 'breakoutMult', label: 'Breakout mult', type: 'number', default: 2.0, min: 0.5, max: 6, step: 0.1 },
      { name: 'trailMult', label: 'Trail mult', type: 'number', default: 3.0, min: 1, max: 8, step: 0.1 },
    ],
  },
  {
    id: 'macd-trend',
    name: 'MACD Trend Rider',
    version: '1.0.0',
    description:
      'Daily trend system using MACD signal-line crosses filtered by a 200-day moving average.',
    supportedAssets: ['xauusd'],
    timeframe: '1d',
    status: 'active',
    parameters: [
      { name: 'fastLength', label: 'MACD fast', type: 'number', default: 12, min: 2, max: 50, step: 1 },
      { name: 'slowLength', label: 'MACD slow', type: 'number', default: 26, min: 10, max: 100, step: 1 },
      { name: 'signalLength', label: 'Signal', type: 'number', default: 9, min: 2, max: 30, step: 1 },
      { name: 'useMaFilter', label: '200D MA filter', type: 'boolean', default: true },
    ],
  },
  {
    id: 'bollinger-squeeze',
    name: 'Bollinger Squeeze',
    version: '1.1.0',
    description:
      'Trades volatility expansions after Bollinger Band squeezes, confirmed by momentum direction.',
    supportedAssets: ['xauusd'],
    timeframe: '1h',
    status: 'inactive',
    parameters: [
      { name: 'bbLength', label: 'BB length', type: 'number', default: 20, min: 5, max: 60, step: 1 },
      { name: 'bbMult', label: 'BB mult', type: 'number', default: 2.0, min: 1, max: 4, step: 0.1 },
      { name: 'squeezeLookback', label: 'Squeeze lookback', type: 'number', default: 120, min: 20, max: 500, step: 10 },
    ],
  },
];

export function strategyById(id: string): Strategy | undefined {
  return MOCK_STRATEGIES.find((s) => s.id === id);
}
