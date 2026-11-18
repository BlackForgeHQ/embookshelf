# embookshelf UI

Next.js (App Router) + React + TypeScript + shadcn/ui, built as a static export
and embedded in the Go binary. See `docs/architecture.md`.

## Adding components

To add components to your app, run the following command:

```bash
npx shadcn@latest add button
```

This will place the ui components in the `components` directory.

## Using components

To use the components in your app, import them as follows:

```tsx
import { Button } from "@/components/ui/button";
```
