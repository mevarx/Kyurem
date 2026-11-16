import { motion, useReducedMotion } from 'motion/react'
import { Collapsible } from '@base-ui/react/collapsible'
import {
  Bot,
  ChevronDown,
  Coins,
  LayoutDashboard,
  ListTodo,
  Settings as SettingsIcon,
  ShieldCheck,
  Workflow,
} from 'lucide-react'
import { NavLink, useLocation } from 'react-router-dom'
import { useSettings } from '@/api/hooks'
import { ApiError } from '@/api/client'
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
  SidebarRail,
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
    { name: 'Overview', href: '/', icon: LayoutDashboard },
    { name: 'Tasks', href: '/tasks', icon: ListTodo },
    { name: 'Agent graph', href: '/graph', icon: Workflow },
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

function Brand({ onNavigate }: { onNavigate?: () => void }) {
  return (
    <NavLink
      to="/"
      className="flex min-w-0 flex-1 items-center gap-3 group-data-[collapsible=icon]:flex-none group-data-[collapsible=icon]:justify-center group-data-[collapsible=icon]:gap-0"
      onClick={onNavigate}
      title="Kyurem home"
    >
      <span className="kyurem-brand-mark" aria-hidden="true">
        <svg width="24" height="24" viewBox="0 0 24 24" fill="none">
          <path d="M6 3h5v7l6-7h6l-8 9 8 9h-6l-6-7v7H6V3Z" fill="currentColor" />
          <path d="M2 7h3v10H2V7Z" fill="currentColor" opacity=".45" />
        </svg>
      </span>
      <span className="flex min-w-0 flex-col gap-1 leading-none group-data-[collapsible=icon]:hidden">
        <span className="text-[17px] font-semibold tracking-tight text-foreground">Kyurem<span className="text-primary">.</span></span>
        <span className="text-[10px] tracking-wide text-subtle">AGENT WORKSPACE</span>
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
  const reduceMotion = useReducedMotion()
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
          'sidebar-nav-item',
          'text-sm font-medium text-muted-foreground',
          '[&>svg]:text-subtle',
          'hover:bg-[var(--surface-raised)] hover:text-foreground hover:[&>svg]:text-foreground',
          'data-[active=true]:bg-accent data-[active=true]:text-primary',
          'data-[active=true]:[&>svg]:text-primary',
        )}
      >
        <NavLink to={item.href} end={item.href === '/'} onClick={handleClick}>
          {isActive ? (
            <motion.span
              aria-hidden="true"
              layoutId="sidebar-active-bar"
              className="sidebar-active-bar"
              transition={reduceMotion ? { duration: 0 } : { type: 'spring', stiffness: 550, damping: 40 }}
            />
          ) : null}
          <Icon className="size-4 shrink-0" />
          <span className="truncate group-data-[collapsible=icon]:hidden">{item.name}</span>
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
        <SidebarGroup className="group-data-[collapsible=icon]:p-0">
          {section.label ? (
            <Collapsible.Trigger className="flex w-full cursor-pointer items-center justify-between px-2 py-2 text-[10px] font-medium tracking-[0.1em] text-subtle uppercase transition-colors group-data-[collapsible=icon]:hidden hover:text-muted-foreground [&>svg]:size-3.5! [&>svg]:transition-transform [&>svg]:duration-200 [&>svg]:ease-out data-panel-open:[&>svg]:rotate-180">
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
    <SidebarGroup className="group-data-[collapsible=icon]:p-0">
      {section.label ? (
        <SidebarGroupLabel className="px-2 py-2 text-[10px] font-medium tracking-[0.1em] text-subtle uppercase group-data-[collapsible=icon]:hidden">
          {section.label}
        </SidebarGroupLabel>
      ) : null}
      <SidebarSectionMenu items={section.items} />
    </SidebarGroup>
  )
}

function GatewayStatusCard() {
  const settingsQuery = useSettings({ refetchInterval: 30_000 })
  const reachable = !settingsQuery.isPending && !settingsQuery.isError
  const httpError = settingsQuery.error instanceof ApiError && settingsQuery.error.status > 0
  const version = settingsQuery.data?.version
  const uptime = settingsQuery.data?.uptime_seconds

  const statusLabel = settingsQuery.isPending
    ? 'Connecting…'
    : reachable
      ? 'Gateway live'
      : httpError ? 'API access issue' : 'Unreachable'

  return (
    <div
      role="status"
      aria-label={`Gateway status: ${statusLabel}`}
      title={statusLabel}
      className="kyurem-gateway-card rounded-xl border border-border bg-background p-3 group-data-[collapsible=icon]:flex group-data-[collapsible=icon]:size-8 group-data-[collapsible=icon]:items-center group-data-[collapsible=icon]:justify-center group-data-[collapsible=icon]:rounded-md group-data-[collapsible=icon]:border-transparent group-data-[collapsible=icon]:bg-transparent group-data-[collapsible=icon]:p-0"
    >
      <div className="flex items-center gap-2 text-xs font-semibold text-foreground group-data-[collapsible=icon]:gap-0">
        <span
          className="size-2 shrink-0 rounded-full"
          style={{
            backgroundColor: settingsQuery.isPending ? 'var(--text-subtle)' : reachable ? 'var(--success)' : 'var(--danger)',
          }}
          aria-hidden="true"
        />
        <span className="group-data-[collapsible=icon]:hidden">{statusLabel}</span>
      </div>
      <dl className="mt-2 space-y-1 text-[11px] text-muted-foreground group-data-[collapsible=icon]:hidden">
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
  const { isMobile, setOpenMobile } = useSidebar()
  const closeOnNavigate = () => {
    if (isMobile) setOpenMobile(false)
  }
  return (
    <Sidebar collapsible="icon" className="kyurem-sidebar border-r border-border capitalio-dashboard">
      <SidebarHeader className="mb-5 h-16 flex-row items-center gap-2 border-b border-border px-5 py-0 group-data-[collapsible=icon]:justify-center group-data-[collapsible=icon]:px-1.5">
        <Brand onNavigate={closeOnNavigate} />
      </SidebarHeader>

      <SidebarContent className="gap-5 px-3 pb-4 group-data-[collapsible=icon]:items-center group-data-[collapsible=icon]:px-1.5">
        <SidebarNavSection section={operateSection} />
        <SidebarNavSection section={manageSection} />
      </SidebarContent>

      <SidebarFooter className="gap-3 px-4 pt-0 pb-4 group-data-[collapsible=icon]:items-center group-data-[collapsible=icon]:px-1.5">
        <GatewayStatusCard />
      </SidebarFooter>

      {isMobile ? null : (
        <SidebarRail aria-label="Resize sidebar" title="Resize sidebar" />
      )}
    </Sidebar>
  )
}
