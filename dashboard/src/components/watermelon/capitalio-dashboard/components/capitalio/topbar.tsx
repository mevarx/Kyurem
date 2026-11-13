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
    <header className="flex h-14 w-full shrink-0 items-center gap-2 border-b border-border bg-background px-4 md:h-16 md:gap-3 md:px-8">
      <SidebarTrigger className="size-9 shrink-0 md:hidden [&_svg]:size-5!" />

      <nav className="flex min-w-0 flex-1 items-center gap-2 text-sm" aria-label="Breadcrumb">
        <span className="hidden shrink-0 text-muted-foreground sm:inline">{section}</span>
        <span className="hidden shrink-0 text-muted-foreground sm:inline" aria-hidden="true">
          /
        </span>
        <span className="truncate text-[15px] font-semibold tracking-tight text-foreground">
          {title}
        </span>
      </nav>

      <div className="flex shrink-0 items-center gap-2">
        <button
          type="button"
          onClick={onSearch}
          className="flex h-9 w-9 items-center justify-center rounded-md border border-border bg-card px-0 text-sm text-muted-foreground transition-colors hover:text-foreground md:w-52 md:justify-start md:px-2.5"
        >
          <Search className="size-4 shrink-0" aria-hidden="true" />
          <span className="hidden truncate md:inline">Search</span>
          <kbd className="ml-auto hidden rounded border border-border bg-muted px-1.5 font-mono text-[11px] md:inline">
            ⌘K
          </kbd>
          <span className="sr-only">Open command palette</span>
        </button>

        <span
          className="flex shrink-0 items-center gap-2 rounded-full border border-border bg-card px-3 py-1.5 font-mono text-xs text-muted-foreground"
          title="Dashboard polls the gateway every 5 seconds"
        >
          <span className="size-2 shrink-0 rounded-full bg-success" aria-hidden="true" />
          <span className="hidden sm:inline">Live · 5s</span>
        </span>

        <ThemeToggle />
      </div>
    </header>
  )
}
