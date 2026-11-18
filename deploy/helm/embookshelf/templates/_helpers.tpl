{{/*
The app's resource name. Defaults to the release name; an Argo CD
Application named embookshelf yields the names earlier plain manifests
used (embookshelf, embookshelf-data), so moving to the chart updates
those objects in place.
*/}}
{{- define "embookshelf.fullname" -}}
{{- .Values.fullnameOverride | default .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "embookshelf.image" -}}
{{- printf "%s:%s" .Values.image.repository (.Values.image.tag | default .Chart.AppVersion) -}}
{{- end -}}
