import React from "react";

interface StatusBadgeProps {
  status: string;
}

const statusColors: Record<string, string> = {
  active: "bg-green-500/10 text-green-400 border-green-500/20",
  running: "bg-green-500/10 text-green-400 border-green-500/20",
  live: "bg-green-500/10 text-green-400 border-green-500/20",
  completed: "bg-green-500/10 text-green-400 border-green-500/20",
  success: "bg-green-500/10 text-green-400 border-green-500/20",
  stopped: "bg-red-500/10 text-red-400 border-red-500/20",
  failed: "bg-red-500/10 text-red-400 border-red-500/20",
  suspended: "bg-yellow-500/10 text-yellow-400 border-yellow-500/20",
  warning: "bg-yellow-500/10 text-yellow-400 border-yellow-500/20",
  pending: "bg-blue-500/10 text-blue-400 border-blue-500/20",
  deploying: "bg-blue-500/10 text-blue-400 border-blue-500/20",
  building: "bg-blue-500/10 text-blue-400 border-blue-500/20",
  queued: "bg-gray-500/10 text-gray-400 border-gray-500/20",
};

export function StatusBadge({ status }: StatusBadgeProps) {
  const color = statusColors[status.toLowerCase()] || statusColors.queued;
  return (
    <span className={`inline-flex items-center px-2.5 py-0.5 rounded-full text-xs font-medium border ${color}`}>
      {status}
    </span>
  );
}
