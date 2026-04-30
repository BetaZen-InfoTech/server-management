import { DeveloperPanel } from "@serverpanel/ui";

// Thin wrapper around the shared DeveloperPanel — same UI for WHM and the
// User Panel; the `scope="whm"` prop selects /api/v1/whm/developer/* on the
// network side. Tenant scoping in the backend keeps the platform owner's
// view unrestricted.
export default function DeveloperPage() {
  return <DeveloperPanel scope="whm" />;
}
