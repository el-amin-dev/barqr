package version_test

import (
	"strings"
	"testing"

	"github.com/el-amin-dev/barqr/internal/version"
)

func TestGet(t *testing.T) {
	t.Parallel()

	info := version.Get()

	if got, want := info.Name, "barqr"; got != want {
		t.Errorf("Name = %q, want %q", got, want)
	}
	if !strings.HasPrefix(info.Go, "go1.") {
		t.Errorf("Go = %q, want a go1.x runtime version", info.Go)
	}
	if !strings.Contains(info.Platform, "/") {
		t.Errorf("Platform = %q, want os/arch", info.Platform)
	}
	if got, want := info.UserAgent, "barqr/"+version.Version; got != want {
		t.Errorf("UserAgent = %q, want %q", got, want)
	}
}

func TestInfoString(t *testing.T) {
	t.Parallel()

	s := version.Get().String()
	for _, want := range []string{"barqr", version.Version, version.Commit} {
		if !strings.Contains(s, want) {
			t.Errorf("String() = %q, want it to contain %q", s, want)
		}
	}
}
