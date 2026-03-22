'use client'

import { useEffect, useState } from 'react'
import { apiFetch } from '@/lib/api'
import { SystemInfo } from '@/lib/types'

export default function SystemPage() {
  const [info, setInfo] = useState<SystemInfo | null>(null)

  useEffect(() => {
    apiFetch<SystemInfo>('/api/system').then(setInfo).catch(console.error)
  }, [])

  if (!info) return <div className="text-gray-500">Loading...</div>

  return (
    <div>
      <h2 className="text-xl font-bold text-gray-800 mb-6">System Information</h2>

      <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
        <div className="bg-white rounded-lg border p-4">
          <h3 className="text-sm font-medium text-gray-500 uppercase tracking-wide mb-2">Rspamd</h3>
          <div className="text-lg font-mono">{info.rspamd_version || 'unknown'}</div>
        </div>

        <div className="bg-white rounded-lg border p-4">
          <h3 className="text-sm font-medium text-gray-500 uppercase tracking-wide mb-2">Go Runtime</h3>
          <div className="text-lg font-mono">{info.go_version}</div>
        </div>

        <div className="bg-white rounded-lg border p-4">
          <h3 className="text-sm font-medium text-gray-500 uppercase tracking-wide mb-2">Redis</h3>
          <div className="text-lg font-mono">{info.redis?.total_keys?.toLocaleString()} keys</div>
        </div>

        {info.clickhouse && (
          <>
            <div className="bg-white rounded-lg border p-4">
              <h3 className="text-sm font-medium text-gray-500 uppercase tracking-wide mb-2">ClickHouse Rows</h3>
              <div className="text-lg font-mono">{info.clickhouse.total_rows?.toLocaleString()}</div>
            </div>

            <div className="bg-white rounded-lg border p-4">
              <h3 className="text-sm font-medium text-gray-500 uppercase tracking-wide mb-2">Disk Usage</h3>
              <div className="text-lg font-mono">{formatBytes(info.clickhouse.disk_bytes)}</div>
            </div>

            <div className="bg-white rounded-lg border p-4">
              <h3 className="text-sm font-medium text-gray-500 uppercase tracking-wide mb-2">Data Range</h3>
              <div className="text-sm font-mono">
                {info.clickhouse.oldest_message ? new Date(info.clickhouse.oldest_message * 1000).toLocaleDateString() : '—'}
                {' → '}
                {info.clickhouse.newest_message ? new Date(info.clickhouse.newest_message * 1000).toLocaleDateString() : '—'}
              </div>
            </div>
          </>
        )}
      </div>
    </div>
  )
}

function formatBytes(bytes: number): string {
  if (!bytes) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB']
  let i = 0
  while (bytes >= 1024 && i < units.length - 1) {
    bytes /= 1024
    i++
  }
  return `${bytes.toFixed(1)} ${units[i]}`
}
