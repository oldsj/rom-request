import type { ComponentType } from 'react';

/** Renders every story a preview file exports, each under its own labelled heading. */
export function Stories({ name, stories }: { name: string; stories: Record<string, unknown> }) {
  const entries = Object.entries(stories).filter(
    (entry): entry is [string, ComponentType] => typeof entry[1] === 'function',
  );
  return (
    <main className="mx-auto max-w-3xl space-y-8 p-6">
      <h1 className="text-xl font-semibold">{name}</h1>
      {entries.map(([story, Story]) => (
        <section key={story} aria-labelledby={`story-${story}`} data-story={story}>
          <h2 id={`story-${story}`} className="text-muted-foreground mb-3 text-sm font-medium">
            {story}
          </h2>
          <Story />
        </section>
      ))}
    </main>
  );
}
