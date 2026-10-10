// Package templates holds embedded license texts and small file templates used
// when scaffolding a new local repository.
package templates

import (
	"embed"
	"fmt"
	"strings"
	"time"
)

//go:embed licenses/*.txt
var licenseFS embed.FS

// License describes one of the licenses we can stamp into a new project.
type License struct {
	// ID is the SPDX identifier (e.g. "MIT") and the name accepted on the CLI.
	ID string
	// Title is a human friendly name shown in help output.
	Title string
	// File is the embedded SPDX text.
	File string
}

// Licenses is the set of licenses offered, keyed by SPDX identifier.
var Licenses = map[string]License{
	"MIT":          {ID: "MIT", Title: "MIT License", File: "MIT.txt"},
	"Apache-2.0":   {ID: "Apache-2.0", Title: "Apache License 2.0", File: "Apache-2.0.txt"},
	"BSD-3-Clause": {ID: "BSD-3-Clause", Title: "BSD 3-Clause License", File: "BSD-3-Clause.txt"},
	"GPL-3.0-only": {ID: "GPL-3.0-only", Title: "GNU GPL v3.0", File: "GPL-3.0-only.txt"},
	"Unlicense":    {ID: "Unlicense", Title: "The Unlicense", File: "Unlicense.txt"},
}

// DefaultLicense is used when the user does not ask for a specific license.
const DefaultLicense = "MIT"

// LicenseIDs returns the license identifiers in a stable, sorted order.
func LicenseIDs() []string {
	ids := make([]string, 0, len(Licenses))
	for id := range Licenses {
		ids = append(ids, id)
	}
	// Licenses is small; sort for deterministic output.
	for i := 1; i < len(ids); i++ {
		for j := i; j > 0 && ids[j] < ids[j-1]; j-- {
			ids[j], ids[j-1] = ids[j-1], ids[j]
		}
	}
	return ids
}

// Render returns the license text with SPDX placeholders filled in.
//
// year   - the year to use in copyright lines
// holder - the copyright holder (author or organisation)
func (l License) Render(year int, holder string) (string, error) {
	raw, err := licenseFS.ReadFile("licenses/" + l.File)
	if err != nil {
		return "", fmt.Errorf("read license %s: %w", l.ID, err)
	}
	if year <= 0 {
		year = time.Now().Year()
	}
	if strings.TrimSpace(holder) == "" {
		holder = "The authors"
	}
	text := string(raw)

	repl := strings.NewReplacer(
		"<year>", fmt.Sprint(year),
		"<copyright holders>", holder,
		"<owner>", holder,
		"[yyyy]", fmt.Sprint(year),
		"[name of copyright owner]", holder,
		"<name of author>", holder,
	)
	return repl.Replace(text), nil
}

// LicenseByID looks up a license, accepting a few convenient aliases.
func LicenseByID(id string) (License, bool) {
	if id == "" {
		return Licenses[DefaultLicense], true
	}
	if l, ok := Licenses[id]; ok {
		return l, true
	}
	// Tolerate common shorthands.
	switch strings.ToLower(id) {
	case "mit":
		return Licenses["MIT"], true
	case "apache", "apache2", "apache-2":
		return Licenses["Apache-2.0"], true
	case "bsd", "bsd3":
		return Licenses["BSD-3-Clause"], true
	case "gpl3", "gpl-3", "gplv3":
		return Licenses["GPL-3.0-only"], true
	case "publicdomain", "unlicensed":
		return Licenses["Unlicense"], true
	}
	return License{}, false
}

// Gitignore renders a starter .gitignore for the given language. An empty or
// unknown language returns an empty string.
func Gitignore(language string) string {
	base := map[string]string{
		"go":         goGitignore,
		"golang":     goGitignore,
		"node":       nodeGitignore,
		"js":         nodeGitignore,
		"typescript": nodeGitignore,
		"ts":         nodeGitignore,
		"python":     pythonGitignore,
		"py":         pythonGitignore,
		"rust":       rustGitignore,
		"rs":         rustGitignore,
	}
	if t, ok := base[strings.ToLower(strings.TrimSpace(language))]; ok {
		return t
	}
	return ""
}
