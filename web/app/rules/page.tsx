'use client'

import { useEffect, useState } from 'react'
import { apiFetch } from '@/lib/api'

export default function RulesPage() {
  const [regexps, setRegexps] = useState<string[]>([])
  const [urls, setUrls] = useState<string[]>([])
  const [newRegexp, setNewRegexp] = useState('')
  const [newUrl, setNewUrl] = useState('')

  const loadRules = () => {
    apiFetch<string[]>('/api/rules/regexp').then(r => setRegexps(r || [])).catch(console.error)
    apiFetch<string[]>('/api/rules/urls').then(r => setUrls(r || [])).catch(console.error)
  }

  useEffect(loadRules, [])

  const addRegexp = async () => {
    if (!newRegexp.trim()) return
    await apiFetch('/api/rules/regexp', { method: 'POST', body: JSON.stringify({ pattern: newRegexp }) })
    setNewRegexp('')
    loadRules()
  }

  const deleteRegexp = async (pattern: string) => {
    await apiFetch('/api/rules/regexp', { method: 'DELETE', body: JSON.stringify({ pattern }) })
    loadRules()
  }

  const addUrl = async () => {
    if (!newUrl.trim()) return
    await apiFetch('/api/rules/urls', { method: 'POST', body: JSON.stringify({ url: newUrl }) })
    setNewUrl('')
    loadRules()
  }

  const deleteUrl = async (url: string) => {
    await apiFetch('/api/rules/urls', { method: 'DELETE', body: JSON.stringify({ url }) })
    loadRules()
  }

  return (
    <div>
      <h2 className="text-xl font-bold text-gray-800 mb-6">Rules Management</h2>

      <div className="grid grid-cols-1 lg:grid-cols-2 gap-6">
        <div>
          <h3 className="text-lg font-semibold text-gray-700 mb-3">Regexp Patterns</h3>
          <div className="flex gap-2 mb-3">
            <input
              type="text"
              className="flex-1 border rounded px-3 py-2 text-sm"
              placeholder="/pattern/flags"
              value={newRegexp}
              onChange={(e) => setNewRegexp(e.target.value)}
              onKeyDown={(e) => e.key === 'Enter' && addRegexp()}
            />
            <button onClick={addRegexp} className="px-3 py-2 bg-green-600 text-white rounded text-sm">Add</button>
          </div>
          <div className="bg-white rounded-lg border divide-y max-h-96 overflow-y-auto">
            {regexps.length === 0 && <div className="px-4 py-3 text-sm text-gray-400">No patterns</div>}
            {regexps.map((p, i) => (
              <div key={i} className="flex items-center justify-between px-4 py-2 hover:bg-gray-50">
                <code className="text-sm text-gray-700 truncate flex-1">{p}</code>
                <button onClick={() => deleteRegexp(p)} className="text-red-500 text-xs ml-2 hover:text-red-700">delete</button>
              </div>
            ))}
          </div>
        </div>

        <div>
          <h3 className="text-lg font-semibold text-gray-700 mb-3">URL Blocklist</h3>
          <div className="flex gap-2 mb-3">
            <input
              type="text"
              className="flex-1 border rounded px-3 py-2 text-sm"
              placeholder="spamsite.com"
              value={newUrl}
              onChange={(e) => setNewUrl(e.target.value)}
              onKeyDown={(e) => e.key === 'Enter' && addUrl()}
            />
            <button onClick={addUrl} className="px-3 py-2 bg-green-600 text-white rounded text-sm">Add</button>
          </div>
          <div className="bg-white rounded-lg border divide-y max-h-96 overflow-y-auto">
            {urls.length === 0 && <div className="px-4 py-3 text-sm text-gray-400">No URLs</div>}
            {urls.map((u, i) => (
              <div key={i} className="flex items-center justify-between px-4 py-2 hover:bg-gray-50">
                <code className="text-sm text-gray-700 truncate flex-1">{u}</code>
                <button onClick={() => deleteUrl(u)} className="text-red-500 text-xs ml-2 hover:text-red-700">delete</button>
              </div>
            ))}
          </div>
        </div>
      </div>
    </div>
  )
}
