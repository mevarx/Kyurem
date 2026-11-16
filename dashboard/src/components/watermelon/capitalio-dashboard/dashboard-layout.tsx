import { useCallback, useState, type CSSProperties, type ReactNode } from 'react'
import { DashboardSidebar } from './components/capitalio/sidebar'
import { DashboardTopbar } from './components/capitalio/topbar'
import { SidebarProvider } from '@/components/ui/sidebar'
import './dashboard.css'

const SIDEBAR_STORAGE_KEY = 'kyurem.sidebar'

function readStoredSidebarOpen(): boolean {
  if (typeof window === 'undefined') return true
  try {
    return window.localStorage.getItem(SIDEBAR_STORAGE_KEY) !== 'collapsed'
  } catch {
    return true
  }
}

type DashboardLayoutProps = {
  children: ReactNode
  onSearch: () => void
}

export default function DashboardLayout({ children, onSearch }: DashboardLayoutProps) {
  const [sidebarOpen, setSidebarOpen] = useState(readStoredSidebarOpen)

  const handleSidebarOpenChange = useCallback((open: boolean) => {
    setSidebarOpen(open)
    try {
      window.localStorage.setItem(SIDEBAR_STORAGE_KEY, open ? 'expanded' : 'collapsed')
    } catch {
      // Storage can be unavailable (private mode, disabled cookies); the toggle still works.
    }
  }, [])

  return (
    <SidebarProvider
      open={sidebarOpen}
      onOpenChange={handleSidebarOpenChange}
      className="capitalio-dashboard m4-shell h-svh overflow-hidden"
      style={
        {
          '--sidebar-width': '13.25rem',
        } as CSSProperties
      }
    >
      <DashboardSidebar />

      <main className="flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden">
        <div className="flex min-h-0 min-w-0 flex-1 flex-col bg-background">
          <DashboardTopbar onSearch={onSearch} />
          <div className="min-h-0 flex-1 overflow-y-auto">{children}</div>
        </div>
      </main>
    </SidebarProvider>
  )
}
