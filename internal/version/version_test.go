// SPDX-License-Identifier: MIT

package version

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// TestReleaseVersionIsDocumented: every version has its release notes, indexed in the releases
// README, so a version moved without notes fails here.
func TestReleaseVersionIsDocumented(t *testing.T) {
	if !regexp.MustCompile(`^\d+\.\d+\.\d+$`).MatchString(Version) {
		t.Fatalf("Version %q is not X.Y.Z", Version)
	}
	releases := filepath.Join("..", "..", "docs", "releases")
	notes := "v" + Version + ".md"
	if _, err := os.Stat(filepath.Join(releases, notes)); err != nil {
		t.Fatalf("no release notes for %s: %v", Version, err)
	}
	index, err := os.ReadFile(filepath.Join(releases, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`\(` + regexp.QuoteMeta(notes) + `\)`).Match(index) {
		t.Fatalf("docs/releases/README.md does not link %s", notes)
	}
}
