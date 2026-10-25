import { useTranslation } from 'react-i18next'
import { Database } from 'lucide-react'
import { Tooltip, TooltipContent, TooltipTrigger } from '@cyber/ui'
import { formatTokenCount, normalizeTokenUsage } from '../../../cyber-ui/packages/viewer/src/lib/token-usage'

export function TokenUsageBadge({ usage }: { usage?: unknown }) {
  const { t } = useTranslation('chat')
  const value = normalizeTokenUsage(usage)
  if (!value || value.totalTokens <= 0) return null
  const breakdown = value.inputTokens > 0 || value.outputTokens > 0
  const read = value.cacheReadInputTokens ?? value.cachedInputTokens
  const hit = read !== undefined && value.inputTokens > 0 ? Math.min(100, read / value.inputTokens * 100) : undefined
  const details = [
    ...(breakdown ? [t('tokenInput', { count: value.inputTokens }), t('tokenOutput', { count: value.outputTokens })] : []),
    t('tokenTotal', { count: value.totalTokens }),
    ...(read !== undefined ? [t('tokenCacheRead', { count: read })] : []),
    ...(value.cacheCreationInputTokens !== undefined ? [t('tokenCacheWrite', { count: value.cacheCreationInputTokens })] : []),
    ...(hit !== undefined ? [t('tokenCacheHit', { percent: hit.toFixed(0) })] : []),
  ]
  return <Tooltip><TooltipTrigger asChild>
    <span tabIndex={0} data-testid="token-usage" aria-label={details.join(' · ')}
      className="inline-flex max-w-full flex-wrap items-center gap-x-2 gap-y-0.5 rounded-sm text-[10px] font-normal tabular-nums text-muted-foreground outline-none focus-visible:ring-1 focus-visible:ring-ring">
      {breakdown ? <><span>↓{formatTokenCount(value.inputTokens)}</span><span>↑{formatTokenCount(value.outputTokens)}</span></>
        : <span>{formatTokenCount(value.totalTokens)} tok</span>}
      {hit !== undefined && hit > 0 && <span className="inline-flex items-center gap-1"><Database className="h-3 w-3 shrink-0" aria-hidden="true" /><span>{hit.toFixed(0)}%</span></span>}
    </span>
  </TooltipTrigger><TooltipContent className="text-xs">{details.map(detail => <p key={detail}>{detail}</p>)}</TooltipContent></Tooltip>
}
