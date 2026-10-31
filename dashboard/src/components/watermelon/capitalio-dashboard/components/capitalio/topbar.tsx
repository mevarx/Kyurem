import { Search } from 'lucide-react'
import { useLocation } from 'react-router-dom'
import { SidebarTrigger } from '@/components/ui/sidebar'
import { ThemeToggle } from '@/components/ThemeToggle'

const TITLES: Record<string, { section: string; title: string }> = {
  '/': { section: 'Operate', title: 'Overview' },
  '/tasks': { section: 'Operate', title: 'Tasks' },
  '/graph': { section: 'Operate', title: 'Agent graph' },
  '/costs': { section: 'Manage', title: 'Costs' },
  '/agents': { section: 'Manage', title: 'Agents' },
  '/policy': { section: 'Manage', title: 'Policy & audit' },
  '/settings': { section: 'Manage', title: 'Settings' },
}

function titleFor(pathname: string): { section: string; title: string } {
  const direct = TITLES[pathname]
  if (direct) return direct
  if (pathname.startsWith('/tasks/')) return { section: 'Operate', title: 'Task detail' }
  return { section: 'Operate', title: 'Kyurem' }
}

export function DashboardTopbar({ onSearch }: { onSearch: () => void }) {
  const { pathname } = useLocation()
  const { section, title } = titleFor(pathname)

  return (
    <header className="flex shrink-0 flex-col gap-3 px-4 py-3 md:flex-row md:items-center md:justify-between md:px-8">
      <div className="flex min-w-0 items-center gap-3">
        <SidebarTrigger className="size-8.5 shrink-0 md:hidden [&_svg]:size-5!" />

        <div className="flex min-w-0 flex-1 items-center gap-3">
          <nav className="flex min-w-0 items-center gap-2 text-sm" aria-label="Breadcrumb">
            <span className="hidden shrink-0 text-muted-foreground sm:inline">{section}</span>
            <span className="hidden shrink-0 text-muted-foreground sm:inline" aria-hidden="true">
              /
            </span>
            <span className="truncate text-lg leading-none font-semibold text-foreground md:text-xl">
              {title}
            </span>
          </nav>
        </div>
      </div>

      <div className="flex min-w-0 items-center gap-2 md:shrink-0">
        <button
          type="button"
          onClick={onSearch}
          aria-label="Open command palette"
          className="flex h-9 min-w-0 flex-1 items-center gap-2 rounded-md border border-border bg-card px-2.5 text-sm text-muted-foreground transition-colors hover:text-foreground md:w-52 md:flex-none"
        >
          <Search className="size-4 shrink-0" />
          <span className="hidden truncate md:inline">Search…</span>
          <kbd className="ml-auto hidden rounded border border-border bg-muted px-1.5 font-mono text-[11px] md:inline">
            ⌘K
          </kbd>
        </button>

        <span
          className="flex shrink-0 items-center gap-2 rounded-full border border-border bg-card px-3 py-1.5 font-mono text-xs text-muted-foreground"
          title="Dashboard polls the gateway every 5 seconds"
        >
          <span
            className="size-2 shrink-0 animate-pulse rounded-full"
            style={{ backgroundColor: 'var(--chart-4)', boxShadow: '0 0 8px var(--chart-4)' }}
            aria-hidden="true"
          />
          <span className="hidden sm:inline">Live · 5s</span>
        </span>

        <ThemeToggle />
      </div>
    </header>
  )
}
