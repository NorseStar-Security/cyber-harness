import { expect, test } from '@playwright/test'
import { createRegistry, fromJson, toBinary } from '@bufbuild/protobuf'
import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { EventSchema } from '../cyber-ui/packages/aop/src/gen/aop/event_pb'
import { GetLibraryResponseSchema, RuntimeEventSchema } from '../src/gen/types/jev_pb'
import { jevEvent } from '../src/lib/jev-view'

// The Go live acceptance runs real inference and browser operations first.
// Replay its original protocol events and library; no generated fixture can
// satisfy the business oracle and warm execution checks below.
const root = process.env.JEV_ACCEPTANCE_REPORT_DIR
for (const kind of (process.env.JEV_ACCEPTANCE_CASES || 'shadow,repeat,expense').split(',')) {
  test(`real ${kind} Reflex executes and renders its retained evidence`, async ({ page }, info) => {
    test.skip(!root, 'Run the real browser acceptance and set JEV_ACCEPTANCE_REPORT_DIR')
    const report = JSON.parse(readFileSync(join(root!, kind, 'report.json'), 'utf8'))
    expect(report.real_llm && report.real_jev && report.real_browser).toBe(true)
    expect(report.seeded).toBe(false)
    expect(report.rows).toHaveLength((report.warm_pairs + 1) * 2)
    for (const row of report.rows) expect(row.correct).toBe(true)
    const warm = report.rows.find((row: any) => row.mode === 'auto' && row.warm)
    expect(warm).toBeDefined()
    expect(warm.full_takeover).toBe(true)
    expect(warm.main_tool_calls).toEqual([])
    expect(warm.source_after).toBe(warm.source_before)
    expect(warm.oracle.wrong).toBe(0)

    const directory = join(root!, kind, 'auto')
    const registry = createRegistry(RuntimeEventSchema)
    const recorded = JSON.parse(readFileSync(join(directory, `events-${warm.index}.json`), 'utf8'))
    const events = recorded.map((delivery: any) => fromJson(EventSchema, delivery.event, { registry }))
    const runtime = events.map(jevEvent).filter(Boolean)
    const dispatches = runtime.filter((event: any) => event.payload.case === 'dispatch' && !event.background)
    expect(dispatches).toHaveLength(warm.jev_dispatches)
    expect(runtime.filter((event: any) => event.payload.case === 'takeover')).toHaveLength(warm.jev_takeovers)
    expect(dispatches.filter((event: any) => !event.payload.value.read)).toHaveLength(warm.jev_effect_dispatches)
    expect(runtime.some((event: any) => event.payload.case === 'handoff' && event.payload.value.reason === 'report')).toBe(true)

    const libraryJSON = JSON.parse(readFileSync(join(directory, `library-${warm.index}.json`), 'utf8'))
    const library = fromJson(GetLibraryResponseSchema, libraryJSON)
    const reflex = library.reflexes.find(value => !value.archived)
    expect(reflex?.qualification?.replayed).toBeGreaterThan(0n)
    expect(reflex?.qualification?.checks).toContain('recorded_replay')

    const errors: string[] = []
    page.on('pageerror', error => errors.push(error.message))
    await page.addInitScript(() => localStorage.setItem('cyber-locale', 'en'))
    await page.goto('/e2e/fixtures/jev.html')
    await page.evaluate(({ binary, library }) => {
      ;(window as any).renderJEVEvents(binary)
      ;(window as any).renderJEVLibrary(library)
    }, { binary: events.map((event: any) => [...toBinary(EventSchema, event)]), library: libraryJSON })
    await expect(page.getByTestId('agent-workflow').first()).toBeVisible()
    await expect(page.locator('.control-reflex-invocation').first()).toBeVisible()
    await page.screenshot({ path: info.outputPath(`${kind}-real-execution.png`), fullPage: true })
    await page.getByRole('button', { name: 'Reflex', exact: true }).click()
    await page.getByRole('tab', { name: 'Reflex library', exact: true }).click()
    const panel = page.getByRole('dialog')
    await panel.getByRole('button').filter({ hasText: reflex!.id }).click()
    await expect(panel.getByTestId('jev-mechanism-proof')).toBeVisible()
    await expect(panel.getByTestId('jev-reflex-definition')).toContainText(reflex!.when)
    await expect(panel.getByTestId('jev-reflex-definition')).toContainText('recorded_replay')
    await page.screenshot({ path: info.outputPath(`${kind}-real-library.png`) })
    await page.setViewportSize({ width: 390, height: 844 })
    expect(await panel.evaluate(element => element.scrollWidth - element.clientWidth)).toBeLessThanOrEqual(1)
    const list = panel.locator('.tool-panel-list')
    const lastEntry = list.getByRole('button').last()
    await lastEntry.scrollIntoViewIfNeeded()
    await expect(lastEntry).toBeInViewport()
    expect(await panel.getByTestId('jev-reflex-definition').evaluate(element => {
      const { x, y, width } = element.getBoundingClientRect()
      return element.contains(document.elementFromPoint(x + width / 2, y + 12))
    })).toBe(true)
    await page.screenshot({ path: info.outputPath(`${kind}-real-library-mobile.png`) })
    expect(errors).toEqual([])
  })
}
