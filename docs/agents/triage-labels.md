# Triage Labels

The skills speak in terms of five canonical triage roles. This file maps those roles to the actual label strings used in this repo's GitHub issue tracker.

| Canonical role    | Label in this repo | Meaning                                  |
| ----------------- | ------------------ | ---------------------------------------- |
| `needs-triage`    | `needs-triage`     | Maintainer needs to evaluate this issue  |
| `needs-info`      | `needs-info`       | Waiting on reporter for more information |
| `ready-for-agent` | `ready-for-agent`  | Fully specified, ready for an AFK agent  |
| `ready-for-human` | `ready-for-human`  | Requires human implementation            |
| `wontfix`         | `wontfix`          | Will not be actioned                     |

When a skill mentions a role (e.g. "apply the AFK-ready triage label"), use the corresponding label string from this table.

## Bootstrapping notes

At setup time (2026-05-07) only `wontfix` existed on the GitHub repo. The other four labels are created on first use by `/triage` (it issues `gh label create` when the label is missing). To create them up front:

```bash
gh label create needs-triage --description "Maintainer needs to evaluate this issue" --color FBCA04
gh label create needs-info --description "Waiting on reporter for more information" --color FEF2C0
gh label create ready-for-agent --description "Fully specified, ready for an AFK agent" --color 0E8A16
gh label create ready-for-human --description "Requires human implementation" --color 1D76DB
```

Edit the right-hand column above if the label vocabulary changes later.
