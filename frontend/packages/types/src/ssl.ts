export interface SSLCertificate {
  id: string;
  domain: string;
  issuer: string;
  type: "letsencrypt" | "custom";
  domains: string[];
  issued_at: string;
  expires_at: string;
  days_remaining: number;
  auto_renew: boolean;
  wildcard: boolean;
  key_type: string;
}
