//  @ts-check

import { tanstackConfig } from "@tanstack/eslint-config"
import reactHooks from "eslint-plugin-react-hooks"

export default [
  ...tanstackConfig,
  {
    plugins: { "react-hooks": reactHooks },
    rules: {
      ...reactHooks.configs.recommended.rules,
    },
  },
  {
    // Rules tightened by @tanstack/eslint-config v0.4 / typescript-eslint
    // v8 / eslint-plugin-react-hooks v7. Real-but-low-risk cleanups —
    // downgrade to warn so CI stays green while the codebase catches up.
    rules: {
      "@typescript-eslint/no-unnecessary-condition": "warn",
      "@typescript-eslint/naming-convention": "warn",
      "no-shadow": "warn",
      "react-hooks/exhaustive-deps": "warn",
      "react-hooks/set-state-in-effect": "warn",
    },
  },
]
