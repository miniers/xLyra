import {
  CartesianGrid,
  Line,
  LineChart,
  ResponsiveContainer,
  Tooltip,
  type TooltipContentProps,
  XAxis,
  YAxis,
} from 'recharts'
import { useTranslation } from 'react-i18next'
import { formatPercent } from '@/features/dashboard/lib/dashboard-utils'
import {
  dashboardChartColors,
  dashboardTooltipStyle,
} from './chart-style'
import type { DashboardSeries, DashboardTrendDatum } from './dashboard-types'

type CacheHitTrendChartProps = {
  data: DashboardTrendDatum[]
  series: DashboardSeries[]
  height?: number
}

export function CacheHitTrendChart({ data, series, height = 260 }: CacheHitTrendChartProps) {
  const { t } = useTranslation('dashboard')
  const seriesWithColor = series.map((item, index) => ({
    ...item,
    color: item.color ?? dashboardChartColors[index % dashboardChartColors.length],
  }))

  return (
    <div style={{ height }}>
      <ResponsiveContainer width="100%" height="100%">
        <LineChart accessibilityLayer={false} data={data} margin={{ top: 8, right: 8, bottom: 0, left: 0 }}>
          <CartesianGrid stroke="hsl(var(--glass-border))" vertical={false} />
          <XAxis
            dataKey="date"
            interval="preserveStartEnd"
            minTickGap={24}
            padding={{ left: 8, right: 36 }}
            tickMargin={8}
            tickLine={false}
            axisLine={false}
            tick={{ fill: 'hsl(var(--text-muted-soft))', fontSize: 12 }}
          />
          <YAxis
            domain={[0, 1]}
            tickFormatter={(value) => formatPercent(Number(value), 0)}
            tickLine={false}
            axisLine={false}
            width={46}
            tick={{ fill: 'hsl(var(--text-muted-soft))', fontSize: 12 }}
          />
          <Tooltip content={(props) => <CacheHitTrendTooltip {...props} series={seriesWithColor} metricLabel={t('cacheHit.hitRate')} />} />
          {seriesWithColor.map((item) => (
            <Line
              key={item.key}
              type="monotone"
              connectNulls
              dataKey={item.key}
              name={item.name}
              stroke={item.color}
              strokeWidth={2}
              dot={{ r: 3 }}
              activeDot={{ r: 4 }}
            />
          ))}
        </LineChart>
      </ResponsiveContainer>
    </div>
  )
}

type CacheHitTrendTooltipProps = TooltipContentProps & {
  series: Array<DashboardSeries & { color: string }>
  metricLabel: string
}

function CacheHitTrendTooltip({ active, label, payload, series, metricLabel }: CacheHitTrendTooltipProps) {
  if (!active || !payload?.length) return null

  const seriesByKey = new Map(series.map((item) => [item.key, item]))
  const items = payload
    .map((item) => {
      const key = String(item.dataKey ?? '')
      const seriesItem = seriesByKey.get(key)
      return {
        key,
        name: seriesItem?.name ?? String(item.name ?? key),
        color: seriesItem?.color ?? item.color ?? item.stroke ?? 'hsl(var(--text-muted-soft))',
        value: Number(item.value ?? 0),
      }
    })
    .filter((item) => Number.isFinite(item.value))
    .sort((left, right) => right.value - left.value)

  if (!items.length) return null

  return (
    <div style={dashboardTooltipStyle} className="min-w-[176px]">
      <div className="mb-2 text-xs text-muted-soft">{label}</div>
      <div className="space-y-1">
        {items.map((item) => (
          <div key={item.key} className="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-3 text-xs text-foreground">
            <span className="flex min-w-0 items-center gap-1.5">
              <span className="size-2 shrink-0 rounded-[2px]" style={{ backgroundColor: item.color }} />
              <span className="truncate">{item.name}</span>
            </span>
            <span className="tabular-nums">{metricLabel} {formatPercent(item.value)}</span>
          </div>
        ))}
      </div>
    </div>
  )
}
