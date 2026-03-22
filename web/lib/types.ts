export interface OverviewStats {
  total_messages: number;
  spam_count: number;
  media_count: number;
  users_joined: number;
  users_left: number;
  unique_users: number;
}

export interface TalkerStats {
  user_id: number;
  username: string;
  first_name: string;
  msg_count: number;
  sample_message: string;
}

export interface Channel {
  chat_id: string;
  title: string;
  msg_count: string;
  user_count: number;
  last_activity: number;
}

export interface SearchResult {
  match: {
    message_id: number;
    chat_id: number;
    user_id: number;
    username: string;
    first_name: string;
    text: string;
    timestamp: number;
    rspamd_score: number;
    is_spam: boolean;
    distance?: number;
  };
  context?: Array<{
    message_id: number;
    username: string;
    first_name: string;
    text: string;
    timestamp: number;
  }>;
}

export interface SystemInfo {
  rspamd_version: string;
  go_version: string;
  clickhouse: {
    total_rows: number;
    disk_bytes: number;
    oldest_message: number;
    newest_message: number;
  };
  redis: {
    total_keys: number;
  };
}
