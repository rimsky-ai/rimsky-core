// Copyright © 2026 Fall Guy Consulting.
// Licensed under the Apache License, Version 2.0.
// See LICENSE.apache at the repo root.

import { useState } from 'react';
import type { CustomUI } from '../types';
import { Button } from './ui/button';
import { Card, CardContent, CardHeader, CardTitle } from './ui/card';

export function CustomUIPanel({
  customUi,
  template,
  substitutions,
}: {
  customUi: CustomUI;
  template?: string;
  substitutions: Record<string, string>;
}) {
  const url = renderUrl(customUi.ui_url, template ?? '', substitutions);
  const isSameOrigin = isOriginSameAsCurrent(url);
  const sandbox = isSameOrigin
    ? 'allow-scripts allow-forms allow-same-origin'
    : 'allow-scripts allow-forms';

  const [tab, setTab] = useState<'embedded' | 'external'>(
    customUi.embed_mode === 'IFRAME' ? 'embedded' :
    customUi.embed_mode === 'LINK' ? 'external' : 'embedded',
  );

  return (
    <Card>
      <CardHeader>
        <CardTitle>Custom UI</CardTitle>
      </CardHeader>
      <CardContent>
        <div className="flex gap-2 mb-3">
          {(customUi.embed_mode === 'BOTH' || customUi.embed_mode === 'IFRAME') && (
            <Button variant={tab === 'embedded' ? 'default' : 'ghost'} size="sm" onClick={() => setTab('embedded')}>
              Embedded
            </Button>
          )}
          {(customUi.embed_mode === 'BOTH' || customUi.embed_mode === 'LINK') && (
            <Button variant={tab === 'external' ? 'default' : 'ghost'} size="sm" onClick={() => setTab('external')}>
              External
            </Button>
          )}
          <a href={url} target="_blank" rel="noreferrer noopener" className="ml-auto">
            <Button variant="outline" size="sm">Open in new tab ↗</Button>
          </a>
        </div>
        {tab === 'embedded' ? (
          <iframe
            src={url}
            sandbox={sandbox}
            referrerPolicy="no-referrer"
            className="w-full h-[480px] border rounded-md bg-background"
          />
        ) : (
          <p className="text-sm">
            <a href={url} target="_blank" rel="noreferrer noopener" className="underline">
              {url}
            </a>
          </p>
        )}
      </CardContent>
    </Card>
  );
}

function renderUrl(base: string, template: string, subs: Record<string, string>) {
  let path = template;
  for (const [k, v] of Object.entries(subs)) {
    path = path.replace(new RegExp(`\\{${k}\\}`, 'g'), encodeURIComponent(v));
  }
  return base.replace(/\/$/, '') + path;
}

function isOriginSameAsCurrent(url: string): boolean {
  try {
    const u = new URL(url);
    return u.origin === window.location.origin;
  } catch (_e) {
    return false;
  }
}
