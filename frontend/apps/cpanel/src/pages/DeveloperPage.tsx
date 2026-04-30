import { DeveloperPanel } from "@serverpanel/ui";

// User Panel (vendor / team) wrapper — passes scope="cpanel" so the shared
// DeveloperPanel hits /api/v1/cpanel/developer/*. Tenant scoping in the
// backend keeps the vendor restricted to their own tokens, webhooks, and
// delivery log.
export default function DeveloperPage() {
  return <DeveloperPanel scope="cpanel" />;
}
