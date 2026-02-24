export interface DNSZone {
  id: string;
  domain: string;
  server_ip: string;
  nameservers: string[];
  dnssec_enabled: boolean;
  created_at: string;
}

export interface DNSRecord {
  id: string;
  zone_id: string;
  type: string;
  name: string;
  value: string;
  ttl: number;
  priority?: number;
  created_at: string;
}
