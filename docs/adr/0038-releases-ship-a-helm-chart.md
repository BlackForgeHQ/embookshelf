# Every release ships a Helm chart; clusters follow GHCR, not a Git commit

Deploying a release used to take a commit to the cluster's GitOps repository bumping one image tag. The question this ADR settles is how a release reaches a cluster without that commit, and what stays in Git.

## Status

accepted (2026-11-18)

## Decision

### 1. The chart is versioned with the app

`deploy/helm/embookshelf` holds the app's Deployment, Service and data claim, and the converter sidecar's Deployment and Service. On every `v*` tag, `image.yml` packages it at the release's version with `appVersion` equal to it, and pushes `oci://ghcr.io/blackforgehq/charts/embookshelf:<version>`. Chart version and app version are one number: there is no chart-only release.

The converter is released on its own cadence (ADR-0033). The chart pins the converter version current at release time, read from `.release-please-manifest.json` and written into `values.yaml` when the chart is packaged. A chart version therefore names a known pair of app and converter versions.

### 2. A cluster selects releases by a version range

The Argo CD Application points at the chart in GHCR with a semver range as `targetRevision` (Argo CD resolves ranges for OCI sources). Cluster-specific settings (the public URL, the S3 bucket and endpoint, secret names) are Helm values in the Application, in the GitOps repository. They change rarely; the release cadence no longer touches them. Resources that belong to the cluster rather than the app (the database cluster, the secret mapping, the route) stay as plain manifests beside the Application.

The rejected alternative is Argo CD Image Updater. It writes the new tag into the Application inside the cluster, which an app-of-apps with self-heal reverts on its next sync. Writing back to Git instead brings back the commit this removes.

### 3. The data claim is protected from the deploy tool

The claim carries `argocd.argoproj.io/sync-options: Prune=false,Delete=false`. A template rename or a deleted Application otherwise deletes the library's covers, cache and BookDrop. Resource names default to the release name, so an Application named `embookshelf` produces the names the earlier plain manifests used, and moving onto the chart updated those objects in place.

## Consequences

- What runs is decided by GHCR plus the range, not by a commit. Argo CD's UI shows the resolved version and digest. To hold or roll back a version, pin `targetRevision` to it.
- A published chart deploys without review, so CI lints and renders the chart on every pull request (`chart` job).
- Within a combined release, the converter image may finish building after the chart is published; the converter pod waits on the pull until it does.
