import { Collapsible } from '@base-ui/react/collapsible'
import {
  Activity,
  Bot,
  ChevronDown,
  Coins,
  List,
  Network,
  Settings as SettingsIcon,
  ShieldCheck,
} from 'lucide-react'
import { NavLink, useLocation } from 'react-router-dom'
import { useSettings } from '@/api/hooks'
import { formatUptime } from '@/lib/format'
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarGroup,
  SidebarGroupContent,
  SidebarGroupLabel,
  SidebarHeader,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  useSidebar,
} from '@/components/ui/sidebar'
import { cn } from '@/lib/utils'

type NavItem = {
  name: string
  href: string
  icon: React.ComponentType<{ className?: string }>
}

type NavSection = {
  label: string
  collapsible?: boolean
  defaultOpen?: boolean
  items: NavItem[]
}

const operateSection: NavSection = {
  label: 'Operate',
  items: [
    { name: 'Overview', href: '/', icon: Activity },
    { name: 'Tasks', href: '/tasks', icon: List },
    { name: 'Agent graph', href: '/graph', icon: Network },
  ],
}

const manageSection: NavSection = {
  label: 'Manage',
  collapsible: true,
  defaultOpen: true,
  items: [
    { name: 'Costs', href: '/costs', icon: Coins },
    { name: 'Agents', href: '/agents', icon: Bot },
    { name: 'Policy & audit', href: '/policy', icon: ShieldCheck },
    { name: 'Settings', href: '/settings', icon: SettingsIcon },
  ],
}

function Brand() {
  return (
    <NavLink to="/" className="flex min-w-0 flex-1 items-center gap-3">
      <div className="flex size-10 shrink-0 items-center justify-center rounded-lg border border-primary/40 bg-primary text-lg font-bold text-primary-foreground">
        K
      </div>
      <span className="flex min-w-0 flex-col leading-tight">
        <span className="truncate text-lg font-medium text-foreground">Kyurem</span>
        <span className="font-mono text-[11px] text-subtle">a2a gateway</span>
      </span>
    </NavLink>
  )
}

function isItemActive(pathname: string, href: string): boolean {
  if (href === '/') return pathname === '/'
  return pathname === href || pathname.startsWith(`${href}/`)
}

function NavLinkButton({ item }: { item: NavItem }) {
  const { pathname } = useLocation()
  const { isMobile, setOpenMobile } = useSidebar()
  const isActive = isItemActive(pathname, item.href)
  const Icon = item.icon

  function handleClick() {
    if (isMobile) setOpenMobile(false)
  }

  return (
    <SidebarMenuItem>
      <SidebarMenuButton
        asChild
        tooltip={item.name}
        isActive={isActive}
        className={cn(
          'text-muted-foreground',
          '[&>a>svg]:text-subtle',
          'hover:bg-sidebar-accent hover:text-accent-foreground',
          'data-[active=true]:bg-sidebar-accent data-[active=true]:text-accent-foreground',
          'data-[active=true]:[&>a>svg]:text-primary',
          isActive && 'active-sidebar-item',
        )}
      >
        <NavLink to={item.href} end={item.href === '/'} onClick={handleClick}>
          <Icon className="size-4 shrink-0" />
          <span className="truncate">{item.name}</span>
        </NavLink>
      </SidebarMenuButton>
    </SidebarMenuItem>
  )
}

function SidebarSectionMenu({ items }: { items: NavItem[] }) {
  return (
    <SidebarGroupContent>
      <SidebarMenu>
        {items.map((item) => (
          <NavLinkButton key={item.name} item={item} />
        ))}
      </SidebarMenu>
    </SidebarGroupContent>
  )
}

function SidebarNavSection({ section }: { section: NavSection }) {
  if (section.collapsible) {
    return (
      <Collapsible.Root defaultOpen={section.defaultOpen ?? true}>
        <SidebarGroup>
          {section.label ? (
            <Collapsible.Trigger className="flex w-full cursor-pointer items-center justify-between px-2 py-2 text-xs font-semibold tracking-wider text-subtle uppercase transition-colors hover:text-muted-foreground [&>svg]:size-3.5! [&>svg]:transition-transform [&>svg]:duration-200 [&>svg]:ease-out data-panel-open:[&>svg]:rotate-180">
              {section.label}
              <ChevronDown className="shrink-0" />
            </Collapsible.Trigger>
          ) : null}
          <Collapsible.Panel>
            <SidebarSectionMenu items={section.items} />
          </Collapsible.Panel>
        </SidebarGroup>
      </Collapsible.Root>
    )
  }

  return (
    <SidebarGroup>
      {section.label ? (
        <SidebarGroupLabel className="px-2 py-2 text-xs font-semibold tracking-wider text-subtle uppercase">
          {section.label}
        </SidebarGroupLabel>
      ) : null}
      <SidebarSectionMenu items={section.items} />
    </SidebarGroup>
  )
}

function GatewayStatusCard() {
  const settingsQuery = useSettings({ refetchInterval: 30_000 })
  const reachable = !settingsQuery.isError
  const version = settingsQuery.data?.version
  const uptime = settingsQuery.data?.uptime_seconds

  return (
    <div className="rounded-lg border border-border bg-card p-3">
      <div className="flex items-center gap-2 text-xs font-semibold text-foreground">
        <span
          className="size-2 shrink-0 rounded-full"
          style={{
            backgroundColor: reachable ? 'var(--success)' : 'var(--danger)',
            boxShadow: reachable ? '0 0 8px var(--success)' : undefined,
          }}
          aria-hidden="true"
        />
        {settingsQuery.isPending ? 'Connecting…' : reachable ? 'Gateway live' : 'Unreachable'}
      </div>
      <dl className="mt-2 space-y-1 font-mono text-[11px] text-muted-foreground">
        <div className="flex justify-between gap-2">
          <dt className="text-subtle">Version</dt>
          <dd className="truncate">{version ?? '—'}</dd>
        </div>
        <div className="flex justify-between gap-2">
          <dt className="text-subtle">Uptime</dt>
          <dd>{uptime === undefined ? '—' : formatUptime(uptime)}</dd>
        </div>
      </dl>
    </div>
  )
}

export function DashboardSidebar() {
  return (
    <Sidebar className="border-none! p-0.5 capitalio-dashboard">
      <SidebarHeader className="mb-4 flex-row items-start gap-2 px-4 pt-6 pb-0">
        <Brand />
      </SidebarHeader>

      <SidebarContent className="gap-2 px-4 pb-4">
        <SidebarNavSection section={operateSection} />
        <SidebarNavSection section={manageSection} />
      </SidebarContent>

      <SidebarFooter className="gap-3 px-4 pt-0 pb-5">
        <GatewayStatusCard />
      </SidebarFooter>
    </Sidebar>
  )
}
