import { ArrowRight, CircuitBoard, Code2, Eye, Repeat2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { CodeBlock } from '@/markdown'
import type { ClaimDefinition, ReflexDefinition } from '../../gen/types/jev_pb'
import { ChoiceBranches } from './JEVDecision'
import { toJson, toJsonString, type JsonObject } from '@bufbuild/protobuf'
import { ReflexDefinitionSchema, ReflexQualificationSchema } from '../../gen/types/jev_pb'

export function JEVDefinition({ value, compact = false, inline = false }: { value: ClaimDefinition | ReflexDefinition; compact?: boolean; inline?: boolean }) {
  const { t } = useTranslation('jev')
  const reflex = 'observe' in value ? value : undefined
  const proof = reflex?.qualification
  const manifest = reflex && toJson(ReflexDefinitionSchema, reflex) as JsonObject | undefined
  return <div className="min-w-0 space-y-3" data-testid={reflex ? 'jev-reflex-definition' : 'jev-claim-definition'}>
    <div className="flex items-center gap-2 text-xs"><span className="rounded border border-emerald-500/30 px-1.5 py-0.5 text-emerald-600 dark:text-emerald-400">{reflex ? 'Reflex' : 'Claim'}{!value.id && ` · ${t('draft')}`}</span>
      {value.id && <code className="min-w-0 truncate text-[10px] text-muted-foreground" title={value.id}>{value.id}</code>}</div>
    <p className={`${compact ? 'text-[11px]' : 'text-xs'} whitespace-pre-wrap break-words text-muted-foreground`}>{'context' in value ? value.context : value.when}</p>
    {reflex ? <>
      {(proof || reflex.archived) && <p className="text-xs text-muted-foreground">{t(reflex.archived ? 'archived' : 'qualified')}</p>}
      {reflex.archiveReason && <p role="status" className="whitespace-pre-wrap break-words text-xs text-amber-600" data-testid="jev-archive-reason">{reflex.archiveReason}</p>}
      {proof?.checks && <div className="space-y-1 text-xs text-muted-foreground" data-testid="jev-mechanism-proof"><p>{t('mechanismChecks', { count: proof.checks.length, replayed: Number(proof.replayed) })}</p><p>{t('mechanismScope')}</p>
        {!!proof.coverageGaps?.length && <details><summary>{t('coverageGaps', { count: proof.coverageGaps.length })}</summary><ul className="mt-1 list-disc pl-4">{proof.coverageGaps.map((gap, i) => <li key={i}>{gap}</li>)}</ul></details>}</div>}
      {proof && <CodeBlock code={toJsonString(ReflexQualificationSchema, proof, { prettySpaces: 2 })} language="json" maxHeight={160} />}
      {manifest && (manifest.steps || manifest.parametersSchema !== undefined) && <CodeBlock code={JSON.stringify({ steps: manifest.steps, parametersSchema: manifest.parametersSchema }, null, 2)} language="json" maxHeight={160} />}
      {!inline && <div className="jev-reflex-loop" data-testid="jev-reflex-loop">
        <span><Eye />{t('observation')}</span><ArrowRight /><span><CircuitBoard />JEV</span><ArrowRight />
        <span><Code2 />{t('programExecution')}</span><ArrowRight /><span><Repeat2 />{t('feedback')}</span>
      </div>}
      {!!reflex.claimIds.length && <div className="flex flex-wrap items-center gap-1.5 text-[10px] text-muted-foreground"><span>Claim</span>{reflex.claimIds.map(id => <code key={id} className="rounded bg-muted px-1.5 py-1">{id}</code>)}</div>}
      {inline ? <div className="space-y-2"><p className="whitespace-pre-wrap text-xs">{reflex.decide}</p>
        <CodeBlock code={reflex.observe} language="javascript" maxHeight={300} />
        {Object.entries(reflex.readers).map(([id, source]) => <div key={id}><p className="text-[11px]">{id}</p><CodeBlock code={source} language="javascript" maxHeight={200} /></div>)}
      </div> : <details open={compact ? undefined : true} className="jev-definition-details">
        <summary className="cursor-pointer text-[11px] text-muted-foreground"><span>{t('policy')}</span> · <span>{t('observationBinding')}</span></summary>
        <p className="mt-2 whitespace-pre-wrap break-words text-xs">{reflex.decide}</p>
        <CodeBlock code={reflex.observe} language="javascript" maxHeight={300} />
        {Object.entries(reflex.readers).map(([id, source]) => <div key={id}><p className="mt-2 text-[11px] text-muted-foreground">{id}</p><CodeBlock code={source} language="javascript" maxHeight={300} /></div>)}
      </details>}
    </> : 'context' in value && <>
      {!!value.options.length && <ChoiceBranches definition question={{ $typeName: 'decision.Claim', type: value.type, context: value.context, options: value.options }} />}
      <p className="text-[10px] text-muted-foreground">{t('compilationEvidence')}</p>
    </>}
  </div>
}
