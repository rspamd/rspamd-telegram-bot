'use client'

import { useState } from 'react'
import { apiFetch } from '@/lib/api'
import { SearchResult } from '@/lib/types'

export default function SearchPage() {
  const [query, setQuery] = useState('')
  const [results, setResults] = useState<SearchResult[]>([])
  const [loading, setLoading] = useState(false)
  const [expandedIdx, setExpandedIdx] = useState<number | null>(null)

  const handleSearch = async () => {
    if (!query.trim()) return
    setLoading(true)
    try {
      const data = await apiFetch<SearchResult[]>('/api/search', {
        method: 'POST',
        body: JSON.stringify({ query, limit: 20 }),
      })
      setResults(data || [])
    } catch (e) {
      console.error(e)
    } finally {
      setLoading(false)
    }
  }

  return (
    <div>
      <h2 className="text-xl font-bold text-gray-800 mb-4">Message Search</h2>

      <div className="flex gap-2 mb-6">
        <input
          type="text"
          className="flex-1 border rounded px-3 py-2 text-sm"
          placeholder="Search messages (fuzzy match)..."
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          onKeyDown={(e) => e.key === 'Enter' && handleSearch()}
        />
        <button
          onClick={handleSearch}
          disabled={loading}
          className="px-4 py-2 bg-blue-600 text-white rounded text-sm hover:bg-blue-700 disabled:opacity-50"
        >
          {loading ? 'Searching...' : 'Search'}
        </button>
      </div>

      <div className="space-y-2">
        {results.map((r, i) => (
          <div key={i} className="bg-white rounded-lg border">
            <div
              className="px-4 py-3 cursor-pointer hover:bg-gray-50 flex items-start gap-3"
              onClick={() => setExpandedIdx(expandedIdx === i ? null : i)}
            >
              <div className="flex-1">
                <div className="flex items-center gap-2 mb-1">
                  <span className="font-medium text-sm">{r.match.first_name}</span>
                  {r.match.username && (
                    <span className="text-xs text-gray-400">@{r.match.username}</span>
                  )}
                  <span className="text-xs text-gray-400">
                    {new Date(r.match.timestamp * 1000).toLocaleString()}
                  </span>
                  {r.match.is_spam && (
                    <span className="text-xs bg-red-100 text-red-700 px-1.5 rounded">spam</span>
                  )}
                </div>
                <div className="text-sm text-gray-700">{r.match.text}</div>
              </div>
              {r.match.distance !== undefined && (
                <span className="text-xs text-gray-400 whitespace-nowrap">
                  dist: {r.match.distance.toFixed(2)}
                </span>
              )}
            </div>

            {expandedIdx === i && r.context && r.context.length > 0 && (
              <div className="border-t bg-gray-50 px-4 py-2">
                <div className="text-xs text-gray-500 mb-2">Context messages:</div>
                {r.context.map((c, j) => (
                  <div key={j} className="text-xs text-gray-600 py-0.5">
                    <span className="font-medium">{c.first_name}:</span> {c.text?.substring(0, 120)}
                  </div>
                ))}
              </div>
            )}
          </div>
        ))}
      </div>
    </div>
  )
}
