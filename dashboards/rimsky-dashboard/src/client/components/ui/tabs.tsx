import { useState, type ReactNode } from 'react';
import { cn } from '../../lib/utils';

interface TabDef {
  id: string;
  label: string;
  content: ReactNode;
}

export function Tabs({
  tabs,
  defaultTab,
  className,
}: {
  tabs: TabDef[];
  defaultTab?: string;
  className?: string;
}) {
  const initial = defaultTab ?? tabs[0]?.id ?? '';
  const [active, setActive] = useState(initial);
  const tab = tabs.find((t) => t.id === active);
  return (
    <div className={cn('flex flex-col gap-4', className)}>
      <div className="flex border-b gap-2">
        {tabs.map((t) => (
          <button
            key={t.id}
            onClick={() => setActive(t.id)}
            className={cn(
              'px-3 py-2 text-sm font-medium border-b-2 -mb-px',
              t.id === active ? 'border-foreground text-foreground' : 'border-transparent text-muted-foreground hover:text-foreground',
            )}
          >
            {t.label}
          </button>
        ))}
      </div>
      <div>{tab?.content}</div>
    </div>
  );
}
