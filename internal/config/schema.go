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
	DepIndex    DepIndexConfig      `yaml:"dep_index,omitempty"`
}

// DepIndexConfig controls which dependency tiers gopher-mcp indexes.
//
// Workspace packages are always indexed. Direct dependencies (listed in the
// root go.mod's require block) are indexed by default. Indirect deps and the
// standard library are opt-in — they multiply the indexed-symbol count by
// 10x+ on a typical repo.
//
// When the .repo-mcp.yaml is absent, the zero value applies and defaults to
// workspace + direct.
type DepIndexConfig struct {
	// Direct controls indexing of direct require'd modules. Nil means default
	// (true). Use a pointer so an explicit `direct: false` is distinguishable
	// from absence.
	Direct *bool `yaml:"direct,omitempty"`

	// Indirect indexes modules pulled in transitively (go.mod `// indirect`).
	Indirect bool `yaml:"indirect,omitempty"`

	// Stdlib indexes the Go standard library packages (those with no module).
	Stdlib bool `yaml:"stdlib,omitempty"`
}

// DirectEnabled returns whether direct deps are indexed. Defaults to true
// when unset.
func (d DepIndexConfig) DirectEnabled() bool {
	if d.Direct == nil {
		return true
	}
	return *d.Direct
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
