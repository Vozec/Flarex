import type { LucideIcon } from "lucide-react";

// EmptyState renders the "nothing here yet" panel with an icon, a
// heading, subtext, and an optional CTA. Used across every tab so empty
// tables don't just show "—".
export default function EmptyState({
  icon: Icon,
  title,
  desc,
  cta,
}: {
  icon: LucideIcon;
  title: string;
  desc?: string;
  cta?: React.ReactNode;
}) {
  return (
    <div className="flex flex-col items-center justify-center gap-3 py-12 text-center">
      <div className="rounded-full bg-muted/50 p-4">
        <Icon className="h-6 w-6 text-muted-foreground" />
      </div>
      <div>
        <div className="text-sm font-semibold">{title}</div>
        {desc && <div className="mt-1 text-xs text-muted-foreground">{desc}</div>}
      </div>
      {cta}
    </div>
  );
}
