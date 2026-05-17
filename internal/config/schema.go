package config

// RepoConfig is the parsed .repo-mcp.yaml. Absence of the file is fine: the
// server still works as a generic Go nav, with zero-valued fields here.
type RepoConfig struct {
	Version     int                 `yaml:"version"`
	Resources   []Resource          `yaml:"resources,omitempty"`
	Lint        []LintEntry         `yaml:"lint,omitempty"`
	EntryPoints map[string][]string `yaml:"entry_points,omitempty"`
	Citations   []Citation          `yaml:"citations,omitempty"`
	Proto       []ProtoEntry        `yaml:"proto,omitempty"`
}

// Resource surfaces a file at <root>/<Path> as an MCP resource.
type Resource struct {
	Path        string `yaml:"path"`
	Title       string `yaml:"title,omitempty"`
	Description string `yaml:"description,omitempty"`
}

// LintEntry is the import path of an analysis.Analyzer plus its config.
// Analyzers must be linked into the binary; Import is the registry key.
type LintEntry struct {
	Import string         `yaml:"import"`
	Config map[string]any `yaml:"config,omitempty"`
}

// Citation maps a regex over comment text to a vendored source tree.
type Citation struct {
	Pattern     string `yaml:"pattern"`
	VendorRoot  string `yaml:"vendor_root"`
	Description string `yaml:"description,omitempty"`
}

// ProtoEntry names a proto-generated Go package to index for proto_field_xref.
type ProtoEntry struct {
	Import string `yaml:"import"`
}
