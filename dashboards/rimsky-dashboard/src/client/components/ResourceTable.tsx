import { type ReactNode } from 'react';
import { Link } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { useCursor } from '../lib/cursor';
import { Button } from './ui/button';
import { Card, CardContent } from './ui/card';
import { cn } from '../lib/utils';

export interface Column<T> {
  header: string;
  accessor: (row: T) => ReactNode;
  className?: string;
}

interface Page<T> {
  rows: T[];
  next_cursor: string;
}

interface Props<T> {
  queryKey: any[];
  fetchPage: (cursor: string) => Promise<Page<T>>;
  columns: Column<T>[];
  rowLink?: (row: T) => string;
  emptyMessage?: string;
}

export function ResourceTable<T>({
  queryKey,
  fetchPage,
  columns,
  rowLink,
  emptyMessage = 'No rows.',
}: Props<T>) {
  const { cursor, canGoBack, pushNext, popPrev } = useCursor();
  const { data, isLoading, error } = useQuery<Page<T>>({
    queryKey: [...queryKey, cursor],
    queryFn: () => fetchPage(cursor),
  });
  return (
    <Card>
      <CardContent className="p-0">
        {error && <p className="text-red-700 p-4">{String(error)}</p>}
        {isLoading && <p className="p-4 text-muted-foreground">Loading…</p>}
        {!isLoading && !error && (
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b">
                {columns.map((c) => (
                  <th
                    key={c.header}
                    className={cn('text-left px-3 py-2 font-medium text-muted-foreground', c.className)}
                  >
                    {c.header}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {(data?.rows ?? []).length === 0 && (
                <tr>
                  <td
                    colSpan={columns.length}
                    className="px-3 py-6 text-center text-muted-foreground"
                  >
                    {emptyMessage}
                  </td>
                </tr>
              )}
              {(data?.rows ?? []).map((row, idx) => (
                <tr key={idx} className="border-b hover:bg-muted/50">
                  {columns.map((c, ci) => (
                    <td key={ci} className={cn('px-3 py-2', c.className)}>
                      {ci === 0 && rowLink ? (
                        <Link to={rowLink(row)} className="hover:underline">
                          {c.accessor(row)}
                        </Link>
                      ) : (
                        c.accessor(row)
                      )}
                    </td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        )}
        <div className="p-3 flex justify-end gap-2">
          <Button variant="outline" size="sm" onClick={popPrev} disabled={!canGoBack}>
            Prev
          </Button>
          <Button
            variant="outline"
            size="sm"
            onClick={() => data?.next_cursor && pushNext(data.next_cursor)}
            disabled={!data?.next_cursor}
          >
            Next
          </Button>
        </div>
      </CardContent>
    </Card>
  );
}
