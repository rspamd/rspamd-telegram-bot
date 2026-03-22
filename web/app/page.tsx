'use client'

import { useEffect, useState } from 'react'
import { apiFetch } from '@/lib/api'
import { OverviewStats, TalkerStats, Channel } from '@/lib/types'

interface EventStats {
  bans: number
  deletes: number
  quiz_triggered: number
  quiz_passed: number
  quiz_failed: number
  restricts: number
}

interface TimelineBucket {
  bucket: string
  total: number
  spam: number
}

interface LengthBucket {
  range: string
  count: number
}

export default function OverviewPage() {
  const [channels, setChannels] = useState<Channel[]>([])
  const [selectedChannel, setSelectedChannel] = useState('')
  const [period, setPeriod] = useState('day')
  const [stats, setStats] = useState<OverviewStats | null>(null)
  const [talkers, setTalkers] = useState<TalkerStats[]>([])
  const [timeline, setTimeline] = useState<TimelineBucket[]>([])
  const [lengths, setLengths] = useState<LengthBucket[]>([])
  const [events, setEvents] = useState<EventStats | null>(null)

  useEffect(() => {
    apiFetch<Channel[]>('/api/stats/channels').then(setChannels).catch(console.error)
  }, [])

  useEffect(() => {
    const params = new URLSearchParams({ period })
    if (selectedChannel) params.set('chat_id', selectedChannel)

    apiFetch<OverviewStats>(`/api/stats/overview?${params}`).then(setStats).catch(console.error)
    apiFetch<TalkerStats[]>(`/api/stats/top-talkers?${params}&limit=20`).then(t => setTalkers(t || [])).catch(console.error)
    apiFetch<TimelineBucket[]>(`/api/stats/timeline?${params}`).then(t => setTimeline(t || [])).catch(console.error)
    apiFetch<LengthBucket[]>(`/api/stats/lengths?${params}`).then(l => setLengths(l || [])).catch(console.error)
    apiFetch<EventStats>(`/api/stats/actions?${params}`).then(setEvents).catch(console.error)
  }, [selectedChannel, period])

  const maxTalkerCount = talkers.length > 0 ? talkers[0].msg_count : 1
  const maxTimelineCount = timeline.length > 0 ? Math.max(...timeline.map(t => t.total)) : 1
  const maxLengthCount = lengths.length > 0 ? Math.max(...lengths.map(l => l.count)) : 1
  const totalLengthCount = lengths.reduce((sum, l) => sum + l.count, 0) || 1

  return (
    <div>
      <div className="flex items-center gap-4 mb-6">
        <h2 className="text-xl font-bold text-gray-800">Overview</h2>
        <select
          className="border rounded px-3 py-1.5 text-sm bg-white"
          value={selectedChannel}
          onChange={(e) => setSelectedChannel(e.target.value)}
        >
          <option value="">All channels</option>
          {channels.map((ch) => (
            <option key={ch.chat_id} value={ch.chat_id}>
              {ch.title || ch.chat_id}
            </option>
          ))}
        </select>
        <div className="flex gap-1">
          {['day', 'week', 'month'].map((p) => (
            <button
              key={p}
              onClick={() => setPeriod(p)}
              className={`px-3 py-1.5 rounded text-sm ${
                period === p ? 'bg-blue-600 text-white' : 'bg-white border text-gray-600'
              }`}
            >
              {p}
            </button>
          ))}
        </div>
      </div>

      {stats && (
        <div className="grid grid-cols-2 md:grid-cols-3 lg:grid-cols-6 gap-4 mb-8">
          <StatCard label="Messages" value={stats.total_messages} />
          <StatCard label="Spam" value={stats.spam_count} color="red" />
          <StatCard label="Media" value={stats.media_count} />
          <StatCard label="Joined" value={stats.users_joined} color="green" />
          <StatCard label="Unique users" value={stats.unique_users} />
          <StatCard label="Spam %" value={stats.total_messages > 0 ? Math.round(stats.spam_count / stats.total_messages * 100) : 0} color="orange" suffix="%" />
        </div>
      )}

      {events && (
        <div className="grid grid-cols-2 md:grid-cols-3 lg:grid-cols-6 gap-4 mb-8">
          <StatCard label="Banned" value={events.bans} color="red" />
          <StatCard label="Deleted" value={events.deletes} color="orange" />
          <StatCard label="Restricted" value={events.restricts} color="orange" />
          <StatCard label="Quizzes" value={events.quiz_triggered} />
          <StatCard label="Quiz passed" value={events.quiz_passed} color="green" />
          <StatCard label="Quiz failed" value={events.quiz_failed} color="red" />
        </div>
      )}

      {/* Message Timeline */}
      {timeline.length > 0 && (
        <div className="mb-8">
          <h3 className="text-lg font-semibold text-gray-700 mb-3">
            Message Volume ({period === 'day' ? 'hourly' : 'daily'})
          </h3>
          <div className="bg-white rounded-lg border p-4">
            <div className="flex items-end gap-px h-32">
              {timeline.map((t, i) => (
                <div key={i} className="flex-1 flex flex-col justify-end" title={`${t.bucket}: ${t.total} msgs, ${t.spam} spam`}>
                  {t.spam > 0 && (
                    <div
                      className="bg-red-400 rounded-t-sm"
                      style={{ height: `${(t.spam / maxTimelineCount) * 128}px` }}
                    />
                  )}
                  <div
                    className="bg-blue-400"
                    style={{ height: `${((t.total - t.spam) / maxTimelineCount) * 128}px` }}
                  />
                </div>
              ))}
            </div>
            <div className="flex justify-between mt-1 text-xs text-gray-400">
              <span>{timeline[0]?.bucket?.substring(5)}</span>
              <span>{timeline[timeline.length - 1]?.bucket?.substring(5)}</span>
            </div>
            <div className="flex gap-4 mt-2 text-xs text-gray-500">
              <span className="flex items-center gap-1"><span className="w-3 h-3 bg-blue-400 rounded-sm inline-block"/> Messages</span>
              <span className="flex items-center gap-1"><span className="w-3 h-3 bg-red-400 rounded-sm inline-block"/> Spam</span>
            </div>
          </div>
        </div>
      )}

      <div className="grid grid-cols-1 lg:grid-cols-2 gap-6 mb-8">
        {/* Top Talkers Bar Chart */}
        <TopTalkersChart
          talkers={talkers}
          maxCount={maxTalkerCount}
          chatID={selectedChannel}
        />

        {/* Message Length Distribution */}
        {lengths.length > 0 && (
          <div>
            <h3 className="text-lg font-semibold text-gray-700 mb-3">Message Length (chars)</h3>
            <div className="bg-white rounded-lg border p-4 space-y-2">
              {lengths.map((l) => (
                <div key={l.range} className="flex items-center gap-2">
                  <span className="text-xs text-gray-500 w-14 text-right font-mono">{l.range}</span>
                  <div className="flex-1 h-5 bg-gray-100 rounded-full overflow-hidden">
                    <div
                      className="h-full bg-emerald-500 rounded-full"
                      style={{ width: `${(l.count / maxLengthCount) * 100}%` }}
                    />
                  </div>
                  <span className="text-xs text-gray-500 font-mono w-20 text-right">{l.count.toLocaleString()} ({Math.round(l.count / totalLengthCount * 100)}%)</span>
                </div>
              ))}
            </div>
          </div>
        )}
      </div>
    </div>
  )
}

