import { useState } from "react";
import { Button, Card } from "@serverpanel/ui";
import api from "@/lib/api";
import toast from "react-hot-toast";
import { RefreshCw, AlertTriangle, Power } from "lucide-react";

interface Props {
  mode: "graceful" | "forceful";
}

// Shared page used by both Graceful and Forceful Reboot sidebar entries.
// We don't show two separate pages because the only real difference is
// which endpoint we hit + the severity of the confirmation copy.
export default function RebootPage({ mode }: Props) {
  const [working, setWorking] = useState(false);
  const [done, setDone] = useState(false);

  const run = async () => {
    const label = mode === "graceful" ? "graceful" : "forceful";
    const yes = window.confirm(
      mode === "forceful"
        ? "Forceful reboot can cause data loss if services don't flush. Continue?"
        : "Schedule a graceful reboot? Services will stop cleanly before the machine comes down."
    );
    if (!yes) return;
    setWorking(true);
    try {
      await api.post(`/config/reboot/${mode}`);
      toast.success(`${label} reboot issued`);
      setDone(true);
    } catch (e: any) {
      toast.error(e?.response?.data?.error?.message || "Reboot command failed");
    } finally {
      setWorking(false);
    }
  };

  const isForce = mode === "forceful";

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-xl font-bold text-panel-text flex items-center gap-2">
          {isForce ? <Power size={20} className="text-red-400" /> : <RefreshCw size={20} className="text-amber-400" />}
          {isForce ? "Forceful Server Reboot" : "Graceful Server Reboot"}
        </h1>
        <p className="text-panel-muted text-sm mt-1">
          {isForce
            ? "Hard reboot via systemctl --force reboot. Used only when a graceful reboot hangs."
            : "Schedules shutdown -r +1 — services stop cleanly, then the box reboots in about a minute."}
        </p>
      </div>

      <Card>
        <div className="p-5 space-y-4">
          <div className={`rounded-lg border p-3 text-xs flex gap-2 ${
            isForce
              ? "border-red-500/30 bg-red-500/5 text-red-300"
              : "border-amber-500/30 bg-amber-500/5 text-amber-200/90"
          }`}>
            <AlertTriangle size={14} className="mt-0.5 shrink-0" />
            <div>
              {isForce ? (
                <>
                  <strong>Warning:</strong> This skips the normal systemd
                  shutdown order. Use only when the machine is already
                  unresponsive and a graceful reboot will not complete.
                  In-flight writes can be lost.
                </>
              ) : (
                <>
                  The panel API will respond first, then the box reboots in
                  roughly one minute. Operator can cancel with
                  <code className="px-1">shutdown -c</code> on SSH.
                </>
              )}
            </div>
          </div>

          {done ? (
            <div className="rounded-lg border border-emerald-500/30 bg-emerald-500/5 p-3 text-sm text-emerald-300">
              {isForce ? "Forceful reboot issued — the machine should be back in a few minutes."
                       : "Graceful reboot scheduled — the machine will be back in a few minutes."}
            </div>
          ) : (
            <Button
              onClick={run}
              loading={working}
              variant={isForce ? "danger" : "primary"}
            >
              <Power size={16} /> {isForce ? "Proceed with Forceful Reboot" : "Proceed with Graceful Reboot"}
            </Button>
          )}
        </div>
      </Card>
    </div>
  );
}
