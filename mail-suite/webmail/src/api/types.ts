export type User = { id: string; email: string; name: string; avatar_url?: string }
export type TokenPair = {
  access_token: string
  refresh_token: string
  expires_at: string
  user: User
}

export type TrackingSettings = {
  configured: boolean
  delivery: boolean
  open: boolean
  click: boolean
}

export type MailAccount = {
  id: string
  user_id: string
  display_name: string
  address: string
  provider: 'betazen' | 'imap'
  imap_host: string
  imap_port: number
  imap_ssl: boolean
  smtp_host: string
  smtp_port: number
  smtp_ssl: boolean
  username: string
  is_primary: boolean
  color?: string
  tracking?: TrackingSettings
}

export type Folder = {
  name: string
  delimiter: string
  total: number
  unread: number
  special?: 'inbox' | 'sent' | 'drafts' | 'spam' | 'trash'
}

export type Address = { name?: string; address: string }

export type MessageHeader = {
  uid: number
  folder: string
  message_id?: string
  thread_key?: string
  subject: string
  from: Address[]
  to?: Address[]
  cc?: Address[]
  date: string
  snippet: string
  unread: boolean
  starred: boolean
  has_attach: boolean
  size: number
}

export type MessageBody = {
  uid: number
  message_id: string
  subject: string
  from: Address[]
  to?: Address[]
  cc?: Address[]
  bcc?: Address[]
  reply_to?: Address[]
  date: string
  html?: string
  text?: string
  attachments?: Attachment[]
}

export type Attachment = {
  id: string
  filename: string
  content_type: string
  size: number
  is_inline?: boolean
}

export type Signature = {
  id: string
  user_id: string
  name: string
  html: string
  is_default: boolean
  created_at: string
  updated_at: string
}

export type Forwarder = {
  id: string
  account_id: string
  source: string
  destinations: string[]
  keep_copy: boolean
}

export type Draft = {
  id: string
  account_id: string
  to: string
  cc: string
  bcc: string
  subject: string
  html: string
  signature_id?: string
  in_reply_to?: string
  references?: string[]
  created_at: string
  updated_at: string
}

export type SentMessage = {
  id: string
  account_id: string
  track_id: string
  message_id: string
  subject: string
  to: Address[]
  cc?: Address[]
  bcc?: Address[]
  snippet: string
  track_delivery: boolean
  track_open: boolean
  track_click: boolean
  status: string
  open_count: number
  click_count: number
  first_open_at?: string
  last_open_at?: string
  sent_at: string
}

export type TrackingEvent = {
  id: string
  track_id: string
  account_id: string
  type: 'open' | 'click' | 'delivered' | 'bounced'
  url?: string
  ip?: string
  user_agent?: string
  at: string
}

export type ContactStatus = 'subscribed' | 'unsubscribed' | 'bounced' | 'complained'

export type Contact = {
  id: string
  user_id: string
  email: string
  name: string
  fields?: Record<string, string>
  group_ids: string[]
  status: ContactStatus
  source: string
  unsub_at?: string
  created_at: string
  updated_at: string
}

export type ContactGroup = {
  id: string
  user_id: string
  name: string
  description?: string
  color?: string
  contact_count: number
  created_at: string
  updated_at: string
}

export type ContactImportResult = {
  created: number
  updated: number
  skipped: number
  errors?: string[]
}

export type CampaignStatus = 'draft' | 'sending' | 'paused' | 'sent' | 'canceled' | 'failed'

export type Campaign = {
  id: string
  user_id: string
  account_id: string
  name: string
  subject: string
  html: string
  signature_id?: string
  group_ids: string[]
  mode: 'now' | 'drip'
  batch_size: number
  interval_seconds: number
  status: CampaignStatus
  total_recipients: number
  sent_count: number
  failed_count: number
  next_run_at?: string
  started_at?: string
  completed_at?: string
  created_at: string
  updated_at: string
}

export type CampaignStats = {
  total: number
  pending: number
  sent: number
  failed: number
  bounced: number
  unsubscribed: number
  opened: number
  clicked: number
  open_total: number
  click_total: number
}

export type CampaignTemplate = {
  id: string
  user_id: string
  name: string
  subject: string
  html: string
  created_at: string
  updated_at: string
}

export type CampaignRecipient = {
  id: string
  campaign_id: string
  email: string
  name: string
  status: string
  open_count: number
  click_count: number
  sent_at?: string
  error?: string
}

export type Envelope<T> = { success: boolean; data?: T; error?: string; code?: string }