interface UserMessage {
  message_id: number
  text: string
  timestamp: number
  rspamd_score: number
  is_spam: boolean
}

function TopTalkersChart({ talkers, maxCount, chatID }: { talkers: TalkerStats[]; maxCount: number; chatID: string }) {
  const [expandedUser, setExpandedUser] = useState<number | null>(null)
  const [userMsgs, setUserMsgs] = useState<UserMessage[]>([])
  const [loading, setLoading] = useState(false)

  const toggleUser = async (userID: number) => {
    if (expandedUser === userID) {
      setExpandedUser(null)
      return
    }
    setExpandedUser(userID)
    setLoading(true)
    try {
      const params = new URLSearchParams({ user_id: String(userID) })
      if (chatID) params.set('chat_id', chatID)
      const msgs = await apiFetch<UserMessage[]>(`/api/stats/user-messages?${params}`)
      setUserMsgs(msgs || [])
    } catch (e) {
      console.error(e)
      setUserMsgs([])
    } finally {
      setLoading(false)
    }
  }

  return (
    <div>
      <h3 className="text-lg font-semibold text-gray-700 mb-3">Top Talkers</h3>
      <div className="bg-white rounded-lg border p-4 space-y-1">
        {talkers.slice(0, 15).map((t, i) => (
          <div key={t.user_id}>
            <div
              className="flex items-center gap-2 cursor-pointer hover:bg-gray-50 rounded px-1 py-1"
              onClick={() => toggleUser(t.user_id)}
            >
              <span className="text-xs text-gray-400 w-5 text-right">{i + 1}</span>
              <div className="flex-1">
                <div className="flex items-center justify-between mb-0.5">
                  <span className="text-sm font-medium truncate">
                    {t.first_name}
                    {t.username && <span className="text-gray-400 font-normal ml-1">@{t.username}</span>}
                  </span>
                  <span className="text-xs text-gray-500 font-mono ml-2">{t.msg_count}</span>
                </div>
                <div className="h-2 bg-gray-100 rounded-full overflow-hidden">
                  <div
                    className="h-full bg-blue-500 rounded-full"
                    style={{ width: `${(t.msg_count / maxCount) * 100}%` }}
                  />
                </div>
              </div>
            </div>
            {expandedUser === t.user_id && (
              <div className="ml-8 mt-1 mb-2 border-l-2 border-blue-200 pl-3">
                {loading ? (
                  <div className="text-xs text-gray-400 py-1">Loading...</div>
                ) : userMsgs.length === 0 ? (
                  <div className="text-xs text-gray-400 py-1">No messages found</div>
                ) : (
                  userMsgs.map((m) => (
                    <div key={m.message_id} className="text-xs py-0.5 flex gap-2">
                      <span className="text-gray-400 whitespace-nowrap">
                        {new Date(m.timestamp * 1000).toLocaleString([], { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })}
                      </span>
                      <span className={`flex-1 truncate ${m.is_spam ? 'text-red-500' : 'text-gray-600'}`}>
                        {m.text}
                      </span>
                    </div>
                  ))
                )}
              </div>
            )}
          </div>
        ))}
      </div>
    </div>
  )
}

function StatCard({ label, value, color, suffix }: { label: string; value: number; color?: string; suffix?: string }) {
  const colorClass = color === 'red' ? 'text-red-600' : color === 'green' ? 'text-green-600' : color === 'orange' ? 'text-orange-500' : 'text-gray-900'
  return (
    <div className="bg-white rounded-lg border p-4">
      <div className="text-xs text-gray-500 uppercase tracking-wide">{label}</div>
      <div className={`text-2xl font-bold mt-1 ${colorClass}`}>
        {value.toLocaleString()}{suffix}
      </div>
    </div>
  )
}
