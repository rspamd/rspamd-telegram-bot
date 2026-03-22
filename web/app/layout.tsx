'use client'

import './globals.css'
import Link from 'next/link'
import { usePathname } from 'next/navigation'
import { useState, useEffect } from 'react'

const tabs = [
  { href: '/', label: 'Overview' },
  { href: '/search/', label: 'Search' },
  { href: '/rules/', label: 'Rules' },
  { href: '/system/', label: 'System' },
]

export default function RootLayout({ children }: { children: React.ReactNode }) {
  const pathname = usePathname()
  const [token, setToken] = useState<string | null>(null)
  const [tokenInput, setTokenInput] = useState('')

  useEffect(() => {
    const saved = localStorage.getItem('auth_token')
    if (saved) setToken(saved)
  }, [])

  const handleLogin = () => {
    localStorage.setItem('auth_token', tokenInput)
    setToken(tokenInput)
  }

  if (token === null) {
    return (
      <html lang="en">
        <body className="bg-gray-50 min-h-screen flex items-center justify-center">
          <div className="bg-white rounded-lg border p-8 w-80">
            <h1 className="text-lg font-bold text-gray-800 mb-4">Rspamd TG Bot</h1>
            <input
              type="password"
              className="w-full border rounded px-3 py-2 text-sm mb-3"
              placeholder="Auth token"
              value={tokenInput}
              onChange={(e) => setTokenInput(e.target.value)}
              onKeyDown={(e) => e.key === 'Enter' && handleLogin()}
            />
            <button
              onClick={handleLogin}
              className="w-full px-4 py-2 bg-blue-600 text-white rounded text-sm"
            >
              Login
            </button>
          </div>
        </body>
      </html>
    )
  }

  return (
    <html lang="en">
      <body className="bg-gray-50 min-h-screen">
        <div className="flex">
          <nav className="w-56 bg-white border-r border-gray-200 min-h-screen p-4">
            <h1 className="text-lg font-bold text-gray-800 mb-6">Rspamd TG Bot</h1>
            <ul className="space-y-1">
              {tabs.map((tab) => (
                <li key={tab.href}>
                  <Link
                    href={tab.href}
                    className={`block px-3 py-2 rounded text-sm ${
                      pathname === tab.href
                        ? 'bg-blue-50 text-blue-700 font-medium'
                        : 'text-gray-600 hover:bg-gray-50'
                    }`}
                  >
                    {tab.label}
                  </Link>
                </li>
              ))}
            </ul>
            <button
              onClick={() => { localStorage.removeItem('auth_token'); setToken(null) }}
              className="mt-4 text-xs text-gray-400 hover:text-gray-600"
            >
              Logout
            </button>
          </nav>
          <main className="flex-1 p-6">{children}</main>
        </div>
      </body>
    </html>
  )
}
