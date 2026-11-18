// SPDX-License-Identifier: AGPL-3.0-or-later

package repo

import "context"

// SettingMCP names the app_settings row that switches the MCP server on
// for the whole instance (ADR-0039 §4).
const SettingMCP = "MCP"

// MCPConfig is the MCP row. Off by default: nothing is exposed to MCP
// clients, and no token can be minted, until an admin opts in. Turning
// it off keeps every token — they work again when it is turned back on.
type MCPConfig struct {
	Enabled bool `json:"enabled"`
}

func DefaultMCPConfig() MCPConfig {
	return MCPConfig{}
}

var mcpSetting = Setting[MCPConfig]{
	Key:     SettingMCP,
	Default: DefaultMCPConfig,
}

// GetMCP loads the row. A missing row yields the disabled default.
func (r *AppSettingsRepo) GetMCP(ctx context.Context) (MCPConfig, error) {
	return mcpSetting.Get(ctx, r)
}

// SetMCP upserts the row.
func (r *AppSettingsRepo) SetMCP(ctx context.Context, cfg MCPConfig) error {
	return mcpSetting.Set(ctx, r, cfg)
}
